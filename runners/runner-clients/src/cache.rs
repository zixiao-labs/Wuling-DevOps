//! Remote pipeline-cache helpers: expression expansion, stable cache versions,
//! deterministic tar creation, checksum verification, and safe staged restore.

use std::collections::{BTreeMap, BTreeSet};
use std::fs::{self, File};
use std::io::{self, Cursor};
use std::path::{Component, Path, PathBuf};

use anyhow::{Context, Result, anyhow, bail};
use glob::{MatchOptions, Pattern};
use sha2::{Digest, Sha256};

use crate::backend::RunnerOS;

const ARCHIVE_FORMAT_VERSION: &str = "wuling-cache-tar-v1";
const MAX_CACHE_KEY_BYTES: usize = 512;
const MAX_RESTORE_KEYS: usize = 10;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CacheRequest {
    pub key: String,
    pub version: String,
    pub restore_keys: Vec<String>,
    pub paths: Vec<PathBuf>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CacheSave {
    pub key: String,
    pub version: String,
    pub paths: Vec<PathBuf>,
}

impl From<&CacheRequest> for CacheSave {
    fn from(request: &CacheRequest) -> Self {
        Self {
            key: request.key.clone(),
            version: request.version.clone(),
            paths: request.paths.clone(),
        }
    }
}

pub fn prepare_request(
    workspace: &Path,
    os: RunnerOS,
    key: &str,
    path: &str,
    restore_keys: Option<&str>,
) -> Result<CacheRequest> {
    let paths = parse_paths(path)?;
    let key = expand_key(key, workspace, os)?.trim().to_string();
    validate_cache_key("cache key", &key)?;

    let mut expanded_restore_keys = Vec::new();
    for restore_key in restore_keys.unwrap_or_default().lines() {
        let restore_key = restore_key.trim();
        if restore_key.is_empty() {
            continue;
        }
        let expanded = expand_key(restore_key, workspace, os)?.trim().to_string();
        if !expanded.is_empty() {
            validate_cache_key("cache restore key", &expanded)?;
            expanded_restore_keys.push(expanded);
        }
    }
    if expanded_restore_keys.len() > MAX_RESTORE_KEYS {
        bail!("cache restore-keys exceeds the maximum of {MAX_RESTORE_KEYS} entries");
    }

    Ok(CacheRequest {
        key,
        version: cache_version(os, &paths),
        restore_keys: expanded_restore_keys,
        paths,
    })
}

pub fn parse_paths(input: &str) -> Result<Vec<PathBuf>> {
    let mut paths = BTreeMap::<String, PathBuf>::new();
    for line in input.lines() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        let path = normalize_relative_path(line)?;
        paths.insert(normalized_path_text(&path), path);
    }
    if paths.is_empty() {
        bail!("cache path contains no non-empty relative paths");
    }
    Ok(paths.into_values().collect())
}

pub fn cache_version(os: RunnerOS, paths: &[PathBuf]) -> String {
    let mut normalized: Vec<String> = paths.iter().map(|p| normalized_path_text(p)).collect();
    normalized.sort();
    normalized.dedup();

    let mut hash = Sha256::new();
    hash.update(ARCHIVE_FORMAT_VERSION.as_bytes());
    hash.update([0]);
    hash.update(runner_os_name(os).as_bytes());
    for path in normalized {
        hash.update([0]);
        hash.update(path.as_bytes());
    }
    format!("{ARCHIVE_FORMAT_VERSION}-{}", hex::encode(hash.finalize()))
}

pub fn expand_key(input: &str, workspace: &Path, os: RunnerOS) -> Result<String> {
    let mut output = String::with_capacity(input.len());
    let mut rest = input;
    while let Some(start) = rest.find("${{") {
        output.push_str(&rest[..start]);
        let expression_and_tail = &rest[start + 3..];
        let Some(end) = expression_and_tail.find("}}") else {
            output.push_str(&rest[start..]);
            return Ok(output);
        };
        let expression = expression_and_tail[..end].trim();
        if expression == "runner.os" {
            output.push_str(runner_os_name(os));
        } else if let Some(pattern) = parse_hash_files_expression(expression) {
            output.push_str(&hash_files(workspace, pattern)?);
        } else {
            output.push_str(&rest[start..start + 3 + end + 2]);
        }
        rest = &expression_and_tail[end + 2..];
    }
    output.push_str(rest);
    Ok(output)
}

