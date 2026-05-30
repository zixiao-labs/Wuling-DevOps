//! Runner configuration, sourced from CLI flags and environment variables.

use clap::Parser;

/// Wuling DevOps CI runner client.
///
/// Registers with the control plane (or uses an existing runner token),
/// long-polls for jobs, and executes them in a container runtime.
#[derive(Parser, Debug, Clone)]
#[command(name = "wuling-runner", version, about)]
pub struct Config {
    /// Control-plane base URL, e.g. https://wuling.example.com
    #[arg(long, env = "WULING_RUNNER_SERVER_URL")]
    pub server_url: String,

    /// Persistent runner token (wlrt_…). If absent, --registration-token is
    /// redeemed on startup to obtain one.
    #[arg(long, env = "WULING_RUNNER_TOKEN")]
    pub token: Option<String>,

    /// One-time registration token (wlreg_…) used to self-register on first
    /// boot. The control plane / autoscaler injects this via user-data.
    #[arg(long, env = "WULING_RUNNER_REGISTRATION_TOKEN")]
    pub registration_token: Option<String>,

    /// Runner name. Generated server-side if empty.
    #[arg(long, env = "WULING_RUNNER_NAME", default_value = "")]
    pub name: String,

    /// Extra labels (comma-separated) advertised at registration.
    #[arg(
        long,
        env = "WULING_RUNNER_LABELS",
        value_delimiter = ',',
        default_value = ""
    )]
    pub labels: Vec<String>,

    /// Number of jobs to execute concurrently.
    #[arg(long, env = "WULING_RUNNER_CONCURRENCY", default_value_t = 1)]
    pub concurrency: usize,

    /// Working directory root for job checkouts and caches.
    #[arg(long, env = "WULING_RUNNER_WORK_DIR", default_value = "./_work")]
    pub work_dir: String,

    /// Default container image when a job sets no `container:`.
    #[arg(
        long,
        env = "WULING_RUNNER_DEFAULT_IMAGE",
        default_value = "debian:stable-slim"
    )]
    pub default_image: String,

    /// Seconds between acquire polls when the queue is empty.
    #[arg(long, env = "WULING_RUNNER_POLL_INTERVAL", default_value_t = 3)]
    pub poll_interval: u64,
}

impl Config {
    /// Trim empty labels that arise from an empty WULING_RUNNER_LABELS value.
    pub fn clean_labels(&self) -> Vec<String> {
        self.labels
            .iter()
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
            .collect()
    }

    pub fn api_base(&self) -> String {
        let base = self.server_url.trim_end_matches('/');
        format!("{base}/api/v1")
    }
}
