//! Job execution: check out the repo, spin up a container, run each step in
//! it while streaming logs, then report step/job status to the control plane.

use std::path::{Path, PathBuf};

use anyhow::{Context, Result, anyhow};
use bollard::Docker;
use bollard::container::{Config, CreateContainerOptions, LogOutput, RemoveContainerOptions};
use bollard::exec::{CreateExecOptions, StartExecResults};
use bollard::image::CreateImageOptions;
use futures_util::StreamExt;
use tokio::process::Command;
use tracing::{info, warn};

use crate::api::{AcquiredJob, ApiClient, StepSpec};

const WORKSPACE_MOUNT: &str = "/workspace";
const STEP_TIMEOUT_DEFAULT_MINS: u64 = 60;

/// Executes jobs in a local container runtime.
#[derive(Clone)]
pub struct Executor {
    api: ApiClient,
    docker: Docker,
    work_dir: PathBuf,
    cache_dir: PathBuf,
    default_image: String,
    token: String,
}

impl Executor {
    pub fn new(
        api: ApiClient,
        work_dir: PathBuf,
        default_image: String,
        token: String,
    ) -> Result<Self> {
        let docker = Docker::connect_with_local_defaults().context("connect to docker")?;
        let cache_dir = work_dir.join("_cache");
        Ok(Self {
            api,
            docker,
            work_dir,
            cache_dir,
            default_image,
            token,
        })
    }

    /// run_job executes a job end to end and always reports a terminal
    /// conclusion to the control plane (failed on any internal error).
    pub async fn run_job(&self, job: AcquiredJob) {
        let job_id = job.job_id.clone();
        info!(job_id, name = %job.job_name, run = job.run_number, "starting job");
        let conclusion = match self.execute(&job).await {
            Ok(failed) => {
                if failed {
                    "failed"
                } else {
                    "success"
                }
            }
            Err(e) => {
                warn!(job_id, error = %e, "job execution error");
                let _ = self
                    .api
                    .append_log(
                        &job_id,
                        format!("\n[runner] internal error: {e}\n").into_bytes(),
                    )
                    .await;
                "failed"
            }
        };
        if let Err(e) = self.api.complete(&job_id, conclusion).await {
            warn!(job_id, error = %e, "failed to report completion");
        }
        // Best-effort cleanup of the job workspace.
        let _ = tokio::fs::remove_dir_all(self.job_dir(&job_id)).await;
        info!(job_id, conclusion, "job finished");
    }

    /// execute returns Ok(job_failed). A returned Err is an infrastructure
    /// failure (docker down, etc.) which run_job maps to a failed job.
    async fn execute(&self, job: &AcquiredJob) -> Result<bool> {
        let job_id = &job.job_id;
        let workspace = self.job_dir(job_id).join("workspace");
        tokio::fs::create_dir_all(&workspace)
            .await
            .context("create workspace")?;
        let workspace_abs = tokio::fs::canonicalize(&workspace).await?;

        let image = if job.spec.container.is_empty() {
            self.default_image.clone()
        } else {
            job.spec.container.clone()
        };
        self.log(
            job_id,
            &format!("[runner] preparing container image {image}\n"),
        )
        .await;
        self.pull_image(&image).await.context("pull image")?;

        // Base container env = job env + secrets. Step env overrides per-exec.
        let mut base_env: Vec<String> = Vec::new();
        for (k, v) in &job.spec.env {
            base_env.push(format!("{k}={v}"));
        }
        for (k, v) in &job.secrets {
            base_env.push(format!("{k}={v}"));
        }

        let container_id = self
            .create_container(&image, &workspace_abs, &base_env)
            .await
            .context("create container")?;
        // Ensure the container is torn down whatever happens below.
        let cleanup = ContainerGuard {
            docker: self.docker.clone(),
            id: container_id.clone(),
        };

        let mut job_failed = false;
        let mut cache_saves: Vec<(String, String)> = Vec::new();

        for (i, step) in job.spec.steps.iter().enumerate() {
            let number = i + 1;
            if !should_run(&step.if_, job_failed) {
                let _ = self.api.patch_step(job_id, number, "skipped").await;
                continue;
            }
            let _ = self.api.patch_step(job_id, number, "running").await;
            self.log(
                job_id,
                &format!(
                    "\n\u{2500}\u{2500} step {number}: {} \u{2500}\u{2500}\n",
                    display_name(step)
                ),
            )
            .await;

            let step_result = self
                .run_step(job, &container_id, step, &workspace, &mut cache_saves)
                .await;

            match step_result {
                Ok(true) => {
                    let _ = self.api.patch_step(job_id, number, "success").await;
                }
                Ok(false) => {
                    job_failed = true;
                    let _ = self.api.patch_step(job_id, number, "failed").await;
                }
                Err(e) => {
                    job_failed = true;
                    self.log(job_id, &format!("[runner] step error: {e}\n"))
                        .await;
                    let _ = self.api.patch_step(job_id, number, "failed").await;
                }
            }
        }

        // Persist caches requested by cache steps (best-effort).
        for (key, path) in cache_saves {
            if let Err(e) = self.save_cache(&key, &workspace, &path).await {
                warn!(error = %e, key, "cache save failed");
            }
        }

        drop(cleanup);
        Ok(job_failed)
    }