pub fn hash_files(workspace: &Path, pattern: &str) -> Result<String> {
    validate_glob_pattern(pattern)?;
    let pattern =
        Pattern::new(pattern).with_context(|| format!("invalid hashFiles glob {pattern:?}"))?;
    let fallback = pattern
        .as_str()
        .strip_prefix("**/")
        .map(Pattern::new)
        .transpose()
        .with_context(|| format!("invalid hashFiles glob {:?}", pattern.as_str()))?;
    let options = MatchOptions {
        case_sensitive: true,
        require_literal_separator: true,
        require_literal_leading_dot: false,
    };

    let mut files = Vec::new();
    collect_hash_files(
        workspace,
        Path::new(""),
        &pattern,
        fallback.as_ref(),
        options,
        &mut files,
    )?;
    files.sort_by_key(|path| normalized_path_text(path));

    if files.is_empty() {
        return Ok(String::new());
    }

    let mut hash = Sha256::new();
    for relative in files {
        let relative_text = normalized_path_text(&relative);
        let data = fs::read(workspace.join(&relative))
            .with_context(|| format!("read hashFiles match {relative_text}"))?;
        hash.update((relative_text.len() as u64).to_be_bytes());
        hash.update(relative_text.as_bytes());
        hash.update((data.len() as u64).to_be_bytes());
        hash.update(&data);
    }
    Ok(hex::encode(hash.finalize()))
}

pub fn sha256_hex(data: &[u8]) -> String {
    hex::encode(Sha256::digest(data))
}

pub fn verify_sha256(data: &[u8], expected: &str) -> Result<()> {
    let expected = expected.trim();
    if expected.len() != 64 || !expected.bytes().all(|b| b.is_ascii_hexdigit()) {
        bail!("cache response has invalid SHA-256 header {expected:?}");
    }
    let actual = sha256_hex(data);
    if !actual.eq_ignore_ascii_case(expected) {
        bail!("cache download SHA-256 mismatch: expected {expected}, got {actual}");
    }
    Ok(())
}

pub fn create_archive(workspace: &Path, paths: &[PathBuf]) -> Result<Vec<u8>> {
    let workspace = fs::canonicalize(workspace)
        .with_context(|| format!("canonicalize workspace {}", workspace.display()))?;
    let mut entries = BTreeMap::<PathBuf, ArchiveEntryKind>::new();
    let mut matched_root = false;

    for relative in paths {
        validate_normalized_path(relative)?;
        ensure_existing_path_has_no_symlink(&workspace, relative)?;
        let source = workspace.join(relative);
        let metadata = match fs::symlink_metadata(&source) {
            Ok(metadata) => metadata,
            Err(error) if error.kind() == io::ErrorKind::NotFound => continue,
            Err(error) => {
                return Err(error).with_context(|| format!("inspect {}", source.display()));
            }
        };
        matched_root = true;
        let canonical = fs::canonicalize(&source)
            .with_context(|| format!("canonicalize cache path {}", source.display()))?;
        if !canonical.starts_with(&workspace) {
            bail!(
                "cache path {} resolves outside the workspace",
                source.display()
            );
        }
        collect_archive_entries(&workspace, relative, &metadata, &mut entries)?;
    }

    if !matched_root {
        bail!("none of the configured cache paths exist");
    }

    let mut bytes = Vec::new();
    {
        let mut builder = tar::Builder::new(&mut bytes);
        for (relative, kind) in entries {
            let source = workspace.join(&relative);
            let metadata = fs::symlink_metadata(&source)
                .with_context(|| format!("inspect cache source {}", source.display()))?;
            if metadata.file_type().is_symlink() {
                bail!("cache source {} became a symlink", source.display());
            }

            let mut header = tar::Header::new_gnu();
            header.set_uid(0);
            header.set_gid(0);
            header.set_mtime(0);
            header.set_mode(file_mode(&metadata, kind));
            match kind {
                ArchiveEntryKind::Directory => {
                    header.set_entry_type(tar::EntryType::Directory);
                    header.set_size(0);
                    header.set_cksum();
                    builder
                        .append_data(&mut header, &relative, io::empty())
                        .with_context(|| format!("archive directory {}", source.display()))?;
                }
                ArchiveEntryKind::File => {
                    if !metadata.file_type().is_file() {
                        bail!("cache source {} is not a regular file", source.display());
                    }
                    header.set_entry_type(tar::EntryType::Regular);
                    header.set_size(metadata.len());
                    header.set_cksum();
                    let mut file = File::open(&source)
                        .with_context(|| format!("open cache source {}", source.display()))?;
                    builder
                        .append_data(&mut header, &relative, &mut file)
                        .with_context(|| format!("archive file {}", source.display()))?;
                }
            }
        }
        builder.finish().context("finish cache archive")?;
    }
    Ok(bytes)
}

