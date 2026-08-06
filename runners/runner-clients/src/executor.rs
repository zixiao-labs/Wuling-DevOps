//! Job execution: check out the repo, run each step via the selected backend
//! (container or host shell) while streaming logs, then report step/job status
//! to the control plane.

use std::path::{Path, PathBuf};

use anyhow::{Context, Result, anyhow};
use tokio::process::Command;
use tracing::{info, warn};

use crate::actions::{self, ActionCtx};
use crate::api::{AcquiredJob, ApiClient, StepSpec};
use crate::backend::{
    Backend, ContainerBackend, HostBackend, JobEnv, Platform, ResourceLimits, RunnerOS, StepTimeout,
};
use crate::toolcache::ToolCache;

/// Executes jobs in a container or on the host shell, chosen per job from the
/// runner's OS and whether the job requests a `container:`.
#[derive(Clone)]
pub struct Executor {
    api: ApiClient,
    work_dir: PathBuf,
    cache_dir: PathBuf,
    tools: ToolCache,
    state_dir: PathBuf,
    default_image: String,
    token: String,
    os: RunnerOS,
    limits: ResourceLimits,
}

struct StepCtx {
    cache_saves: Vec<(String, String)>,
    platform: Option<Platform>,
    temp_files: Vec<PathBuf>,
}

