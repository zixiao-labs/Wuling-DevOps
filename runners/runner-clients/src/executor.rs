//! Job execution: check out the repo, run each step via the selected backend
//! (container or host shell) while streaming logs, then report step/job status
//! to the control plane.
//!
//! checkout / upload-artifact / cache are host-side filesystem operations and
//! run the same regardless of backend; only `run:` steps are dispatched to the
//! container or the host shell (see backend.rs).

use std::path::{Path, PathBuf};

use anyhow::{Context, Result, anyhow};
use tokio::process::Command;
use tracing::{info, warn};

use crate::api::{AcquiredJob, ApiClient, StepSpec};
use crate::backend::{Backend, ContainerBackend, HostBackend, RunnerOS, StepTimeout};

/// Executes jobs in a container or on the host shell, chosen per job from the
/// runner's OS and whether the job requests a `container:`.
#[derive(Clone)]
pub struct Executor {
    api: ApiClient,
    work_dir: PathBuf,
    cache_dir: PathBuf,
    default_image: String,
    token: String,
    os: RunnerOS,
}

impl Executor {
    pub fn new(
        api: ApiClient,
        work_dir: PathBuf,
        default_image: String,
        token: String,
        os: RunnerOS,
    ) -> Self {
        let cache_dir = work_dir.join("_cache");
        Self {
            api,
            work_dir,
            cache_dir,
            default_image,
            token,
            os,
        }
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
    /// failure (docker down, shell missing, etc.) which run_job maps to failed.
    async fn execute(&self, job: &AcquiredJob) -> Result<bool> {
        let job_id = &job.job_id;
        let workspace = self.job_dir(job_id).join("workspace");
        tokio::fs::create_dir_all(&workspace)
            .await
            .context("create workspace")?;
        let workspace_abs = tokio::fs::canonicalize(&workspace).await?;

        // Base env = job env + secrets. Step env overrides per-step (applied by
        // the backend). Carried as pairs so the host backend can set them on the
        // child process and the container backend can format them as KEY=VALUE.
        let mut base_env: Vec<(String, String)> = Vec::new();
        for (k, v) in &job.spec.env {
            base_env.push((k.clone(), v.clone()));
        }
        for (k, v) in &job.secrets {
            base_env.push((k.clone(), v.clone()));
        }

        // Backend choice: Linux always containerizes; Windows containerizes only
        // when the job sets `container:`; macOS always runs on the host (no
        // containers exist there).
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
                    &base_env,
                    self.os,
                )
                .await
                .context("start container")?,
            )
        } else {
            Backend::Host(HostBackend::new(workspace_abs.clone(), base_env, self.os))
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
                .run_step(job, &backend, step, &workspace, &mut cache_saves)
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
                        // The step's container/process tree was killed; running
                        // more steps (incl. always()) against it is pointless.
                        break;
                    }
                }
            }
        }

        // Persist caches requested by cache steps (best-effort).
        for (key, path) in cache_saves {
            if let Err(e) = self.save_cache(&key, &workspace, &path).await {
                warn!(error = %e, key, "cache save failed");
            }
        }

        // Dropping the backend force-removes a container (if any); the host
        // backend has nothing to release.
        drop(backend);
        Ok(job_failed)
    }

    /// run_step dispatches by step kind. Returns Ok(success_bool). The built-in
    /// actions are host-side; a plain `run:` goes to the execution backend.
    async fn run_step(
        &self,
        job: &AcquiredJob,
        backend: &Backend,
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
        // A `run` step: execute the script via the selected backend.
        backend.run_script(&self.api, &job.job_id, step).await
    }

    // ---- built-in actions (host-side, backend-independent) ------------------

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
            // tar_path stored the directory under its own basename, so restore
            // into the PARENT of dest to land contents back at workspace/<path>
            // (not the old workspace/<path>/<path> double-nest). Clamp to the
            // workspace so a top-level path never extracts above it.
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

    // ---- helpers ------------------------------------------------------------

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

/// resolve_in_workspace joins a user-supplied relative path onto the workspace,
/// rejecting anything that escapes it (absolute paths or `..` traversal). The
/// check is lexical so it also works for paths that don't exist yet (e.g. a
/// cache restore target).
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
