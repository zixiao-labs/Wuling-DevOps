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

pub(crate) const STEP_TIMEOUT_DEFAULT_MINS: u64 = 60;
pub(crate) const INTERNAL_INSTALL_TIMEOUT_MINS: u64 = 90;

/// Upper bound on a container image pull. `start` runs before any per-step
/// timeout, so without this a wedged registry would hang the job forever.
const IMAGE_PULL_TIMEOUT: Duration = Duration::from_secs(1800);

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

/// JobEnv is the environment a job accumulates as it runs.
#[derive(Clone, Debug, Default)]
pub struct JobEnv {
    base: Vec<(String, String)>,
    exported: Vec<(String, String)>,
    path_prefix: Vec<String>,
}

impl JobEnv {
    pub fn new(base: Vec<(String, String)>) -> Self {
        Self {
            base,
            exported: Vec::new(),
            path_prefix: Vec::new(),
        }
    }

    pub fn export(&mut self, key: impl Into<String>, value: impl Into<String>) {
        let key = key.into();
        let value = value.into();
        if let Some(entry) = self.exported.iter_mut().find(|(k, _)| k == &key) {
            entry.1 = value;
        } else {
            self.exported.push((key, value));
        }
    }

    pub fn get(&self, key: &str) -> Option<&str> {
        self.exported
            .iter()
            .rev()
            .find(|(k, _)| k == key)
            .map(|(_, v)| v.as_str())
            .or_else(|| {
                self.base
                    .iter()
                    .rev()
                    .find(|(k, _)| k == key)
                    .map(|(_, v)| v.as_str())
            })
    }

    pub fn prepend_path(&mut self, dir: impl Into<String>) {
        self.path_prefix.insert(0, dir.into());
    }

    pub fn pairs(&self) -> Vec<(String, String)> {
        let mut out = self.base.clone();
        for (k, v) in &self.exported {
            if let Some(entry) = out.iter_mut().find(|(ek, _)| ek == k) {
                entry.1 = v.clone();
            } else {
                out.push((k.clone(), v.clone()));
            }
        }
        out
    }

    pub fn path_preamble(&self, os: RunnerOS) -> String {
        if self.path_prefix.is_empty() {
            return String::new();
        }
        match os {
            RunnerOS::Windows => {
                let parts: Vec<String> = self
                    .path_prefix
                    .iter()
                    .map(|d| d.replace('\'', "''"))
                    .collect();
                format!(
                    "$env:PATH = '{}' + ';' + $env:PATH\n",
                    parts.join("' + ';' + '")
                )
            }
            _ => {
                let quoted: Vec<String> = self
                    .path_prefix
                    .iter()
                    .map(|d| format!("'{}'", d.replace('\'', "'\\''")))
                    .collect();
                format!("PATH={}:\"$PATH\"\nexport PATH\n", quoted.join(":"))
            }
        }
    }
}

/// Platform is the OS/arch/libc a step's process will actually see.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Platform {
    pub os: RunnerOS,
    pub arch: String,
    pub libc: String,
}

impl Platform {
    pub fn node_tag(&self) -> Result<String> {
        if self.libc == "musl" && self.os == RunnerOS::Linux {
            anyhow::bail!(
                "setup-node 不支持 musl 镜像（nodejs.org 无 musl 构建），请改用 glibc 镜像或镜像内自带 Node"
            );
        }
        let os_part = match self.os {
            RunnerOS::Linux => "linux",
            RunnerOS::MacOS => "darwin",
            RunnerOS::Windows => "win",
        };
        Ok(format!("{}-{}", os_part, self.arch))
    }

    pub fn rust_triple(&self) -> String {
        match (self.os, self.arch.as_str(), self.libc.as_str()) {
            (RunnerOS::Linux, "x64", "musl") => "x86_64-unknown-linux-musl".into(),
            (RunnerOS::Linux, "arm64", "musl") => "aarch64-unknown-linux-musl".into(),
            (RunnerOS::Linux, "x64", _) => "x86_64-unknown-linux-gnu".into(),
            (RunnerOS::Linux, "arm64", _) => "aarch64-unknown-linux-gnu".into(),
            (RunnerOS::MacOS, "arm64", _) => "aarch64-apple-darwin".into(),
            (RunnerOS::MacOS, "x64", _) => "x86_64-apple-darwin".into(),
            (RunnerOS::Windows, "arm64", _) => "aarch64-pc-windows-msvc".into(),
            (RunnerOS::Windows, _, _) => "x86_64-pc-windows-msvc".into(),
            _ => "x86_64-unknown-linux-gnu".into(),
        }
    }