    /// run_step dispatches by step kind. Returns Ok(success_bool).
    async fn run_step(
        &self,
        job: &AcquiredJob,
        container_id: &str,
        step: &StepSpec,
        workspace: &Path,
        cache_saves: &mut Vec<(String, String)>,
    ) -> Result<bool> {
        if !step.uses.is_empty() {
            let action = step.uses.split('@').next().unwrap_or("");
            return match action {
                "actions/checkout" => self.do_checkout(&job.job_id, workspace, job).await,
                "actions/upload-artifact" => {
                    self.do_upload_artifact(&job.job_id, workspace, step).await
                }
                "actions/cache" => {
                    let restored = self.do_cache_restore(&job.job_id, workspace, step).await?;
                    if let (Some(key), Some(path)) = (step.with.get("key"), step.with.get("path")) {
                        cache_saves.push((key.clone(), path.clone()));
                    }
                    let _ = restored;
                    Ok(true)
                }
                other => Err(anyhow!("unsupported action {other}")),
            };
        }
        // A `run` step: execute the script in the container.
        self.exec_run(&job.job_id, container_id, step).await
    }

    // ---- container plumbing -------------------------------------------------

    async fn pull_image(&self, image: &str) -> Result<()> {
        let opts = CreateImageOptions::<String> {
            from_image: image.to_string(),
            ..Default::default()
        };
        let mut stream = self.docker.create_image(Some(opts), None, None);
        while let Some(item) = stream.next().await {
            item.context("pull image layer")?;
        }
        Ok(())
    }

    async fn create_container(
        &self,
        image: &str,
        workspace_abs: &Path,
        env: &[String],
    ) -> Result<String> {
        let bind = format!("{}:{WORKSPACE_MOUNT}", workspace_abs.display());
        let host_config = bollard::models::HostConfig {
            binds: Some(vec![bind]),
            ..Default::default()
        };
        let config = Config {
            image: Some(image.to_string()),
            cmd: Some(vec!["sleep".to_string(), "infinity".to_string()]),
            env: Some(env.to_vec()),
            working_dir: Some(WORKSPACE_MOUNT.to_string()),
            host_config: Some(host_config),
            tty: Some(false),
            ..Default::default()
        };
        let created = self
            .docker
            .create_container(None::<CreateContainerOptions<String>>, config)
            .await?;
        self.docker
            .start_container(
                &created.id,
                None::<bollard::container::StartContainerOptions<String>>,
            )
            .await?;
        Ok(created.id)
    }

    /// exec_run runs a shell script inside the container, streaming combined
    /// stdout/stderr to the job log, and returns Ok(true) on exit code 0.
    async fn exec_run(&self, job_id: &str, container_id: &str, step: &StepSpec) -> Result<bool> {
        let mut env: Vec<String> = Vec::new();
        for (k, v) in &step.env {
            env.push(format!("{k}={v}"));
        }
        let exec = self
            .docker
            .create_exec(
                container_id,
                CreateExecOptions::<String> {
                    cmd: Some(vec!["sh".into(), "-ec".into(), step.run.clone()]),
                    env: Some(env),
                    working_dir: Some(WORKSPACE_MOUNT.into()),
                    attach_stdout: Some(true),
                    attach_stderr: Some(true),
                    ..Default::default()
                },
            )
            .await?;

        let timeout = std::time::Duration::from_secs(
            60 * if step.timeout_minutes > 0 {
                step.timeout_minutes
            } else {
                STEP_TIMEOUT_DEFAULT_MINS
            },
        );
        let drained = tokio::time::timeout(timeout, self.drain_exec(job_id, &exec.id)).await;
        match drained {
            Err(_) => {
                self.log(job_id, "[runner] step timed out\n").await;
                Ok(false)
            }
            Ok(Err(e)) => Err(e),
            Ok(Ok(())) => {
                let inspect = self.docker.inspect_exec(&exec.id).await?;
                Ok(inspect.exit_code.unwrap_or(0) == 0)
            }
        }
    }

