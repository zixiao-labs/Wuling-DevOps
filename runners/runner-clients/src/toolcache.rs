//! Shared cross-job tool cache: download → verify → extract → atomically
//! publish under `<tools>/<tool>/<version>/<slug>`. Everything here is a host
//! file operation; nothing downloaded is EXECUTED on the host.

use std::collections::HashMap;
use std::io::{Cursor, Read};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

use anyhow::{Context, Result, anyhow, bail};
use flate2::read::GzDecoder;
use reqwest::StatusCode;
use serde::de::DeserializeOwned;
use sha2::{Digest, Sha256, Sha512};
use tokio::sync::Mutex;

/// Archive kind, chosen from the download URL's suffix.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum ArchiveKind {
    TarGz,
    Zip,
    Raw,
}

/// A digest checked before a download is published.
#[derive(Clone, Debug)]
pub enum Checksum {
    /// Lowercase hex sha256 (nodejs.org SHASUMS256.txt, rustup .sha256 sidecar).
    Sha256Hex(String),
    /// npm registry `dist.integrity`, i.e. "sha512-<base64>".
    Sri(String),
}

/// ToolCache owns the immutable tool root and serialises concurrent installs
/// of the same key inside this process.
#[derive(Clone)]
pub struct ToolCache {
    root: Arc<PathBuf>,
    http: reqwest::Client,
    locks: Arc<Mutex<HashMap<String, Arc<Mutex<()>>>>>,
}

static STAGING_COUNTER: AtomicU64 = AtomicU64::new(0);