    pub fn cache_slug(&self) -> String {
        format!("{}-{}", self.arch, self.libc)
    }
}

/// detect_libc probes the host filesystem for musl vs gnu.
pub fn detect_libc() -> String {
    if glob_exists("/lib/ld-musl-*") {
        "musl".into()
    } else {
        "gnu".into()
    }
}

fn glob_exists(pattern: &str) -> bool {
    glob::glob(pattern)
        .ok()
        .map(|paths| paths.flatten().next().is_some())
        .unwrap_or(false)
}

fn map_arch(raw: &str) -> String {
    match raw {
        "x86_64" | "x86" | "amd64" => "x64".into(),
        "aarch64" | "arm64" => "arm64".into(),
        other => other.into(),
    }
}

/// ResourceLimits caps one job container's CPU, memory and process count.
/// Values come from the pool's tier (injected as WULING_RUNNER_CPUS /
/// WULING_RUNNER_MEMORY by the autoscaler's user-data), so a runaway job can't
/// starve the runner process it shares the VM with. Zero fields mean "no
/// limit" — a hand-registered static runner keeps its Stage-1 behavior.
///
/// Host-backend jobs (macOS always, Windows without `container:`) are NOT
/// limited: there is no cgroup to attach to. The VM's own size is the bound.
#[derive(Clone, Copy, Debug, Default)]
pub struct ResourceLimits {
    pub cpus: f64,
    pub memory_bytes: i64,
    pub pids_limit: i64,
}

impl ResourceLimits {
    /// from_config builds limits from the parsed CLI/env config, ignoring
    /// values that don't parse rather than refusing to start.
    pub fn from_config(cpus: f64, memory: &str, pids_limit: i64) -> Self {
        Self {
            cpus: if cpus > 0.0 { cpus } else { 0.0 },
            memory_bytes: parse_memory(memory).unwrap_or(0),
            pids_limit: if pids_limit > 0 { pids_limit } else { 0 },
        }
    }

    /// apply writes the limits onto a Docker HostConfig.
    pub fn apply(&self, hc: &mut bollard::models::HostConfig, os: RunnerOS) {
        if self.memory_bytes > 0 {
            hc.memory = Some(self.memory_bytes);
            if os != RunnerOS::Windows {
                hc.memory_swap = Some(self.memory_bytes);
            }
        }
        if self.cpus > 0.0 {
            match os {
                RunnerOS::Windows => hc.cpu_count = Some(self.cpus.ceil() as i64),
                _ => hc.nano_cpus = Some((self.cpus * 1e9) as i64),
            }
        }
        if self.pids_limit > 0 && os != RunnerOS::Windows {
            hc.pids_limit = Some(self.pids_limit);
        }
    }
}