    async fn drain_exec(&self, job_id: &str, exec_id: &str) -> Result<()> {
        let start = self.docker.start_exec(exec_id, None).await?;
        if let StartExecResults::Attached { mut output, .. } = start {
            let mut buf: Vec<u8> = Vec::with_capacity(8192);
            while let Some(item) = output.next().await {
                let msg = item?;
                let bytes = match msg {
                    LogOutput::StdOut { message }
                    | LogOutput::StdErr { message }
                    | LogOutput::Console { message } => message,
                    LogOutput::StdIn { .. } => continue,
                };
                buf.extend_from_slice(&bytes);
                if buf.len() >= 8192 {
                    let chunk = std::mem::take(&mut buf);
                    let _ = self.api.append_log(job_id, chunk).await;
                }
            }
            if !buf.is_empty() {
                let _ = self.api.append_log(job_id, buf).await;
            }
        }
        Ok(())
    }

    // ---- built-in actions ---------------------------------------------------

    /// do_checkout clones the repo at the dispatched SHA into the workspace,
    /// authenticating with this runner's own token (read-only, org-scoped).
    async fn do_checkout(&self, job_id: &str, workspace: &Path, job: &AcquiredJob) -> Result<bool> {
        let url = inject_basic_auth(&job.checkout.clone_url, "x-runner", &self.token);
        // Fresh clone into the (empty) workspace, then hard-checkout the sha.
        let ws = workspace.to_string_lossy().to_string();
        let ok1 = self
            .run_host_git(job_id, &["clone", "--quiet", &url, &ws])
            .await?;
        if !ok1 {
            return Ok(false);
        }
        let ok2 = self
            .run_host_git(
                job_id,
                &["-C", &ws, "checkout", "--quiet", &job.checkout.sha],
            )
            .await?;
        Ok(ok2)
    }

    async fn run_host_git(&self, job_id: &str, args: &[&str]) -> Result<bool> {
        let out = Command::new("git")
            .args(args)
            .output()
            .await
            .context("run git")?;
        if !out.stdout.is_empty() {
            let _ = self.api.append_log(job_id, out.stdout.clone()).await;
        }
        if !out.stderr.is_empty() {
            // git prints progress to stderr; surface it but don't treat as fatal.
            let _ = self.api.append_log(job_id, redact(&out.stderr)).await;
        }
        Ok(out.status.success())
    }

    /// do_upload_artifact tars `with.path` (relative to workspace) and uploads
    /// it under `with.name`.
    async fn do_upload_artifact(
        &self,
        job_id: &str,
        workspace: &Path,
        step: &StepSpec,
    ) -> Result<bool> {
        let name = step
            .with
            .get("name")
            .cloned()
            .unwrap_or_else(|| "artifact".to_string());
        let path = match step.with.get("path") {
            Some(p) => p.clone(),
            None => {
                self.log(job_id, "[runner] upload-artifact: missing `path`\n")
                    .await;
                return Ok(false);
            }
        };
        let target = workspace.join(&path);
        let tar = tar_path(&target).await?;
        self.log(
            job_id,
            &format!("[runner] uploading artifact {name} ({} bytes)\n", tar.len()),
        )
        .await;
        self.api.upload_artifact(job_id, &name, tar).await?;
        Ok(true)
    }

    async fn do_cache_restore(
        &self,
        job_id: &str,
        workspace: &Path,
        step: &StepSpec,
    ) -> Result<bool> {
        let (key, path) = match (step.with.get("key"), step.with.get("path")) {
            (Some(k), Some(p)) => (k.clone(), p.clone()),
            _ => {
                self.log(job_id, "[runner] cache: missing `key` or `path`\n")
                    .await;
                return Ok(false);
            }
        };
        let cache_file = self.cache_dir.join(format!("{}.tar", sanitize(&key)));
        if tokio::fs::try_exists(&cache_file).await.unwrap_or(false) {
            let dest = workspace.join(&path);
            untar_into(&cache_file, &dest).await?;
            self.log(job_id, &format!("[runner] cache restored: {key}\n"))
                .await;
        } else {
            self.log(job_id, &format!("[runner] cache miss: {key}\n"))
                .await;
        }
        Ok(true)
    }

