//! Control-plane HTTP client and the DTOs exchanged with it. Field names
//! mirror the Go JSON on the runner protocol endpoints (see internal/runnerhttp).

use std::collections::HashMap;

use anyhow::{Context, Result, anyhow};
use serde::{Deserialize, Serialize};

/// A registered runner, returned by /runner/register. `token` is the raw
/// wlrt_ token, shown once.
#[derive(Debug, Deserialize)]
pub struct RegisteredRunner {
    pub id: String,
    pub name: String,
    pub token: String,
}

/// One executable step (mirrors pipeline.StepSpec).
#[derive(Debug, Clone, Deserialize)]
pub struct StepSpec {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub run: String,
    #[serde(default)]
    pub uses: String,
    #[serde(default)]
    pub with: HashMap<String, String>,
    #[serde(default)]
    pub env: HashMap<String, String>,
    #[serde(default, rename = "if")]
    pub if_: String,
    #[serde(default)]
    pub timeout_minutes: u64,
}

/// A job's execution spec (mirrors pipeline.JobSpec).
#[derive(Debug, Clone, Deserialize)]
pub struct JobSpec {
    #[serde(default)]
    pub container: String,
    #[serde(default)]
    pub env: HashMap<String, String>,
    #[serde(default)]
    pub steps: Vec<StepSpec>,
}

/// Where and what to check out.
#[derive(Debug, Clone, Deserialize)]
#[allow(dead_code)] // `ref` mirrors the wire format; not all fields are consumed.
pub struct Checkout {
    pub clone_url: String,
    #[serde(default)]
    pub r#ref: String,
    pub sha: String,
}

/// The full acquire response.
#[derive(Debug, Deserialize)]
#[allow(dead_code)] // run_id/commit_sha mirror the wire format for logging/debug.
pub struct AcquiredJob {
    pub job_id: String,
    pub run_id: String,
    pub run_number: i64,
    pub job_name: String,
    pub commit_sha: String,
    pub spec: JobSpec,
    #[serde(default)]
    pub secrets: HashMap<String, String>,
    pub checkout: Checkout,
}

#[derive(Serialize)]
struct RegisterReq<'a> {
    token: &'a str,
    name: &'a str,
    labels: &'a [String],
}

#[derive(Serialize)]
struct HeartbeatReq<'a> {
    status: &'a str,
}

#[derive(Serialize)]
struct PatchStepReq<'a> {
    status: &'a str,
}

#[derive(Serialize)]
struct CompleteReq<'a> {
    conclusion: &'a str,
}

/// HTTP client bound to a base URL and (after registration) a runner token.
#[derive(Clone)]
pub struct ApiClient {
    http: reqwest::Client,
    api_base: String,
    token: String,
}

impl ApiClient {
    pub fn new(api_base: String, token: String) -> Result<Self> {
        let http = reqwest::Client::builder()
            .build()
            .context("build http client")?;
        Ok(Self {
            http,
            api_base,
            token,
        })
    }

    /// Redeem a registration token for a persistent runner token. Static —
    /// no bearer needed (the body token authenticates).
    pub async fn register(
        api_base: &str,
        reg_token: &str,
        name: &str,
        labels: &[String],
    ) -> Result<RegisteredRunner> {
        let http = reqwest::Client::builder().build()?;
        let resp = http
            .post(format!("{api_base}/runner/register"))
            .json(&RegisterReq {
                token: reg_token,
                name,
                labels,
            })
            .send()
            .await
            .context("register request")?;
        if !resp.status().is_success() {
            return Err(anyhow!(
                "register failed: {} {}",
                resp.status(),
                resp.text().await.unwrap_or_default()
            ));
        }
        resp.json().await.context("decode register response")
    }

    pub async fn heartbeat(&self, status: &str) -> Result<()> {
        let resp = self
            .http
            .post(format!("{}/runner/heartbeat", self.api_base))
            .bearer_auth(&self.token)
            .json(&HeartbeatReq { status })
            .send()
            .await?;
        ensure_ok(resp, "heartbeat").await
    }

    /// Long-poll one job. Ok(None) means the queue had nothing (HTTP 204).
    pub async fn acquire(&self) -> Result<Option<AcquiredJob>> {
        let resp = self
            .http
            .post(format!("{}/runner/jobs/acquire", self.api_base))
            .bearer_auth(&self.token)
            .send()
            .await
            .context("acquire request")?;
        if resp.status() == reqwest::StatusCode::NO_CONTENT {
            return Ok(None);
        }
        if !resp.status().is_success() {
            return Err(anyhow!(
                "acquire failed: {} {}",
                resp.status(),
                resp.text().await.unwrap_or_default()
            ));
        }
        Ok(Some(resp.json().await.context("decode acquired job")?))
    }

    pub async fn append_log(&self, job_id: &str, data: Vec<u8>) -> Result<()> {
        let resp = self
            .http
            .post(format!("{}/runner/jobs/{job_id}/logs", self.api_base))
            .bearer_auth(&self.token)
            .header(reqwest::header::CONTENT_TYPE, "text/plain")
            .body(data)
            .send()
            .await?;
        ensure_ok(resp, "append_log").await
    }

    pub async fn patch_step(&self, job_id: &str, number: usize, status: &str) -> Result<()> {
        let resp = self
            .http
            .patch(format!(
                "{}/runner/jobs/{job_id}/steps/{number}",
                self.api_base
            ))
            .bearer_auth(&self.token)
            .json(&PatchStepReq { status })
            .send()
            .await?;
        ensure_ok(resp, "patch_step").await
    }

    pub async fn complete(&self, job_id: &str, conclusion: &str) -> Result<()> {
        let resp = self
            .http
            .post(format!("{}/runner/jobs/{job_id}/complete", self.api_base))
            .bearer_auth(&self.token)
            .json(&CompleteReq { conclusion })
            .send()
            .await?;
        ensure_ok(resp, "complete").await
    }

    pub async fn upload_artifact(&self, job_id: &str, name: &str, data: Vec<u8>) -> Result<()> {
        let resp = self
            .http
            .post(format!(
                "{}/runner/jobs/{job_id}/artifacts/{}",
                self.api_base,
                encode_path_segment(name)
            ))
            .bearer_auth(&self.token)
            .body(data)
            .send()
            .await?;
        ensure_ok(resp, "upload_artifact").await
    }
}

async fn ensure_ok(resp: reqwest::Response, what: &str) -> Result<()> {
    if resp.status().is_success() {
        return Ok(());
    }
    Err(anyhow!(
        "{what} failed: {} {}",
        resp.status(),
        resp.text().await.unwrap_or_default()
    ))
}

/// encode_path_segment percent-encodes everything outside the RFC 3986
/// unreserved set so an artifact name containing '/', '?', '%', … stays a
/// single path segment instead of breaking or escaping the route.
fn encode_path_segment(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for &b in s.as_bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char);
            }
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}