/// parse_memory turns "8Gi" / "512Mi" / "2g" / "1073741824" into bytes.
/// Binary suffixes (Ki/Mi/Gi) are 1024-based, decimal ones (k/m/g) 1000-based;
/// a bare number is bytes. None on anything unparseable.
pub fn parse_memory(s: &str) -> Option<i64> {
    let s = s.trim();
    if s.is_empty() {
        return None;
    }
    let lower = s.to_ascii_lowercase();
    let (num_str, mult) = if let Some(rest) = lower.strip_suffix("tib") {
        (rest, 1024i64.pow(4))
    } else if let Some(rest) = lower.strip_suffix("gib") {
        (rest, 1024i64.pow(3))
    } else if let Some(rest) = lower.strip_suffix("mib") {
        (rest, 1024i64.pow(2))
    } else if let Some(rest) = lower.strip_suffix("kib") {
        (rest, 1024)
    } else if let Some(rest) = lower.strip_suffix("ti") {
        (rest, 1024i64.pow(4))
    } else if let Some(rest) = lower.strip_suffix("gi") {
        (rest, 1024i64.pow(3))
    } else if let Some(rest) = lower.strip_suffix("mi") {
        (rest, 1024i64.pow(2))
    } else if let Some(rest) = lower.strip_suffix("ki") {
        (rest, 1024)
    } else if let Some(rest) = lower.strip_suffix("tb") {
        (rest, 1000i64.pow(4))
    } else if let Some(rest) = lower.strip_suffix("gb") {
        (rest, 1000i64.pow(3))
    } else if let Some(rest) = lower.strip_suffix("mb") {
        (rest, 1000i64.pow(2))
    } else if let Some(rest) = lower.strip_suffix("kb") {
        (rest, 1000)
    } else if let Some(rest) = lower.strip_suffix('t') {
        (rest, 1000i64.pow(4))
    } else if let Some(rest) = lower.strip_suffix('g') {
        (rest, 1000i64.pow(3))
    } else if let Some(rest) = lower.strip_suffix('m') {
        (rest, 1000i64.pow(2))
    } else if let Some(rest) = lower.strip_suffix('k') {
        (rest, 1000)
    } else {
        (lower.as_str(), 1)
    };
    let n: f64 = num_str.trim().parse().ok()?;
    if n < 0.0 {
        return None;
    }
    Some((n * mult as f64) as i64)
}

/// Backend is a job's chosen execution environment for `run:` steps.
pub enum Backend {
    Container(ContainerBackend),
    Host(HostBackend),
}

impl Backend {
    /// run_script executes one `run:` step and returns Ok(true) on exit code 0.
    pub async fn run_script(
        &self,
        api: &ApiClient,
        job_id: &str,
        step: &StepSpec,
        env: &JobEnv,
    ) -> Result<bool> {
        match self {
            Backend::Container(c) => c.run_script(api, job_id, step, env).await,
            Backend::Host(h) => h.run_script(api, job_id, step, env).await,
        }
    }

    /// run_internal executes a runner-synthesized script with streaming logs.
    pub async fn run_internal(
        &self,
        api: &ApiClient,
        job_id: &str,
        label: &str,
        script: &str,
        env: &JobEnv,
        timeout_minutes: u64,
    ) -> Result<bool> {
        match self {
            Backend::Container(c) => {
                c.run_internal(api, job_id, label, script, env, timeout_minutes)
                    .await
            }
            Backend::Host(h) => {
                h.run_internal(api, job_id, label, script, env, timeout_minutes)
                    .await
            }
        }
    }

    /// capture runs `script` and returns (success, trimmed stdout) without streaming.
    #[allow(dead_code)]
    pub async fn capture(&self, script: &str, env: &JobEnv) -> Result<(bool, String)> {
        match self {
            Backend::Container(c) => c.capture(script, env).await,
            Backend::Host(h) => h.capture(script, env).await,
        }
    }

    pub fn tool_mount(&self) -> &Path {
        match self {
            Backend::Container(c) => &c.tool_mount,
            Backend::Host(h) => &h.tools,
        }
    }

    pub fn state_mount(&self) -> &Path {
        match self {
            Backend::Container(c) => &c.state_mount,
            Backend::Host(h) => &h.state,
        }
    }

    pub async fn probe(&self) -> Result<Platform> {
        match self {
            Backend::Container(c) => c.probe().await,
            Backend::Host(h) => h.probe().await,
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
    tool_mount: PathBuf,
    state_mount: PathBuf,
}

impl ContainerBackend {
    /// start connects to Docker, pulls the image, and launches an idle
    /// container with the workspace bind-mounted and the base env applied.
    #[allow(clippy::too_many_arguments)]
    pub async fn start(
        api: &ApiClient,
        job_id: &str,
        image: &str,
        workspace_abs: &Path,
        tools_abs: &Path,
        state_abs: &Path,
        base_env: &[(String, String)],
        os: RunnerOS,
        limits: ResourceLimits,
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
        let pull = async {
            let mut stream = docker.create_image(Some(opts), None, None);
            while let Some(item) = stream.next().await {
                item.context("pull image layer")?;
            }
            Ok::<(), anyhow::Error>(())
        };
        tokio::time::timeout(IMAGE_PULL_TIMEOUT, pull)
            .await
            .context("timed out pulling container image")??;

        let mount = container_mount(os);
        let tool_m = container_tool_mount(os);
        let state_m = container_state_mount(os);
        let binds = vec![
            format!("{}:{mount}", bind_path(workspace_abs)),
            format!("{}:{tool_m}:ro", bind_path(tools_abs)),
            format!("{}:{state_m}", bind_path(state_abs)),
        ];
        let mut host_config = bollard::models::HostConfig {
            binds: Some(binds),
            ..Default::default()
        };
        limits.apply(&mut host_config, os);
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
            tool_mount: PathBuf::from(tool_m),
            state_mount: PathBuf::from(state_m),
        })
    }

