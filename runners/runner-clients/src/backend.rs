//! Step execution backends.
//!
//! A job's `run:` steps execute either **inside a container** (the Stage-1
//! model — Linux always; Windows when the job sets `container:`) or **directly
//! on the runner's host shell** (macOS always; Windows by default). Only the
//! `run:` path differs: checkout / upload-artifact / cache are host-side
//! filesystem operations the `Executor` performs regardless of backend.
//!
//! The container client (bollard / Docker) is connected lazily, in
//! `ContainerBackend::start`, so a host-only runner (macOS, or Windows without
//! `container:` jobs) never needs a Docker daemon.

use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::time::Duration;

use anyhow::{Context, Result};
use bollard::Docker;
use bollard::container::{
    Config, CreateContainerOptions, KillContainerOptions, LogOutput, RemoveContainerOptions,
};
use bollard::exec::{CreateExecOptions, StartExecResults};
use bollard::image::CreateImageOptions;
use futures_util::StreamExt;
use tokio::io::AsyncReadExt;
use tokio::process::Command;

use crate::api::{ApiClient, StepSpec};

const STEP_TIMEOUT_DEFAULT_MINS: u64 = 60;

/// RunnerOS is the operating system this runner executes jobs on. It selects
/// the execution backend and the shell/paths each backend uses.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum RunnerOS {
    Linux,
    Windows,
    MacOS,
}

impl RunnerOS {
    /// parse maps the normalized os string (see config::Config::resolve_os) to
    /// the enum, defaulting to Linux for any unexpected value.
    pub fn parse(s: &str) -> Self {
        match s {
            "windows" => RunnerOS::Windows,
            "macos" => RunnerOS::MacOS,
            _ => RunnerOS::Linux,
        }
    }
}

/// Backend is a job's chosen execution environment for `run:` steps.
pub enum Backend {
    Container(ContainerBackend),
    Host(HostBackend),
}

impl Backend {
    /// run_script executes one `run:` step and returns Ok(true) on exit code 0.
    /// A returned Err of kind StepTimeout means the step exceeded its timeout
    /// and its process/container was killed — the caller should abort the job.
    pub async fn run_script(&self, api: &ApiClient, job_id: &str, step: &StepSpec) -> Result<bool> {
        match self {
            Backend::Container(c) => c.run_script(api, job_id, step).await,
            Backend::Host(h) => h.run_script(api, job_id, step).await,
        }
    }
}

// ----------------------------------------------------------------------------
// Container backend (bollard / Docker)
// ----------------------------------------------------------------------------

/// ContainerBackend runs steps inside a long-lived container with the workspace
/// bind-mounted. Connected lazily on `start`. Dropping it force-removes the
/// container, so a panic or early return never leaks it.
pub struct ContainerBackend {
    docker: Docker,
    container_id: String,
    os: RunnerOS,
}

impl ContainerBackend {
    /// start connects to Docker, pulls the image, and launches an idle
    /// container with the workspace bind-mounted and the base env applied.
    pub async fn start(
        api: &ApiClient,
        job_id: &str,
        image: &str,
        workspace_abs: &Path,
        base_env: &[(String, String)],
        os: RunnerOS,
    ) -> Result<Self> {
        let docker = Docker::connect_with_local_defaults().context("connect to docker")?;

        let _ = api
            .append_log(
                job_id,
                format!("[runner] preparing container image {image}\n").into_bytes(),
            )
            .await;
        let opts = CreateImageOptions::<String> {
            from_image: image.to_string(),
            ..Default::default()
        };
        let mut stream = docker.create_image(Some(opts), None, None);
        while let Some(item) = stream.next().await {
            item.context("pull image layer")?;
        }

        let mount = container_mount(os);
        let bind = format!("{}:{mount}", workspace_abs.display());
        let host_config = bollard::models::HostConfig {
            binds: Some(vec![bind]),
            ..Default::default()
        };
        let env: Vec<String> = base_env.iter().map(|(k, v)| format!("{k}={v}")).collect();
        let config = Config {
            image: Some(image.to_string()),
            cmd: Some(idle_cmd(os)),
            env: Some(env),
            working_dir: Some(mount.to_string()),
            host_config: Some(host_config),
            tty: Some(false),
            ..Default::default()
        };
        let created = docker
            .create_container(None::<CreateContainerOptions<String>>, config)
            .await?;
        docker
            .start_container(
                &created.id,
                None::<bollard::container::StartContainerOptions<String>>,
            )
            .await?;
        Ok(Self {
            docker,
            container_id: created.id,
            os,
        })
    }