pub fn restore_archive(
    data: &[u8],
    workspace: &Path,
    staging_directory: &Path,
    allowed_paths: &[PathBuf],
) -> Result<()> {
    let workspace = fs::canonicalize(workspace)
        .with_context(|| format!("canonicalize workspace {}", workspace.display()))?;
    match fs::symlink_metadata(staging_directory) {
        Ok(metadata) if metadata.file_type().is_symlink() => {
            bail!(
                "cache staging directory {} is a symlink",
                staging_directory.display()
            );
        }
        Ok(metadata) if metadata.is_dir() => {
            fs::remove_dir_all(staging_directory).with_context(|| {
                format!(
                    "remove previous cache staging directory {}",
                    staging_directory.display()
                )
            })?;
        }
        Ok(_) => {
            fs::remove_file(staging_directory).with_context(|| {
                format!(
                    "remove previous cache staging file {}",
                    staging_directory.display()
                )
            })?;
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => {}
        Err(error) => {
            return Err(error).with_context(|| {
                format!(
                    "inspect cache staging directory {}",
                    staging_directory.display()
                )
            });
        }
    }
    fs::create_dir_all(staging_directory).with_context(|| {
        format!(
            "create cache staging directory {}",
            staging_directory.display()
        )
    })?;

    let result = extract_to_staging(data, staging_directory, allowed_paths)
        .and_then(|modes| merge_staged_tree(staging_directory, &workspace, Path::new(""), &modes));
    let cleanup_result = fs::remove_dir_all(staging_directory);
    match (result, cleanup_result) {
        (Err(error), _) => Err(error),
        (Ok(()), Err(error)) => Err(error).with_context(|| {
            format!(
                "remove cache staging directory {}",
                staging_directory.display()
            )
        }),
        (Ok(()), Ok(())) => Ok(()),
    }
}

fn runner_os_name(os: RunnerOS) -> &'static str {
    match os {
        RunnerOS::Linux => "Linux",
        RunnerOS::Windows => "Windows",
        RunnerOS::MacOS => "macOS",
    }
}

fn validate_cache_key(name: &str, value: &str) -> Result<()> {
    if value.is_empty() {
        bail!("{name} is empty after expression expansion");
    }
    if value.len() > MAX_CACHE_KEY_BYTES {
        bail!("{name} exceeds the maximum of {MAX_CACHE_KEY_BYTES} bytes");
    }
    if !value.bytes().all(|byte| (0x20..=0x7e).contains(&byte)) {
        bail!("{name} must contain printable ASCII characters only");
    }
    Ok(())
}

fn parse_hash_files_expression(expression: &str) -> Option<&str> {
    let inner = expression
        .strip_prefix("hashFiles(")?
        .strip_suffix(')')?
        .trim();
    if inner.len() < 2 {
        return None;
    }
    let quote = inner.as_bytes()[0];
    if !matches!(quote, b'\'' | b'"') || inner.as_bytes()[inner.len() - 1] != quote {
        return None;
    }
    let value = &inner[1..inner.len() - 1];
    if value.contains(quote as char) || value.contains(',') {
        return None;
    }
    Some(value)
}

fn validate_glob_pattern(pattern: &str) -> Result<()> {
    if pattern.trim().is_empty() {
        bail!("hashFiles glob is empty");
    }
    let path = Path::new(pattern);
    if path.is_absolute() {
        bail!("hashFiles glob {pattern:?} must be workspace-relative");
    }
    for component in path.components() {
        match component {
            Component::ParentDir | Component::RootDir | Component::Prefix(_) => {
                bail!("hashFiles glob {pattern:?} escapes the workspace");
            }
            Component::CurDir | Component::Normal(_) => {}
        }
    }
    Ok(())
}

