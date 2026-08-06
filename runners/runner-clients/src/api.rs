//! Control-plane HTTP client and the DTOs exchanged with it. Field names
//! mirror the Go JSON on the runner protocol endpoints (see internal/runnerhttp).

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

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
    os: &'a str,
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
    /// Per-job stream redactors. A job's secrets are registered before any
    /// output is sent, so every execution path that calls append_log (including
    /// action helpers and container drains) gets the same protection.
    redactors: Arc<Mutex<HashMap<String, StreamSecretRedactor>>>,
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
            redactors: Arc::default(),
        })
    }

    /// Redeem a registration token for a persistent runner token. Static —
    /// no bearer needed (the body token authenticates).
    pub async fn register(
        api_base: &str,
        reg_token: &str,
        name: &str,
        os: &str,
        labels: &[String],
    ) -> Result<RegisteredRunner> {
        let http = reqwest::Client::builder().build()?;
        let resp = http
            .post(format!("{api_base}/runner/register"))
            .json(&RegisterReq {
                token: reg_token,
                name,
                os,
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

    /// Begin redacting the given job's secret values from streamed logs.
    ///
    /// Redaction is deliberately maintained by job id rather than by an
    /// executor-local wrapper: backend and action code also append logs
    /// directly through this client. Values shorter than four bytes are skipped
    /// because redacting common shell fragments creates misleading logs while
    /// offering little practical secret protection.
    pub fn start_log_redaction(&self, job_id: &str, secrets: impl IntoIterator<Item = String>) {
        let redactor = StreamSecretRedactor::new(
            secrets
                .into_iter()
                // Git checkout failures can include the runner's bearer token
                // in an authenticated URL. It is not a job Secret, but has
                // the same log-redaction requirement.
                .chain(std::iter::once(self.token.clone())),
        );
        if redactor.is_empty() {
            return;
        }
        if let Ok(mut redactors) = self.redactors.lock() {
            redactors.insert(job_id.to_string(), redactor);
        }
    }

    /// Flush a job's buffered log suffix and remove its secret redactor.
    ///
    /// A suffix is held between chunks so a secret split across HTTP log
    /// requests cannot leak. This must run before the final complete callback.
    pub async fn finish_log_redaction(&self, job_id: &str) -> Result<()> {
        let tail = {
            let mut redactors = self
                .redactors
                .lock()
                .map_err(|_| anyhow!("log redactor lock poisoned"))?;
            match redactors.remove(job_id) {
                Some(mut redactor) => redactor.redact_chunk(&[], true),
                None => Vec::new(),
            }
        };
        if tail.is_empty() {
            return Ok(());
        }
        self.post_log(job_id, tail).await
    }

    pub async fn append_log(&self, job_id: &str, data: Vec<u8>) -> Result<()> {
        let data = {
            let mut redactors = self
                .redactors
                .lock()
                .map_err(|_| anyhow!("log redactor lock poisoned"))?;
            match redactors.get_mut(job_id) {
                Some(redactor) => redactor.redact_chunk(&data, false),
                None => data,
            }
        };
        if data.is_empty() {
            return Ok(());
        }
        self.post_log(job_id, data).await
    }

    async fn post_log(&self, job_id: &str, data: Vec<u8>) -> Result<()> {
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

/// Redacts byte values while preserving enough suffix to recognize a secret
/// spanning two log chunks. We work on bytes instead of UTF-8 strings because
/// command output is not guaranteed to be valid Unicode.
#[derive(Debug)]
struct StreamSecretRedactor {
    secrets: Vec<Vec<u8>>,
    max_secret_len: usize,
    pending: Vec<u8>,
}

impl StreamSecretRedactor {
    fn new(values: impl IntoIterator<Item = String>) -> Self {
        let mut secrets: Vec<Vec<u8>> = values
            .into_iter()
            .map(String::into_bytes)
            .filter(|value| value.len() >= 4)
            .collect();
        secrets.sort();
        secrets.dedup();
        secrets.sort_by_key(|value| std::cmp::Reverse(value.len()));
        let max_secret_len = secrets.iter().map(Vec::len).max().unwrap_or(0);
        Self {
            secrets,
            max_secret_len,
            pending: Vec::new(),
        }
    }

    fn is_empty(&self) -> bool {
        self.secrets.is_empty()
    }

    fn redact_chunk(&mut self, chunk: &[u8], finish: bool) -> Vec<u8> {
        self.pending.extend_from_slice(chunk);
        if self.secrets.is_empty() {
            return std::mem::take(&mut self.pending);
        }

        // Keep the longest possible secret prefix until the next chunk unless
        // the stream is complete. Any start before safe_end has enough bytes
        // available to decide whether it is a secret.
        let safe_end = if finish {
            self.pending.len()
        } else {
            self.pending
                .len()
                .saturating_sub(self.max_secret_len.saturating_sub(1))
        };
        let mut out = Vec::with_capacity(safe_end);
        let mut idx = 0;
        while idx < safe_end {
            if let Some(secret) = self.secrets.iter().find(|secret| {
                idx + secret.len() <= self.pending.len()
                    && self.pending[idx..idx + secret.len()] == secret[..]
            }) {
                out.extend_from_slice(b"[REDACTED]");
                idx += secret.len();
            } else {
                out.push(self.pending[idx]);
                idx += 1;
            }
        }
        self.pending.drain(..idx);
        out
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

#[cfg(test)]
mod tests {
    use super::StreamSecretRedactor;

    #[test]
    fn redacts_a_secret_split_across_chunks() {
        let mut redactor = StreamSecretRedactor::new(["SUPERSECRET".to_string()]);
        let mut out = redactor.redact_chunk(b"prefix SUPER", false);
        out.extend(redactor.redact_chunk(b"SECRET suffix", false));
        out.extend(redactor.redact_chunk(&[], true));

        assert_eq!(String::from_utf8(out).unwrap(), "prefix [REDACTED] suffix");
    }

    #[test]
    fn keeps_short_values_out_of_the_redaction_set() {
        let mut redactor = StreamSecretRedactor::new(["abc".to_string()]);
        let out = redactor.redact_chunk(b"abc", true);
        assert_eq!(out, b"abc");
    }
}