    async fn run_script(
        &self,
        api: &ApiClient,
        job_id: &str,
        step: &StepSpec,
        env: &JobEnv,
    ) -> Result<bool> {
        self.exec(api, job_id, &step.run, env, step, step_timeout(step))
            .await
    }

    async fn run_internal(
        &self,
        api: &ApiClient,
        job_id: &str,
        label: &str,
        script: &str,
        env: &JobEnv,
        timeout_minutes: u64,
    ) -> Result<bool> {
        let _ = api
            .append_log(job_id, format!("[runner] {label}\n").into_bytes())
            .await;
        let mins = if timeout_minutes > 0 {
            timeout_minutes
        } else {
            INTERNAL_INSTALL_TIMEOUT_MINS
        };
        let fake_step = internal_step(label, mins);
        self.exec(
            api,
            job_id,
            script,
            env,
            &fake_step,
            Duration::from_secs(60 * mins),
        )
        .await
    }

    async fn capture(&self, script: &str, env: &JobEnv) -> Result<(bool, String)> {
        let preamble = env.path_preamble(self.os);
        let wrapped = wrap_script(self.os, &preamble, script);
        let empty = internal_step("capture", 5);
        let exec = self
            .docker
            .create_exec(
                &self.container_id,
                CreateExecOptions::<String> {
                    cmd: Some(container_exec_argv(self.os, "", &wrapped)),
                    env: Some(format_env(env, &empty)),
                    working_dir: Some(container_mount(self.os).to_string()),
                    attach_stdout: Some(true),
                    attach_stderr: Some(false),
                    ..Default::default()
                },
            )
            .await?;
        let start = self.docker.start_exec(&exec.id, None).await?;
        let mut out = String::new();
        if let StartExecResults::Attached { mut output, .. } = start {
            while let Some(item) = output.next().await {
                let msg = item?;
                if let LogOutput::StdOut { message } = msg {
                    out.push_str(&String::from_utf8_lossy(&message));
                }
            }
        }
        let inspect = self.docker.inspect_exec(&exec.id).await?;
        Ok((inspect.exit_code.unwrap_or(1) == 0, out.trim().to_string()))
    }

    async fn probe(&self) -> Result<Platform> {
        let script = "uname -m\nif [ -n \"$(ls /lib/ld-musl-* 2>/dev/null)\" ]; then echo musl; else echo gnu; fi\n";
        let (ok, out) = self.capture(script, &JobEnv::default()).await?;
        if !ok {
            anyhow::bail!("platform probe failed");
        }
        let mut lines = out.lines();
        let arch_raw = lines.next().unwrap_or("x86_64");
        let libc = lines.next().unwrap_or("gnu");
        Ok(Platform {
            os: self.os,
            arch: map_arch(arch_raw),
            libc: libc.to_string(),
        })
    }