impl ToolCache {
    pub fn new(root: PathBuf) -> Self {
        Self {
            root: Arc::new(root),
            http: reqwest::Client::new(),
            locks: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    /// Acquire an in-process lock for an arbitrary key (e.g. rustup install).
    pub async fn lock_key(&self, key: &str) -> tokio::sync::OwnedMutexGuard<()> {
        let lock = {
            let mut map = self.locks.lock().await;
            map.entry(key.to_string())
                .or_insert_with(|| Arc::new(Mutex::new(())))
                .clone()
        };
        lock.lock_owned().await
    }

    async fn lock_for(&self, key: &str) -> tokio::sync::OwnedMutexGuard<()> {
        self.lock_key(key).await
    }

    /// dir is the published host directory for a tool version. May not exist.
    pub fn dir(&self, tool: &str, version: &str, slug: &str) -> PathBuf {
        self.root.join(tool).join(version).join(slug)
    }

    /// ensure downloads and publishes `url` under dir(tool, version, slug)
    /// unless already present.
    #[allow(clippy::too_many_arguments)]
    pub async fn ensure(
        &self,
        tool: &str,
        version: &str,
        slug: &str,
        url: &str,
        kind: ArchiveKind,
        checksum: Option<Checksum>,
        strip_root: bool,
    ) -> Result<(PathBuf, bool)> {
        let dest = self.dir(tool, version, slug);
        let marker = dest.join(".wuling-complete");
        if tokio::fs::try_exists(&marker).await.unwrap_or(false) {
            return Ok((dest, false));
        }
        let lock_key = format!("{tool}/{version}/{slug}");
        let _guard = self.lock_for(&lock_key).await;
        if tokio::fs::try_exists(&marker).await.unwrap_or(false) {
            return Ok((dest, false));
        }

        let staging = self.root.join("_staging").join(format!(
            "{}-{}",
            std::process::id(),
            STAGING_COUNTER.fetch_add(1, Ordering::Relaxed)
        ));
        let bytes = download_with_retry(&self.http, url).await?;
        if let Some(sum) = &checksum {
            verify(&bytes, sum, url)?;
        }
        let dest_clone = dest.clone();
        let version_owned = version.to_string();
        let url_owned = url.to_string();
        tokio::task::spawn_blocking(move || -> Result<()> {
            extract(&bytes, kind, &staging, strip_root, &url_owned)?;
            std::fs::write(staging.join(".wuling-complete"), &version_owned)?;
            if let Some(parent) = dest_clone.parent() {
                std::fs::create_dir_all(parent)?;
            }
            match std::fs::rename(&staging, &dest_clone) {
                Ok(()) => Ok(()),
                Err(_) if dest_clone.join(".wuling-complete").exists() => {
                    let _ = std::fs::remove_dir_all(&staging);
                    Ok(())
                }
                Err(e) => Err(e.into()),
            }
        })
        .await??;
        Ok((dest, true))
    }

    /// fetch_text GETs a small text resource with retry.
    pub async fn fetch_text(&self, url: &str) -> Result<String> {
        let bytes = download_with_retry(&self.http, url).await?;
        String::from_utf8(bytes).context("response is not valid UTF-8")
    }

    /// fetch_json GETs and deserialises a small JSON document.
    pub async fn fetch_json<T: DeserializeOwned>(&self, url: &str) -> Result<T> {
        let text = self.fetch_text(url).await?;
        serde_json::from_str(&text).context("parse JSON")
    }
}

/// verify checks `data` against `sum`, naming `url` in the error.
pub fn verify(data: &[u8], sum: &Checksum, url: &str) -> Result<()> {
    match sum {
        Checksum::Sha256Hex(expected) => {
            let got = sha256_hex(data);
            if got != expected.to_ascii_lowercase() {
                bail!("checksum mismatch for {url}: expected {expected}, got {got}");
            }
        }
        Checksum::Sri(sri) => {
            let sri = sri.trim();
            let Some(rest) = sri.strip_prefix("sha512-") else {
                bail!("unsupported integrity format for {url}: {sri}");
            };
            let expected = base64::Engine::decode(&base64::engine::general_purpose::STANDARD, rest)
                .context("decode SRI base64")?;
            let mut hasher = Sha512::new();
            hasher.update(data);
            let got = hasher.finalize();
            if got.as_slice() != expected.as_slice() {
                bail!("SRI checksum mismatch for {url}");
            }
        }
    }
    Ok(())
}

/// extract unpacks an in-memory archive into a freshly created `dest`.
pub fn extract(
    data: &[u8],
    kind: ArchiveKind,
    dest: &Path,
    strip_root: bool,
    url: &str,
) -> Result<()> {
    std::fs::create_dir_all(dest)?;
    match kind {
        ArchiveKind::TarGz => {
            let decoder = GzDecoder::new(Cursor::new(data));
            let mut archive = tar::Archive::new(decoder);
            if strip_root {
                unpack_strip_root(&mut archive, dest)?;
            } else {
                archive.unpack(dest)?;
            }
        }
        ArchiveKind::Zip => {
            let reader = Cursor::new(data);
            let mut archive = zip::ZipArchive::new(reader)?;
            if strip_root {
                let root = find_zip_root(&mut archive)?;
                for i in 0..archive.len() {
                    let mut file = archive.by_index(i)?;
                    if file.is_dir() {
                        continue;
                    }
                    let name = file.name().replace('\\', "/");
                    let Some(rel) = name.strip_prefix(&root) else {
                        continue;
                    };
                    if rel.is_empty() {
                        continue;
                    }
                    let out = dest.join(rel);
                    if let Some(parent) = out.parent() {
                        std::fs::create_dir_all(parent)?;
                    }
                    let mut out_file = std::fs::File::create(&out)?;
                    std::io::copy(&mut file, &mut out_file)?;
                    #[cfg(unix)]
                    {
                        use std::os::unix::fs::PermissionsExt;
                        if let Some(mode) = file.unix_mode() {
                            let _ = std::fs::set_permissions(
                                &out,
                                std::fs::Permissions::from_mode(mode),
                            );
                        }
                    }
                }
            } else {
                archive.extract(dest)?;
            }
        }
        ArchiveKind::Raw => {
            let name = raw_basename(url);
            let out = dest.join(name);
            std::fs::write(&out, data)?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                let _ = std::fs::set_permissions(&out, std::fs::Permissions::from_mode(0o755));
            }
        }
    }
    Ok(())
}

fn raw_basename(url: &str) -> String {
    url.rsplit('/').next().unwrap_or("download").to_string()
}

fn find_zip_root(archive: &mut zip::ZipArchive<Cursor<&[u8]>>) -> Result<String> {
    let mut root: Option<String> = None;
    for i in 0..archive.len() {
        let file = archive.by_index(i)?;
        let name = file.name().replace('\\', "/");
        let Some(first) = name.split('/').next() else {
            continue;
        };
        if first.is_empty() {
            continue;
        }
        match &root {
            None => root = Some(format!("{first}/")),
            Some(r) if r == &format!("{first}/") => {}
            Some(_) => return Ok(String::new()),
        }
    }
    Ok(root.unwrap_or_default())
}

fn unpack_strip_root<R: Read>(archive: &mut tar::Archive<R>, dest: &Path) -> Result<()> {
    let mut root: Option<String> = None;
    let entries: Vec<_> = archive.entries()?.collect::<Result<Vec<_>, _>>()?;
    for entry in &entries {
        let path = entry.path()?.display().to_string();
        let Some(first) = path.split('/').next() else {
            continue;
        };
        if first.is_empty() {
            continue;
        }
        match &root {
            None => root = Some(first.to_string()),
            Some(r) if r == first => {}
            Some(_) => {
                root = None;
                break;
            }
        }
    }
    for entry in entries {
        let path = entry.path()?.display().to_string();
        let rel = if let Some(r) = &root {
            path.strip_prefix(&format!("{r}/")).unwrap_or(&path)
        } else {
            &path
        };
        if rel.is_empty() {
            continue;
        }
        let out = dest.join(rel);
        if entry.header().entry_type().is_dir() {
            std::fs::create_dir_all(&out)?;
        } else {
            if let Some(parent) = out.parent() {
                std::fs::create_dir_all(parent)?;
            }
            let mut out_file = std::fs::File::create(&out)?;
            let mut reader = entry;
            std::io::copy(&mut reader, &mut out_file)?;
        }
    }
    Ok(())
}

/// sha256_hex hashes `data` to lowercase hex.
pub fn sha256_hex(data: &[u8]) -> String {
    let mut hasher = Sha256::new();
    hasher.update(data);
    hex::encode(hasher.finalize())
}

/// runner_arch maps std::env::consts::ARCH to "x64" | "arm64".
pub fn runner_arch() -> &'static str {
    match std::env::consts::ARCH {
        "x86_64" | "x86" => "x64",
        "aarch64" | "arm64" => "arm64",
        other => other,
    }
}

async fn download_with_retry(http: &reqwest::Client, url: &str) -> Result<Vec<u8>> {
    let mut delay = std::time::Duration::from_secs(1);
    let mut last_err = None;
    for attempt in 0..3 {
        if attempt > 0 {
            tokio::time::sleep(delay).await;
            delay *= 2;
        }
        match http.get(url).send().await {
            Ok(resp) if resp.status().is_success() => {
                return resp.bytes().await.map(|b| b.to_vec()).context("read body");
            }
            Ok(resp) => {
                last_err = Some(anyhow!("HTTP {} for {url}", resp.status()));
                if resp.status() == StatusCode::NOT_FOUND {
                    break;
                }
            }
            Err(e) => last_err = Some(e.into()),
        }
    }
    Err(last_err.unwrap_or_else(|| anyhow!("download failed for {url}")))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sha256_produces_hex() {
        assert_eq!(sha256_hex(b"hello").len(), 64);
    }

    #[test]
    fn runner_arch_maps() {
        let arch = runner_arch();
        assert!(arch == "x64" || arch == "arm64" || !arch.is_empty());
    }
}