fn collect_hash_files(
    workspace: &Path,
    relative: &Path,
    pattern: &Pattern,
    fallback: Option<&Pattern>,
    options: MatchOptions,
    output: &mut Vec<PathBuf>,
) -> Result<()> {
    let directory = workspace.join(relative);
    let mut children: Vec<_> = fs::read_dir(&directory)
        .with_context(|| format!("read hashFiles directory {}", directory.display()))?
        .collect::<std::result::Result<_, _>>()?;
    children.sort_by_key(|entry| entry.file_name());

    for child in children {
        let child_relative = relative.join(child.file_name());
        let metadata = fs::symlink_metadata(child.path())?;
        if metadata.file_type().is_symlink() {
            let text = normalized_path_text(&child_relative);
            if pattern.matches_with(&text, options)
                || fallback.is_some_and(|p| p.matches_with(&text, options))
            {
                bail!("hashFiles match {text:?} is a symlink");
            }
            continue;
        }
        if metadata.is_dir() {
            // Git metadata can contain hundreds of thousands of objects and is
            // never a valid dependency lockfile source. Avoid recursively
            // scanning it for broad patterns such as **/*.lock.
            if child.file_name() == ".git" {
                continue;
            }
            collect_hash_files(
                workspace,
                &child_relative,
                pattern,
                fallback,
                options,
                output,
            )?;
        } else if metadata.is_file() {
            let text = normalized_path_text(&child_relative);
            if pattern.matches_with(&text, options)
                || fallback.is_some_and(|p| p.matches_with(&text, options))
            {
                output.push(child_relative);
            }
        }
    }
    Ok(())
}

fn normalize_relative_path(input: &str) -> Result<PathBuf> {
    let path = Path::new(input);
    if path.is_absolute() {
        bail!("cache path {input:?} must be relative to the workspace");
    }
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::CurDir => {}
            Component::Normal(part) => normalized.push(part),
            Component::ParentDir => {
                if !normalized.pop() {
                    bail!("cache path {input:?} escapes the workspace");
                }
            }
            Component::RootDir | Component::Prefix(_) => {
                bail!("cache path {input:?} must be relative to the workspace");
            }
        }
    }
    if normalized.as_os_str().is_empty() {
        bail!("cache path {input:?} resolves to the workspace root, which is not allowed");
    }
    Ok(normalized)
}

fn validate_normalized_path(path: &Path) -> Result<()> {
    for component in path.components() {
        if !matches!(component, Component::Normal(_)) {
            bail!(
                "cache path {:?} is not normalized and workspace-relative",
                path
            );
        }
    }
    Ok(())
}

fn normalized_path_text(path: &Path) -> String {
    let parts: Vec<_> = path
        .components()
        .filter_map(|component| match component {
            Component::Normal(part) => Some(part.to_string_lossy()),
            _ => None,
        })
        .collect();
    if parts.is_empty() {
        ".".to_string()
    } else {
        parts.join("/")
    }
}