    async fn exec(
        &self,
        api: &ApiClient,
        job_id: &str,
        script: &str,
        env: &JobEnv,
        step: &StepSpec,
        timeout: Duration,
    ) -> Result<bool> {
        let preamble = env.path_preamble(self.os);
        let env_pairs = format_env(env, step);
        let exec = self
            .docker
            .create_exec(
                &self.container_id,
                CreateExecOptions::<String> {
                    cmd: Some(container_exec_argv(self.os, &preamble, script)),
                    env: Some(env_pairs),
                    working_dir: Some(container_mount(self.os).to_string()),
                    attach_stdout: Some(true),
                    attach_stderr: Some(true),
                    ..Default::default()
                },
            )
            .await?;

        let drained = tokio::time::timeout(timeout, self.drain(api, job_id, &exec.id)).await;
        match drained {
            Err(_) => {
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
/// macOS always, and on Windows when the job sets no `container:`. Host jobs
/// are intentionally unlimited — there is no cgroup; the VM size is the bound.
pub struct HostBackend {
    workspace: PathBuf,
    tools: PathBuf,
    state: PathBuf,
    os: RunnerOS,
}

impl HostBackend {
    pub fn new(workspace: PathBuf, tools: PathBuf, state: PathBuf, os: RunnerOS) -> Self {
        Self {
            workspace,
            tools,
            state,
            os,
        }
    }

    async fn run_script(
        &self,
        api: &ApiClient,
        job_id: &str,
        step: &StepSpec,
        env: &JobEnv,
    ) -> Result<bool> {
        let preamble = env.path_preamble(self.os);
        self.spawn_and_wait(
            api,
            job_id,
            &wrap_script(self.os, &preamble, &step.run),
            env,
            step,
        )
        .await
    }

    async fn run_internal(
        &self,
        api: &ApiClient,
        job_id: &str,
        label: &str,
        script: &str,
        env: &JobEnv,
        timeout_minutes: u64,
    ) -> Result<bool> {
        let _ = api
            .append_log(job_id, format!("[runner] {label}\n").into_bytes())
            .await;
        let mins = if timeout_minutes > 0 {
            timeout_minutes
        } else {
            INTERNAL_INSTALL_TIMEOUT_MINS
        };
        let step = internal_step(label, mins);
        let preamble = env.path_preamble(self.os);
        self.spawn_and_wait(
            api,
            job_id,
            &wrap_script(self.os, &preamble, script),
            env,
            &step,
        )
        .await
    }

    #[allow(dead_code)]
    async fn capture(&self, script: &str, env: &JobEnv) -> Result<(bool, String)> {
        let preamble = env.path_preamble(self.os);
        let wrapped = wrap_script(self.os, &preamble, script);
        let step = internal_step("capture", 5);
        let programs: &[&str] = match self.os {
            RunnerOS::Windows => &["pwsh", "powershell"],
            RunnerOS::MacOS => &["bash"],
            RunnerOS::Linux => &["sh"],
        };
        for prog in programs {
            if let Ok(out) = self
                .build_command(prog, &wrapped, env, &step)
                .output()
                .await
            {
                let stdout = String::from_utf8_lossy(&out.stdout).trim().to_string();
                return Ok((out.status.success(), stdout));
            }
        }
        Ok((false, String::new()))
    }

    async fn probe(&self) -> Result<Platform> {
        match self.os {
            RunnerOS::Windows => {
                let arch =
                    std::env::var("PROCESSOR_ARCHITECTURE").unwrap_or_else(|_| "AMD64".into());
                let arch = match arch.as_str() {
                    "ARM64" => "arm64",
                    _ => "x64",
                };
                Ok(Platform {
                    os: RunnerOS::Windows,
                    arch: arch.into(),
                    libc: "msvc".into(),
                })
            }
            RunnerOS::MacOS => Ok(Platform {
                os: RunnerOS::MacOS,
                arch: crate::toolcache::runner_arch().to_string(),
                libc: "darwin".into(),
            }),
            RunnerOS::Linux => Ok(Platform {
                os: RunnerOS::Linux,
                arch: crate::toolcache::runner_arch().to_string(),
                libc: detect_libc(),
            }),
        }
    }

    async fn spawn_and_wait(
        &self,
        api: &ApiClient,
        job_id: &str,
        script: &str,
        env: &JobEnv,
        step: &StepSpec,
    ) -> Result<bool> {
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
            match self.build_command(prog, script, env, step).spawn() {
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

    fn build_command(&self, program: &str, script: &str, env: &JobEnv, step: &StepSpec) -> Command {
        let mut cmd = Command::new(program);
        for arg in shell_args(self.os) {
            cmd.arg(arg);
        }
        cmd.arg(script);
        cmd.current_dir(&self.workspace);
        for (k, v) in env.pairs() {
            cmd.env(k, v);
        }
        for (k, v) in &step.env {
            cmd.env(k, v);
        }
        cmd.stdin(Stdio::null());
        cmd.stdout(Stdio::piped());
        cmd.stderr(Stdio::piped());
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

fn container_tool_mount(os: RunnerOS) -> &'static str {
    match os {
        RunnerOS::Windows => "C:\\wuling\\tools",
        _ => "/opt/wuling/tools",
    }
}

fn container_state_mount(os: RunnerOS) -> &'static str {
    match os {
        RunnerOS::Windows => "C:\\wuling\\state",
        _ => "/opt/wuling/state",
    }
}

fn bind_path(p: &Path) -> String {
    let s = p.display().to_string();
    s.strip_prefix(r"\\?\").unwrap_or(&s).to_string()
}

fn format_env(env: &JobEnv, step: &StepSpec) -> Vec<String> {
    let mut pairs = env.pairs();
    for (k, v) in &step.env {
        if let Some(entry) = pairs.iter_mut().find(|(ek, _)| ek == k) {
            entry.1 = v.clone();
        } else {
            pairs.push((k.clone(), v.clone()));
        }
    }
    pairs.iter().map(|(k, v)| format!("{k}={v}")).collect()
}

fn internal_step(label: &str, timeout_minutes: u64) -> StepSpec {
    StepSpec {
        name: label.to_string(),
        run: String::new(),
        uses: String::new(),
        with: Default::default(),
        env: Default::default(),
        if_: String::new(),
        timeout_minutes,
    }
}

/// container_exec_argv is the argv that runs `script` inside the container.
fn container_exec_argv(os: RunnerOS, preamble: &str, script: &str) -> Vec<String> {
    match os {
        RunnerOS::Windows => {
            let mut argv = vec!["powershell".to_string()];
            argv.extend(shell_args(os).iter().map(|s| s.to_string()));
            argv.push(wrap_script(os, preamble, script));
            argv
        }
        _ => vec!["sh".into(), "-ec".into(), wrap_script(os, preamble, script)],
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

/// wrap_script adapts the user's script for the host shell.
fn wrap_script(os: RunnerOS, preamble: &str, script: &str) -> String {
    match os {
        RunnerOS::Windows => format!("$ErrorActionPreference = 'Stop';\n{preamble}{script}"),
        _ => format!("{preamble}{script}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_memory_table() {
        assert_eq!(parse_memory("8Gi"), Some(8 * 1024 * 1024 * 1024));
        assert_eq!(parse_memory("512Mi"), Some(512 * 1024 * 1024));
        assert_eq!(parse_memory("2g"), Some(2_000_000_000));
        assert_eq!(parse_memory("1024"), Some(1024));
        assert_eq!(parse_memory(""), None);
        assert_eq!(parse_memory("abc"), None);
    }

    #[test]
    fn apply_linux_sets_nano_cpus_and_memory_swap() {
        let limits = ResourceLimits {
            cpus: 2.0,
            memory_bytes: 1024,
            pids_limit: 100,
        };
        let mut hc = bollard::models::HostConfig::default();
        limits.apply(&mut hc, RunnerOS::Linux);
        assert_eq!(hc.nano_cpus, Some(2_000_000_000));
        assert_eq!(hc.memory, Some(1024));
        assert_eq!(hc.memory_swap, Some(1024));
        assert_eq!(hc.pids_limit, Some(100));
        assert!(hc.cpu_count.is_none());
    }

    #[test]
    fn apply_windows_sets_cpu_count_not_pids() {
        let limits = ResourceLimits {
            cpus: 1.5,
            memory_bytes: 2048,
            pids_limit: 100,
        };
        let mut hc = bollard::models::HostConfig::default();
        limits.apply(&mut hc, RunnerOS::Windows);
        assert_eq!(hc.cpu_count, Some(2));
        assert_eq!(hc.memory, Some(2048));
        assert!(hc.memory_swap.is_none());
        assert!(hc.nano_cpus.is_none());
        assert!(hc.pids_limit.is_none());
    }
}
