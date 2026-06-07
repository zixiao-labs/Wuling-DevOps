//! wuling-runner: registers with the control plane (or uses an existing
//! token), then runs N concurrent workers that long-poll for jobs and execute
//! them in a container runtime. On SIGTERM/SIGINT it stops acquiring new work
//! and lets in-flight jobs finish (graceful drain).

mod api;
mod backend;
mod config;
mod executor;

use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::time::Duration;

use anyhow::{Result, anyhow};
use clap::Parser;
use tracing::{info, warn};

use crate::api::ApiClient;
use crate::backend::RunnerOS;
use crate::config::Config;
use crate::executor::Executor;

const HEARTBEAT_INTERVAL: Duration = Duration::from_secs(20);

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
        )
        .init();

    let cfg = Config::parse();
    let api_base = cfg.api_base();
    let os = cfg.resolve_os();
    // Advertise the OS as a plain label so `runs-on: [windows|macos|linux]`
    // routes here, matching the existing `linux` label convention. Deduped
    // against any user-supplied labels.
    let mut labels = cfg.clean_labels();
    if !labels.iter().any(|l| l.eq_ignore_ascii_case(&os)) {
        labels.push(os.clone());
    }

    // Obtain a runner token: use the supplied one, else redeem a registration
    // token (the autoscaler injects one via user-data on ephemeral VMs).
    let token = match cfg.token.clone() {
        Some(t) => t,
        None => {
            let reg = cfg
                .registration_token
                .clone()
                .ok_or_else(|| anyhow!("either --token or --registration-token is required"))?;
            let r = ApiClient::register(&api_base, &reg, &cfg.name, &os, &labels).await?;
            info!(runner_id = %r.id, name = %r.name, "registered with control plane");
            r.token
        }
    };

    let api = ApiClient::new(api_base, token.clone())?;
    let work_dir = PathBuf::from(&cfg.work_dir);
    tokio::fs::create_dir_all(&work_dir).await?;
    let executor = Executor::new(
        api.clone(),
        work_dir,
        cfg.default_image.clone(),
        token,
        RunnerOS::parse(&os),
    );

    let shutdown = Arc::new(AtomicBool::new(false));
    spawn_signal_handler(shutdown.clone());

    let busy = Arc::new(AtomicUsize::new(0));

    // Heartbeat keeps the runner visible and reports idle/busy for autoscaling.
    {
        let api = api.clone();
        let busy = busy.clone();
        let shutdown = shutdown.clone();
        tokio::spawn(async move {
            loop {
                let status = if busy.load(Ordering::Relaxed) > 0 {
                    "busy"
                } else {
                    "idle"
                };
                if let Err(e) = api.heartbeat(status).await {
                    warn!(error = %e, "heartbeat failed");
                }
                if shutdown.load(Ordering::Relaxed) {
                    return;
                }
                tokio::time::sleep(HEARTBEAT_INTERVAL).await;
            }
        });
    }

    let concurrency = cfg.concurrency.max(1);
    info!(
        concurrency,
        poll_interval = cfg.poll_interval,
        "runner ready"
    );
    let poll = Duration::from_secs(cfg.poll_interval.max(1));

    let mut handles = Vec::new();
    for _ in 0..concurrency {
        let ex = executor.clone();
        let api = api.clone();
        let busy = busy.clone();
        let shutdown = shutdown.clone();
        handles.push(tokio::spawn(worker_loop(ex, api, busy, shutdown, poll)));
    }
    for h in handles {
        let _ = h.await;
    }
    info!("all workers drained; bye");
    Ok(())
}

async fn worker_loop(
    ex: Executor,
    api: ApiClient,
    busy: Arc<AtomicUsize>,
    shutdown: Arc<AtomicBool>,
    poll: Duration,
) {
    while !shutdown.load(Ordering::Relaxed) {
        match api.acquire().await {
            Ok(Some(job)) => {
                busy.fetch_add(1, Ordering::Relaxed);
                ex.run_job(job).await;
                busy.fetch_sub(1, Ordering::Relaxed);
            }
            Ok(None) => {
                tokio::time::sleep(poll).await;
            }
            Err(e) => {
                warn!(error = %e, "acquire failed; backing off");
                tokio::time::sleep(poll).await;
            }
        }
    }
}

/// spawn_signal_handler flips the shutdown flag on SIGINT/SIGTERM.
fn spawn_signal_handler(shutdown: Arc<AtomicBool>) {
    tokio::spawn(async move {
        #[cfg(unix)]
        {
            use tokio::signal::unix::{SignalKind, signal};
            let mut term = signal(SignalKind::terminate()).expect("install SIGTERM handler");
            let mut int = signal(SignalKind::interrupt()).expect("install SIGINT handler");
            tokio::select! {
                _ = term.recv() => {}
                _ = int.recv() => {}
            }
        }
        #[cfg(not(unix))]
        {
            let _ = tokio::signal::ctrl_c().await;
        }
        warn!("shutdown signal received; draining in-flight jobs");
        shutdown.store(true, Ordering::Relaxed);
    });
}