fn ensure_existing_path_has_no_symlink(workspace: &Path, relative: &Path) -> Result<()> {
    let mut current = workspace.to_path_buf();
    for component in relative.components() {
        let Component::Normal(part) = component else {
            bail!("cache path {:?} is not normalized", relative);
        };
        current.push(part);
        match fs::symlink_metadata(&current) {
            Ok(metadata) if metadata.file_type().is_symlink() => {
                bail!("cache path {} contains a symlink", current.display());
            }
            Ok(_) => {}
            Err(error) if error.kind() == io::ErrorKind::NotFound => break,
            Err(error) => {
                return Err(error).with_context(|| format!("inspect {}", current.display()));
            }
        }
    }
    Ok(())
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum ArchiveEntryKind {
    Directory,
    File,
}

fn collect_archive_entries(
    workspace: &Path,
    relative: &Path,
    metadata: &fs::Metadata,
    entries: &mut BTreeMap<PathBuf, ArchiveEntryKind>,
) -> Result<()> {
    let source = workspace.join(relative);
    let file_type = metadata.file_type();
    if file_type.is_symlink() {
        bail!("cache source {} is a symlink", source.display());
    }
    if metadata.is_file() {
        entries.insert(relative.to_path_buf(), ArchiveEntryKind::File);
        return Ok(());
    }
    if !metadata.is_dir() {
        bail!(
            "cache source {} is not a regular file or directory",
            source.display()
        );
    }

    if !relative.as_os_str().is_empty() {
        entries.insert(relative.to_path_buf(), ArchiveEntryKind::Directory);
    }
    let mut children: Vec<_> = fs::read_dir(&source)
        .with_context(|| format!("read cache directory {}", source.display()))?
        .collect::<std::result::Result<_, _>>()?;
    children.sort_by_key(|entry| entry.file_name());
    for child in children {
        let child_relative = relative.join(child.file_name());
        let child_metadata = fs::symlink_metadata(child.path())?;
        collect_archive_entries(workspace, &child_relative, &child_metadata, entries)?;
    }
    Ok(())
}

fn file_mode(metadata: &fs::Metadata, _kind: ArchiveEntryKind) -> u32 {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        metadata.permissions().mode() & 0o777
    }
    #[cfg(not(unix))]
    {
        let _ = metadata;
        match _kind {
            ArchiveEntryKind::Directory => 0o755,
            ArchiveEntryKind::File => 0o644,
        }
    }
}

fn extract_to_staging(
    data: &[u8],
    staging: &Path,
    allowed_paths: &[PathBuf],
) -> Result<BTreeMap<PathBuf, u32>> {
    let mut archive = tar::Archive::new(Cursor::new(data));
    let mut seen = BTreeSet::new();
    let mut modes = BTreeMap::new();
    for entry in archive.entries().context("read cache archive")? {
        let mut entry = entry.context("read cache archive entry")?;
        let entry_type = entry.header().entry_type();
        if !entry_type.is_file() && !entry_type.is_dir() {
            bail!("unsafe cache archive entry type {:?}", entry_type.as_byte());
        }

        let archive_path = entry.path().context("read cache archive entry path")?;
        let relative = normalize_archive_path(&archive_path)?;
        if !allowed_paths
            .iter()
            .any(|allowed| relative.starts_with(allowed))
        {
            bail!(
                "cache archive path {:?} is outside the configured cache paths",
                relative
            );
        }
        if !seen.insert(relative.clone()) {
            bail!("cache archive contains duplicate path {:?}", relative);
        }
        let mode = entry.header().mode().context("read cache archive mode")? & 0o777;
        modes.insert(relative.clone(), mode);
        let destination = staging.join(&relative);
        if entry_type.is_dir() {
            fs::create_dir_all(&destination).with_context(|| {
                format!("create staged cache directory {}", destination.display())
            })?;
        } else {
            let parent = destination
                .parent()
                .ok_or_else(|| anyhow!("cache archive path {:?} has no parent", relative))?;
            fs::create_dir_all(parent)
                .with_context(|| format!("create staged cache directory {}", parent.display()))?;
            let mut output = File::create(&destination)
                .with_context(|| format!("create staged cache file {}", destination.display()))?;
            io::copy(&mut entry, &mut output)
                .with_context(|| format!("extract staged cache file {}", destination.display()))?;
        }
    }
    // Modes are applied only to final destinations after their contents have
    // been merged. A crafted mode 000 entry must not make staging impossible
    // to inspect or clean up.
    Ok(modes)
}

fn normalize_archive_path(path: &Path) -> Result<PathBuf> {
    if path.as_os_str().is_empty() || path.is_absolute() {
        bail!("unsafe cache archive path {:?}", path);
    }
    let mut normalized = PathBuf::new();
    for component in path.components() {
        match component {
            Component::Normal(part) => normalized.push(part),
            Component::CurDir => {}
            Component::ParentDir | Component::RootDir | Component::Prefix(_) => {
                bail!("unsafe cache archive path {:?}", path);
            }
        }
    }
    if normalized.as_os_str().is_empty() {
        bail!("unsafe empty cache archive path");
    }
    Ok(normalized)
}