    /// run_script execs the step's script inside the container, streaming
    /// combined stdout/stderr to the job log. Ok(true) on exit code 0.
    async fn run_script(&self, api: &ApiClient, job_id: &str, step: &StepSpec) -> Result<bool> {
        let env: Vec<String> = step.env.iter().map(|(k, v)| format!("{k}={v}")).collect();
        let exec = self
            .docker
            .create_exec(
                &self.container_id,
                CreateExecOptions::<String> {
                    cmd: Some(container_exec_argv(self.os, &step.run)),
                    env: Some(env),
                    working_dir: Some(container_mount(self.os).to_string()),
                    attach_stdout: Some(true),
                    attach_stderr: Some(true),
                    ..Default::default()
                },
            )
            .await?;

        let drained =
            tokio::time::timeout(step_timeout(step), self.drain(api, job_id, &exec.id)).await;
        match drained {
            Err(_) => {
                // Docker has no "kill exec" API and the process keeps running in
                // the container after the timeout future is dropped. Kill the
                // whole container to stop it now; StepTimeout tells the caller to
                // abort remaining steps (a dead container can't run them).
                let _ = api
                    .append_log(
                        job_id,
                        b"[runner] step timed out; killing container\n".to_vec(),
                    )
                    .await;
                let _ = self
                    .docker
                    .kill_container(&self.container_id, None::<KillContainerOptions<String>>)
                    .await;
                Err(StepTimeout.into())
            }
            Ok(Err(e)) => Err(e),
            Ok(Ok(())) => {
                let inspect = self.docker.inspect_exec(&exec.id).await?;
                Ok(inspect.exit_code.unwrap_or(0) == 0)
            }
        }
    }

    async fn drain(&self, api: &ApiClient, job_id: &str, exec_id: &str) -> Result<()> {
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
                    let _ = api.append_log(job_id, chunk).await;
                }
            }
            if !buf.is_empty() {
                let _ = api.append_log(job_id, buf).await;
            }
        }
        Ok(())
    }
}

impl Drop for ContainerBackend {
    fn drop(&mut self) {
        let docker = self.docker.clone();
        let id = self.container_id.clone();
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

// ----------------------------------------------------------------------------
// Host backend (run steps directly on the runner machine)
// ----------------------------------------------------------------------------

/// HostBackend runs steps directly on the runner host (no container). Used on
/// macOS always, and on Windows when the job sets no `container:`.
pub struct HostBackend {
    workspace: PathBuf,
    base_env: Vec<(String, String)>,
    os: RunnerOS,
}

impl HostBackend {
    pub fn new(workspace: PathBuf, base_env: Vec<(String, String)>, os: RunnerOS) -> Self {
        Self {
            workspace,
            base_env,
            os,
        }
    }

    /// run_script spawns the step's script in the host shell, streams its
    /// stdout+stderr to the job log, and kills the whole process tree on
    /// timeout. Ok(true) on exit code 0.
    async fn run_script(&self, api: &ApiClient, job_id: &str, step: &StepSpec) -> Result<bool> {
        // Windows prefers pwsh (PowerShell 7); fall back to the built-in
        // powershell if pwsh isn't installed. Other OSes have a single shell.
        let programs: &[&str] = match self.os {
            RunnerOS::Windows => &["pwsh", "powershell"],
            RunnerOS::MacOS => &["bash"],
            RunnerOS::Linux => &["sh"],
        };

        let mut child = None;
        let mut last_err = None;
        for prog in programs {
            match self.build_command(prog, step).spawn() {
                Ok(c) => {
                    child = Some(c);
                    break;
                }
                Err(e) => last_err = Some(e),
            }
        }
        let mut child = match child {
            Some(c) => c,
            None => {
                let e = last_err.expect("at least one shell attempted");
                return Err(anyhow::Error::new(e)
                    .context(format!("spawn host shell {programs:?} for step")));
            }
        };

        let pid = child.id();
        // Stream stdout and stderr concurrently so output interleaves roughly
        // in real time, like the container drain does.
        let mut tasks = Vec::new();
        if let Some(out) = child.stdout.take() {
            tasks.push(tokio::spawn(pump(out, api.clone(), job_id.to_string())));
        }
        if let Some(err) = child.stderr.take() {
            tasks.push(tokio::spawn(pump(err, api.clone(), job_id.to_string())));
        }

        let waited = tokio::time::timeout(step_timeout(step), child.wait()).await;
        match waited {
            Err(_) => {
                let _ = api
                    .append_log(
                        job_id,
                        b"[runner] step timed out; killing process tree\n".to_vec(),
                    )
                    .await;
                kill_tree(pid);
                let _ = child.wait().await;
                for t in tasks {
                    let _ = t.await;
                }
                Err(StepTimeout.into())
            }
            Ok(status) => {
                let status = status.context("wait for step process")?;
                for t in tasks {
                    let _ = t.await;
                }
                Ok(status.success())
            }
        }
    }

    /// build_command assembles a fresh tokio Command for one spawn attempt:
    /// shell + script, cwd = workspace, env = base (job env + secrets) then step
    /// env overrides. The runner's own environment is inherited (PATH, etc.).
    fn build_command(&self, program: &str, step: &StepSpec) -> Command {
        let mut cmd = Command::new(program);
        for arg in shell_args(self.os) {
            cmd.arg(arg);
        }
        cmd.arg(wrap_script(self.os, &step.run));
        cmd.current_dir(&self.workspace);
        for (k, v) in &self.base_env {
            cmd.env(k, v);
        }
        for (k, v) in &step.env {
            cmd.env(k, v);
        }
        cmd.stdin(Stdio::null());
        cmd.stdout(Stdio::piped());
        cmd.stderr(Stdio::piped());
        // Own process group so a timeout can kill the step's whole subtree
        // (the shell plus anything it spawned), not just the shell.
        #[cfg(unix)]
        cmd.process_group(0);
        cmd
    }
}

/// pump reads a child stream in chunks and appends them to the job log, mirroring
/// the container drain's 8 KiB flush cadence.
async fn pump<R>(mut reader: R, api: ApiClient, job_id: String)
where
    R: AsyncReadExt + Unpin,
{
    let mut buf = [0u8; 8192];
    let mut acc: Vec<u8> = Vec::with_capacity(8192);
    loop {
        match reader.read(&mut buf).await {
            Ok(0) => break,
            Ok(n) => {
                acc.extend_from_slice(&buf[..n]);
                if acc.len() >= 8192 {
                    let chunk = std::mem::take(&mut acc);
                    let _ = api.append_log(&job_id, chunk).await;
                }
            }
            Err(_) => break,
        }
    }
    if !acc.is_empty() {
        let _ = api.append_log(&job_id, acc).await;
    }
}

/// kill_tree kills the step's process subtree. On Unix the child leads its own
/// process group (set in build_command), so a negative pid signals the group.
/// On Windows taskkill /T walks the child tree.
fn kill_tree(pid: Option<u32>) {
    let Some(pid) = pid else { return };
    #[cfg(unix)]
    unsafe {
        libc::kill(-(pid as i32), libc::SIGKILL);
    }
    #[cfg(windows)]
    {
        let _ = std::process::Command::new("taskkill")
            .args(["/PID", &pid.to_string(), "/T", "/F"])
            .stdout(std::process::Stdio::null())
            .stderr(std::process::Stdio::null())
            .status();
    }
    #[cfg(not(any(unix, windows)))]
    {
        let _ = pid;
    }
}

// ----------------------------------------------------------------------------
// shell / path helpers
// ----------------------------------------------------------------------------

/// StepTimeout marks a step that exceeded its timeout. The backend kills the
/// step (container or process tree) and returns this so the Executor aborts the
/// remaining steps rather than run them against a dead environment.
#[derive(Debug)]
pub struct StepTimeout;

impl std::fmt::Display for StepTimeout {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "step timed out")
    }
}

