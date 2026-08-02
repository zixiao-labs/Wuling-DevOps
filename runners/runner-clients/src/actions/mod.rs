//! Built-in setup actions: Node, Rust, and shared dispatch helpers.

mod setup_node;
mod setup_rust;

use std::path::Path;

use anyhow::Result;

use crate::api::{ApiClient, StepSpec};
use crate::backend::{Backend, JobEnv, Platform, RunnerOS, STEP_TIMEOUT_DEFAULT_MINS};
use crate::toolcache::ToolCache;

pub use setup_node::{run as setup_node, run_pnpm_only};
pub use setup_rust::run as setup_rust;

/// ActionCtx is everything a built-in setup action may touch.
pub struct ActionCtx<'a> {
    pub api: &'a ApiClient,
    pub job_id: &'a str,
    pub step: &'a StepSpec,
    pub uses_ref: &'a str,
    pub backend: &'a Backend,
    pub workspace: &'a Path,
    pub tools: &'a ToolCache,
    pub state: &'a Path,
    pub env: &'a mut JobEnv,
    pub platform: &'a Platform,
}

impl ActionCtx<'_> {
    pub fn with(&self, key: &str) -> Option<&str> {
        self.step
            .with
            .get(key)
            .map(|s| s.trim())
            .filter(|s| !s.is_empty())
    }

    pub fn with_bool(&self, key: &str, default: bool) -> bool {
        match self.with(key) {
            Some("true") | Some("True") | Some("1") | Some("yes") => true,
            Some("false") | Some("False") | Some("0") | Some("no") => false,
            Some(_) => default,
            None => default,
        }
    }

    pub fn with_list(&self, key: &str) -> Vec<String> {
        self.with(key)
            .map(|s| {
                s.split([',', '\n'])
                    .map(str::trim)
                    .filter(|p| !p.is_empty())
                    .map(String::from)
                    .collect()
            })
            .unwrap_or_default()
    }

    pub async fn log(&self, msg: &str) {
        let _ = self
            .api
            .append_log(self.job_id, msg.as_bytes().to_vec())
            .await;
    }

    pub fn visible(&self, host_path: &Path) -> Result<String> {
        let sep = if self.platform.os == RunnerOS::Windows {
            '\\'
        } else {
            '/'
        };
        if let Ok(rel) = host_path.strip_prefix(self.tools.root()) {
            let mut out = self.backend.tool_mount().to_path_buf();
            for comp in rel.components() {
                out.push(comp.as_os_str());
            }
            return Ok(out.to_string_lossy().replace('/', &sep.to_string()));
        }
        if let Ok(rel) = host_path.strip_prefix(self.state) {
            let mut out = self.backend.state_mount().to_path_buf();
            for comp in rel.components() {
                out.push(comp.as_os_str());
            }
            return Ok(out.to_string_lossy().replace('/', &sep.to_string()));
        }
        anyhow::bail!("path {} is outside tools/state dirs", host_path.display());
    }

    pub fn timeout_minutes(&self) -> u64 {
        if self.step.timeout_minutes > 0 {
            self.step.timeout_minutes
        } else {
            STEP_TIMEOUT_DEFAULT_MINS
        }
    }
}

/// dispatch routes a `uses:` base name to a built-in setup action.
pub async fn dispatch(action: &str, ctx: &mut ActionCtx<'_>) -> Option<Result<bool>> {
    match action {
        "actions/setup-node" => Some(setup_node(ctx).await),
        "pnpm/action-setup" => Some(run_pnpm_only(ctx).await),
        "actions/setup-rust"
        | "dtolnay/rust-toolchain"
        | "actions-rust-lang/setup-rust-toolchain" => Some(setup_rust(ctx).await),
        _ => None,
    }
}