fn merge_staged_tree(
    staging: &Path,
    workspace: &Path,
    relative: &Path,
    modes: &BTreeMap<PathBuf, u32>,
) -> Result<()> {
    let source_directory = staging.join(relative);
    let mut children: Vec<_> = fs::read_dir(&source_directory)
        .with_context(|| format!("read staged cache directory {}", source_directory.display()))?
        .collect::<std::result::Result<_, _>>()?;
    children.sort_by_key(|entry| entry.file_name());

    for child in children {
        let child_relative = relative.join(child.file_name());
        ensure_merge_destination_is_safe(workspace, &child_relative)?;
        let source = child.path();
        let destination = workspace.join(&child_relative);
        let metadata = fs::symlink_metadata(&source)?;
        if metadata.file_type().is_symlink() {
            bail!("staged cache path {} is a symlink", source.display());
        }
        if metadata.is_dir() {
            match fs::symlink_metadata(&destination) {
                Ok(existing) if !existing.is_dir() => {
                    bail!(
                        "cannot restore cache directory over non-directory {}",
                        destination.display()
                    );
                }
                Ok(_) => {}
                Err(error) if error.kind() == io::ErrorKind::NotFound => {
                    fs::create_dir(&destination).with_context(|| {
                        format!("create restored cache directory {}", destination.display())
                    })?;
                }
                Err(error) => {
                    return Err(error)
                        .with_context(|| format!("inspect {}", destination.display()));
                }
            }
            merge_staged_tree(staging, workspace, &child_relative, modes)?;
            apply_mode(
                &destination,
                modes
                    .get(&child_relative)
                    .copied()
                    .unwrap_or_else(|| file_mode(&metadata, ArchiveEntryKind::Directory)),
            )?;
        } else if metadata.is_file() {
            match fs::symlink_metadata(&destination) {
                Ok(existing) if !existing.is_file() => {
                    bail!(
                        "cannot restore cache file over non-file {}",
                        destination.display()
                    );
                }
                Ok(_) => {}
                Err(error) if error.kind() == io::ErrorKind::NotFound => {}
                Err(error) => {
                    return Err(error)
                        .with_context(|| format!("inspect {}", destination.display()));
                }
            }
            fs::copy(&source, &destination).with_context(|| {
                format!(
                    "merge staged cache file {} into {}",
                    source.display(),
                    destination.display()
                )
            })?;
            apply_mode(
                &destination,
                modes
                    .get(&child_relative)
                    .copied()
                    .unwrap_or_else(|| file_mode(&metadata, ArchiveEntryKind::File)),
            )?;
        } else {
            bail!(
                "staged cache path {} is not a regular file",
                source.display()
            );
        }
    }
    Ok(())
}

fn apply_mode(path: &Path, mode: u32) -> Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(mode))
            .with_context(|| format!("set cache path mode on {}", path.display()))?;
    }
    #[cfg(not(unix))]
    {
        let _ = (path, mode);
    }
    Ok(())
}