impl std::error::Error for StepTimeout {}

fn step_timeout(step: &StepSpec) -> Duration {
    let mins = if step.timeout_minutes > 0 {
        step.timeout_minutes
    } else {
        STEP_TIMEOUT_DEFAULT_MINS
    };
    Duration::from_secs(60 * mins)
}

/// container_mount is where the workspace is bind-mounted (and the working dir).
fn container_mount(os: RunnerOS) -> &'static str {
    match os {
        RunnerOS::Windows => "C:\\workspace",
        _ => "/workspace",
    }
}

/// idle_cmd keeps the container alive while we exec steps into it.
fn idle_cmd(os: RunnerOS) -> Vec<String> {
    match os {
        // Windows base images (servercore) ship cmd + ping; nanoserver-only
        // images would need a custom keep-alive (documented in docs/pipelines.md).
        RunnerOS::Windows => vec![
            "cmd".into(),
            "/S".into(),
            "/C".into(),
            "ping -t 127.0.0.1 > NUL".into(),
        ],
        _ => vec!["sleep".into(), "infinity".into()],
    }
}

/// container_exec_argv is the argv that runs `script` inside the container.
fn container_exec_argv(os: RunnerOS, script: &str) -> Vec<String> {
    match os {
        RunnerOS::Windows => vec!["cmd".into(), "/S".into(), "/C".into(), script.to_string()],
        _ => vec!["sh".into(), "-ec".into(), script.to_string()],
    }
}

/// shell_args are the flags before the script argument for the host shell.
fn shell_args(os: RunnerOS) -> &'static [&'static str] {
    match os {
        RunnerOS::Windows => &["-NoProfile", "-NonInteractive", "-Command"],
        RunnerOS::MacOS => &["-e", "-c"],
        RunnerOS::Linux => &["-ec"],
    }
}

/// wrap_script adapts the user's script for the host shell. PowerShell needs an
/// explicit stop-on-error preamble to behave like `set -e`.
fn wrap_script(os: RunnerOS, script: &str) -> String {
    match os {
        RunnerOS::Windows => format!("$ErrorActionPreference = 'Stop';\n{script}"),
        _ => script.to_string(),
    }
}