    async fn save_cache(&self, key: &str, workspace: &Path, path: &str) -> Result<()> {
        tokio::fs::create_dir_all(&self.cache_dir).await?;
        let cache_file = self.cache_dir.join(format!("{}.tar", sanitize(key)));
        let src = workspace.join(path);
        let tar = tar_path(&src).await?;
        tokio::fs::write(&cache_file, tar).await?;
        Ok(())
    }

    // ---- helpers ------------------------------------------------------------

    fn job_dir(&self, job_id: &str) -> PathBuf {
        self.work_dir.join(job_id)
    }

    async fn log(&self, job_id: &str, msg: &str) {
        let _ = self.api.append_log(job_id, msg.as_bytes().to_vec()).await;
    }
}

/// Drops force-remove the container so a panic or early return never leaks it.
struct ContainerGuard {
    docker: Docker,
    id: String,
}

impl Drop for ContainerGuard {
    fn drop(&mut self) {
        let docker = self.docker.clone();
        let id = self.id.clone();
        tokio::spawn(async move {
            let _ = docker
                .remove_container(
                    &id,
                    Some(RemoveContainerOptions {
                        force: true,
                        ..Default::default()
                    }),
                )
                .await;
        });
    }
}

fn should_run(if_expr: &str, job_failed: bool) -> bool {
    match if_expr.trim() {
        "" | "success()" => !job_failed,
        "failure()" => job_failed,
        "always()" => true,
        _ => !job_failed,
    }
}

fn display_name(step: &StepSpec) -> String {
    if !step.name.is_empty() {
        return step.name.clone();
    }
    if !step.uses.is_empty() {
        return step.uses.clone();
    }
    step.run.lines().next().unwrap_or("step").trim().to_string()
}

/// inject_basic_auth rewrites https://host/… into https://user:pass@host/….
fn inject_basic_auth(url: &str, user: &str, pass: &str) -> String {
    if let Some(rest) = url.strip_prefix("https://") {
        return format!("https://{user}:{pass}@{rest}");
    }
    if let Some(rest) = url.strip_prefix("http://") {
        return format!("http://{user}:{pass}@{rest}");
    }
    url.to_string()
}

/// redact masks anything that looks like a wlrt_ token in git's stderr so a
/// clone URL with embedded credentials never lands in the public log.
fn redact(bytes: &[u8]) -> Vec<u8> {
    let s = String::from_utf8_lossy(bytes);
    let mut out = String::with_capacity(s.len());
    for part in s.split_inclusive(|c: char| c.is_whitespace()) {
        if part.contains("wlrt_") || part.contains("x-runner:") {
            out.push_str("[redacted]\n");
        } else {
            out.push_str(part);
        }
    }
    out.into_bytes()
}

fn sanitize(key: &str) -> String {
    key.chars()
        .map(|c| {
            if c.is_alphanumeric() || c == '-' || c == '_' || c == '.' {
                c
            } else {
                '_'
            }
        })
        .collect()
}

/// tar_path builds an uncompressed tar of a file or directory in memory. Runs
/// the synchronous tar work on a blocking thread.
async fn tar_path(path: &Path) -> Result<Vec<u8>> {
    let path = path.to_path_buf();
    tokio::task::spawn_blocking(move || -> Result<Vec<u8>> {
        if !path.exists() {
            return Err(anyhow!("path does not exist: {}", path.display()));
        }
        let mut buf = Vec::new();
        {
            let mut builder = tar::Builder::new(&mut buf);
            let name = path
                .file_name()
                .map(|s| s.to_string_lossy().to_string())
                .unwrap_or_else(|| "data".to_string());
            if path.is_dir() {
                builder.append_dir_all(&name, &path)?;
            } else {
                let mut f = std::fs::File::open(&path)?;
                builder.append_file(&name, &mut f)?;
            }
            builder.finish()?;
        }
        Ok(buf)
    })
    .await?
}

async fn untar_into(tar_file: &Path, dest: &Path) -> Result<()> {
    let tar_file = tar_file.to_path_buf();
    let dest = dest.to_path_buf();
    tokio::task::spawn_blocking(move || -> Result<()> {
        std::fs::create_dir_all(&dest)?;
        let f = std::fs::File::open(&tar_file)?;
        let mut ar = tar::Archive::new(f);
        ar.unpack(&dest)?;
        Ok(())
    })
    .await?
}