impl Executor {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        api: ApiClient,
        work_dir: PathBuf,
        tools_dir: PathBuf,
        state_dir: PathBuf,
        default_image: String,
        token: String,
        os: RunnerOS,
        limits: ResourceLimits,
    ) -> Self {
        let cache_dir = work_dir.join("_cache");
        Self {
            api,
            work_dir,
            cache_dir,
            tools: ToolCache::new(tools_dir),
            state_dir,
            default_image,
            token,
            os,
            limits,
        }
    }

    pub async fn run_job(&self, job: AcquiredJob) {
        let job_id = job.job_id.clone();
        info!(job_id, name = %job.job_name, run = job.run_number, "starting job");
        self.api
            .start_log_redaction(&job_id, job.secrets.values().cloned());
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
        if let Err(e) = self.api.finish_log_redaction(&job_id).await {
            warn!(job_id, error = %e, "failed to flush redacted log suffix");
        }
        if let Err(e) = self.api.complete(&job_id, conclusion).await {
            warn!(job_id, error = %e, "failed to report completion");
        }
        let _ = tokio::fs::remove_dir_all(self.job_dir(&job_id)).await;
        info!(job_id, conclusion, "job finished");
    }

    async fn execute(&self, job: &AcquiredJob) -> Result<bool> {
        let job_id = &job.job_id;
        let workspace = self.job_dir(job_id).join("workspace");
        tokio::fs::create_dir_all(&workspace)
            .await
            .context("create workspace")?;
        let workspace_abs = tokio::fs::canonicalize(&workspace).await?;

        let mut base_env: Vec<(String, String)> = Vec::new();
        for (k, v) in &job.spec.env {
            base_env.push((k.clone(), v.clone()));
        }
        for (k, v) in &job.secrets {
            base_env.push((k.clone(), v.clone()));
        }
        // The autoscaler sets this only after its OS-specific data-disk setup
        // completed. Keep it out of ordinary workloads; the internal
        // self-check uses it as a non-secret attestation that the runner
        // started on the configured non-OS work disk.
        if job
            .spec
            .env
            .get("WULING_SELF_CHECK_KIND")
            .map(String::as_str)
            == Some("runner-probe-v1")
            && matches!(
                std::env::var("WULING_RUNNER_DATA_DISK_READY").as_deref(),
                Ok("1")
            )
        {
            base_env.push(("WULING_RUNNER_DATA_DISK_READY".to_string(), "1".to_string()));
        }
        let mut job_env = JobEnv::new(base_env);
        let env_pairs = job_env.pairs();

        let use_container = match self.os {
            RunnerOS::Linux => true,
            RunnerOS::Windows => !job.spec.container.is_empty(),
            RunnerOS::MacOS => false,
        };
        if self.os == RunnerOS::MacOS && !job.spec.container.is_empty() {
            self.log(
                job_id,
                "[runner] note: container: is ignored on macOS; running steps on the host\n",
            )
            .await;
        }

        let tools_abs = self.tools.root().to_path_buf();
        let state_abs = self.state_dir.clone();

        let backend = if use_container {
            let image = if job.spec.container.is_empty() {
                self.default_image.clone()
            } else {
                job.spec.container.clone()
            };
            Backend::Container(
                ContainerBackend::start(
                    &self.api,
                    job_id,
                    &image,
                    &workspace_abs,
                    &tools_abs,
                    &state_abs,
                    &env_pairs,
                    self.os,
                    self.limits,
                )
                .await
                .context("start container")?,
            )
        } else {
            Backend::Host(HostBackend::new(
                workspace_abs.clone(),
                tools_abs.clone(),
                state_abs.clone(),
                self.os,
            ))
        };

        let mut job_failed = false;
        let mut step_ctx = StepCtx {
            cache_saves: Vec::new(),
            platform: None,
            temp_files: Vec::new(),
        };

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
                .run_step(
                    job,
                    &backend,
                    step,
                    &workspace_abs,
                    &mut job_env,
                    &mut step_ctx,
                )
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
                    let timed_out = e.downcast_ref::<StepTimeout>().is_some();
                    self.log(job_id, &format!("[runner] step error: {e}\n"))
                        .await;
                    let _ = self.api.patch_step(job_id, number, "failed").await;
                    if timed_out {
                        break;
                    }
                }
            }
        }

        for (key, path) in step_ctx.cache_saves {
            if let Err(e) = self.save_cache(&key, &workspace_abs, &path).await {
                warn!(error = %e, key, "cache save failed");
            }
        }
        for path in step_ctx.temp_files {
            let _ = tokio::fs::remove_file(&path).await;
        }

        drop(backend);
        Ok(job_failed)
    }

    async fn run_step(
        &self,
        job: &AcquiredJob,
        backend: &Backend,
        step: &StepSpec,
        workspace: &Path,
        env: &mut JobEnv,
        ctx: &mut StepCtx,
    ) -> Result<bool> {
        if !step.uses.is_empty() {
            let (action, uses_ref) = match step.uses.split_once('@') {
                Some((a, r)) => (a, r),
                None => (step.uses.as_str(), ""),
            };

            if action == "actions/checkout" {
                return self.do_checkout(&job.job_id, workspace, job).await;
            }
            if action == "actions/upload-artifact" {
                return self.do_upload_artifact(&job.job_id, workspace, step).await;
            }
            if action == "actions/cache" {
                let restored = self.do_cache_restore(&job.job_id, workspace, step).await?;
                if let (Some(key), Some(path)) = (step.with.get("key"), step.with.get("path")) {
                    ctx.cache_saves.push((key.clone(), path.clone()));
                }
                let _ = restored;
                return Ok(true);
            }

            if ctx.platform.is_none() {
                ctx.platform = Some(backend.probe().await?);
            }
            let platform = ctx.platform.as_ref().unwrap();

            let mut actx = ActionCtx {
                api: &self.api,
                job_id: &job.job_id,
                step,
                uses_ref,
                backend,
                workspace,
                tools: &self.tools,
                state: &self.state_dir,
                env,
                platform,
            };

            return match actions::dispatch(action, &mut actx).await {
                Some(r) => r,
                None => Err(anyhow!("unsupported action {action}")),
            };
        }
        backend.run_script(&self.api, &job.job_id, step, env).await
    }

    async fn do_checkout(&self, job_id: &str, workspace: &Path, job: &AcquiredJob) -> Result<bool> {
        let url = inject_basic_auth(&job.checkout.clone_url, "x-runner", &self.token);
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
            let _ = self.api.append_log(job_id, redact(&out.stderr)).await;
        }
        Ok(out.status.success())
    }

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
        let target = match resolve_in_workspace(workspace, &path) {
            Ok(t) => t,
            Err(e) => {
                self.log(job_id, &format!("[runner] upload-artifact: {e}\n"))
                    .await;
                return Ok(false);
            }
        };
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
        let dest = match resolve_in_workspace(workspace, &path) {
            Ok(d) => d,
            Err(e) => {
                self.log(job_id, &format!("[runner] cache: {e}\n")).await;
                return Ok(false);
            }
        };
        let cache_file = self.cache_dir.join(format!("{}.tar", sanitize(&key)));
        if tokio::fs::try_exists(&cache_file).await.unwrap_or(false) {
            let into = match dest.parent() {
                Some(p) if p.starts_with(workspace) => p.to_path_buf(),
                _ => workspace.to_path_buf(),
            };
            untar_into(&cache_file, &into).await?;
            self.log(job_id, &format!("[runner] cache restored: {key}\n"))
                .await;
        } else {
            self.log(job_id, &format!("[runner] cache miss: {key}\n"))
                .await;
        }
        Ok(true)
    }

    async fn save_cache(&self, key: &str, workspace: &Path, path: &str) -> Result<()> {
        let src = resolve_in_workspace(workspace, path)?;
        tokio::fs::create_dir_all(&self.cache_dir).await?;
        let cache_file = self.cache_dir.join(format!("{}.tar", sanitize(key)));
        let tar = tar_path(&src).await?;
        tokio::fs::write(&cache_file, tar).await?;
        Ok(())
    }

    fn job_dir(&self, job_id: &str) -> PathBuf {
        self.work_dir.join(job_id)
    }

    async fn log(&self, job_id: &str, msg: &str) {
        let _ = self.api.append_log(job_id, msg.as_bytes().to_vec()).await;
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

fn inject_basic_auth(url: &str, user: &str, pass: &str) -> String {
    if let Some(rest) = url.strip_prefix("https://") {
        return format!("https://{user}:{pass}@{rest}");
    }
    if let Some(rest) = url.strip_prefix("http://") {
        return format!("http://{user}:{pass}@{rest}");
    }
    url.to_string()
}

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

fn resolve_in_workspace(workspace: &Path, rel: &str) -> Result<PathBuf> {
    use std::path::Component;
    let mut out = PathBuf::new();
    for comp in Path::new(rel).components() {
        match comp {
            Component::CurDir => {}
            Component::Normal(c) => out.push(c),
            Component::ParentDir => {
                if !out.pop() {
                    return Err(anyhow!("path {rel:?} escapes the workspace"));
                }
            }
            Component::RootDir | Component::Prefix(_) => {
                return Err(anyhow!("path {rel:?} must be relative to the workspace"));
            }
        }
    }
    Ok(workspace.join(out))
}

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