fn ensure_merge_destination_is_safe(workspace: &Path, relative: &Path) -> Result<()> {
    let mut current = workspace.to_path_buf();
    for component in relative.components() {
        let Component::Normal(part) = component else {
            bail!("unsafe merge path {:?}", relative);
        };
        current.push(part);
        match fs::symlink_metadata(&current) {
            Ok(metadata) if metadata.file_type().is_symlink() => {
                bail!(
                    "cache restore destination {} is a symlink",
                    current.display()
                );
            }
            Ok(_) => {}
            Err(error) if error.kind() == io::ErrorKind::NotFound => break,
            Err(error) => {
                return Err(error).with_context(|| format!("inspect {}", current.display()));
            }
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicU64, Ordering};

    use super::*;

    static NEXT_TEMP: AtomicU64 = AtomicU64::new(1);

    struct TestDir(PathBuf);

    impl TestDir {
        fn new(name: &str) -> Self {
            let sequence = NEXT_TEMP.fetch_add(1, Ordering::Relaxed);
            let path = std::env::temp_dir().join(format!(
                "wuling-runner-cache-{name}-{}-{sequence}",
                std::process::id()
            ));
            let _ = fs::remove_dir_all(&path);
            fs::create_dir_all(&path).unwrap();
            Self(path)
        }
    }

    impl Drop for TestDir {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn expands_runner_os_and_stable_hash_files() {
        let first = TestDir::new("hash-first");
        fs::write(first.0.join("b.lock"), b"bravo").unwrap();
        fs::write(first.0.join("a.lock"), b"alpha").unwrap();

        let second = TestDir::new("hash-second");
        fs::write(second.0.join("a.lock"), b"alpha").unwrap();
        fs::write(second.0.join("b.lock"), b"bravo").unwrap();

        let first_key = expand_key(
            "deps-${{ runner.os }}-${{ hashFiles('*.lock') }}",
            &first.0,
            RunnerOS::Linux,
        )
        .unwrap();
        let second_key = expand_key(
            "deps-${{ runner.os }}-${{ hashFiles(\"*.lock\") }}",
            &second.0,
            RunnerOS::Linux,
        )
        .unwrap();
        assert_eq!(first_key, second_key);
        assert!(first_key.starts_with("deps-Linux-"));
        assert_eq!(first_key.len(), "deps-Linux-".len() + 64);

        fs::write(second.0.join("a.lock"), b"changed").unwrap();
        let changed = hash_files(&second.0, "*.lock").unwrap();
        assert_ne!(first_key, format!("deps-Linux-{changed}"));

        fs::create_dir_all(first.0.join(".git/objects")).unwrap();
        fs::write(first.0.join(".git/objects/hidden.lock"), b"one").unwrap();
        let before_git_change = hash_files(&first.0, "**/*.lock").unwrap();
        fs::write(first.0.join(".git/objects/hidden.lock"), b"two").unwrap();
        assert_eq!(
            before_git_change,
            hash_files(&first.0, "**/*.lock").unwrap()
        );
    }

    #[test]
    fn parses_normalized_multiline_paths_and_versions() {
        let paths = parse_paths(" ./target\nfoo/../bar\ntarget\n").unwrap();
        assert_eq!(paths, vec![PathBuf::from("bar"), PathBuf::from("target")]);
        assert!(parse_paths("../outside").is_err());
        assert!(parse_paths("/absolute").is_err());
        assert!(parse_paths(".").is_err());

        let reordered = vec![PathBuf::from("target"), PathBuf::from("bar")];
        assert_eq!(
            cache_version(RunnerOS::Linux, &paths),
            cache_version(RunnerOS::Linux, &reordered)
        );
        assert_ne!(
            cache_version(RunnerOS::Linux, &paths),
            cache_version(RunnerOS::Windows, &paths)
        );
    }

    #[test]
    fn validates_cache_key_protocol_limits() {
        let workspace = TestDir::new("key-limits");
        assert!(
            prepare_request(&workspace.0, RunnerOS::Linux, "valid-key", "target", None).is_ok()
        );
        assert!(
            prepare_request(&workspace.0, RunnerOS::Linux, "bad\nkey", "target", None).is_err()
        );
        assert!(
            prepare_request(
                &workspace.0,
                RunnerOS::Linux,
                &"k".repeat(MAX_CACHE_KEY_BYTES + 1),
                "target",
                None,
            )
            .is_err()
        );
        let too_many = (0..=MAX_RESTORE_KEYS)
            .map(|index| format!("prefix-{index}"))
            .collect::<Vec<_>>()
            .join("\n");
        assert!(
            prepare_request(
                &workspace.0,
                RunnerOS::Linux,
                "key",
                "target",
                Some(&too_many),
            )
            .is_err()
        );
    }

    #[test]
    fn rejects_archive_traversal_and_absolute_paths() {
        for (name, path) in [("parent", "../escape"), ("absolute", "/tmp/escape")] {
            let workspace = TestDir::new(name);
            let staging = workspace.0.join("../staging");
            let archive = raw_archive(path, b'0', b"bad");
            assert!(restore_archive(&archive, &workspace.0, &staging, &[PathBuf::new()]).is_err());
            assert!(!workspace.0.join("escape").exists());
        }
    }

    #[test]
    fn rejects_links_devices_and_fifo_entries() {
        for entry_type in *b"12346" {
            let workspace = TestDir::new("dangerous-entry");
            let staging = workspace.0.join("../staging");
            let archive = raw_archive("danger", entry_type, b"");
            assert!(
                restore_archive(&archive, &workspace.0, &staging, &[PathBuf::from("danger")])
                    .is_err()
            );
            assert!(!workspace.0.join("danger").exists());
        }
    }

    #[test]
    fn rejects_entries_outside_configured_cache_paths() {
        let workspace = TestDir::new("outside-configured-paths");
        let staging = workspace.0.join("../scope-staging");
        let archive = raw_archive("other/file", b'0', b"bad");

        assert!(
            restore_archive(&archive, &workspace.0, &staging, &[PathBuf::from("cache")]).is_err()
        );
        assert!(!workspace.0.join("other").exists());
    }

    #[test]
    fn archive_roundtrip_restores_multiple_workspace_paths() {
        let source = TestDir::new("roundtrip-source");
        fs::create_dir_all(source.0.join("target/nested")).unwrap();
        fs::create_dir_all(source.0.join("empty")).unwrap();
        fs::write(source.0.join("target/nested/output.bin"), b"payload").unwrap();
        fs::write(source.0.join("Cargo.lock"), b"lock").unwrap();
        let paths = parse_paths("target\nCargo.lock\nempty").unwrap();
        let archive = create_archive(&source.0, &paths).unwrap();

        let destination = TestDir::new("roundtrip-destination");
        let staging = destination.0.join("../roundtrip-staging");
        restore_archive(&archive, &destination.0, &staging, &paths).unwrap();

        assert_eq!(
            fs::read(destination.0.join("target/nested/output.bin")).unwrap(),
            b"payload"
        );
        assert_eq!(fs::read(destination.0.join("Cargo.lock")).unwrap(), b"lock");
        assert!(destination.0.join("empty").is_dir());
        assert!(!staging.exists());
    }

    #[cfg(unix)]
    #[test]
    fn archive_roundtrip_preserves_executable_mode() {
        use std::os::unix::fs::PermissionsExt;

        let source = TestDir::new("mode-source");
        fs::write(source.0.join("tool"), b"#!/bin/sh\n").unwrap();
        fs::set_permissions(source.0.join("tool"), fs::Permissions::from_mode(0o751)).unwrap();
        let paths = vec![PathBuf::from("tool")];
        let archive = create_archive(&source.0, &paths).unwrap();

        let destination = TestDir::new("mode-destination");
        let staging = destination.0.join("../mode-staging");
        restore_archive(&archive, &destination.0, &staging, &paths).unwrap();

        let mode = fs::metadata(destination.0.join("tool"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777;
        assert_eq!(mode, 0o751);
    }

    #[cfg(unix)]
    #[test]
    fn archive_creation_rejects_symlink_escape() {
        use std::os::unix::fs::symlink;

        let workspace = TestDir::new("symlink-workspace");
        let outside = TestDir::new("symlink-outside");
        fs::write(outside.0.join("secret"), b"secret").unwrap();
        symlink(outside.0.join("secret"), workspace.0.join("linked")).unwrap();

        assert!(create_archive(&workspace.0, &[PathBuf::from("linked")]).is_err());
    }

    #[cfg(unix)]
    #[test]
    fn restore_refuses_existing_workspace_symlink() {
        use std::os::unix::fs::symlink;

        let source = TestDir::new("merge-source");
        fs::create_dir_all(source.0.join("cache")).unwrap();
        fs::write(source.0.join("cache/file"), b"cached").unwrap();
        let archive = create_archive(&source.0, &[PathBuf::from("cache")]).unwrap();

        let workspace = TestDir::new("merge-workspace");
        let outside = TestDir::new("merge-outside");
        symlink(&outside.0, workspace.0.join("cache")).unwrap();
        let staging = workspace.0.join("../merge-staging");
        assert!(
            restore_archive(&archive, &workspace.0, &staging, &[PathBuf::from("cache")]).is_err()
        );
        assert!(!outside.0.join("file").exists());
    }

    fn raw_archive(path: &str, entry_type: u8, contents: &[u8]) -> Vec<u8> {
        let mut output = Vec::new();
        {
            let mut builder = tar::Builder::new(&mut output);
            let mut header = tar::Header::new_gnu();
            header.set_mode(0o644);
            header.set_uid(0);
            header.set_gid(0);
            header.set_mtime(0);
            header.set_size(contents.len() as u64);
            header.set_entry_type(tar::EntryType::new(entry_type));
            let bytes = header.as_mut_bytes();
            bytes[..100].fill(0);
            bytes[..path.len()].copy_from_slice(path.as_bytes());
            header.set_cksum();
            builder.append(&header, contents).unwrap();
            builder.finish().unwrap();
        }
        output
    }
}
