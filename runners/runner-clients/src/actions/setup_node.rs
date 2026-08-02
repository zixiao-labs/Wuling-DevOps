//! `actions/setup-node` and `pnpm/action-setup`.

use std::path::{Path, PathBuf};

use anyhow::{Context, Result, anyhow, bail};
use serde::Deserialize;

use crate::actions::ActionCtx;
use crate::backend::Platform;
use crate::toolcache::{ArchiveKind, Checksum, ToolCache};

/// NodeRelease is one entry of https://nodejs.org/dist/index.json.
#[derive(Deserialize)]
struct NodeRelease {
    version: String,
    #[serde(default)]
    lts: serde_json::Value,
}

#[derive(Deserialize)]
struct NpmPackage {
    #[serde(rename = "dist-tags")]
    dist_tags: std::collections::HashMap<String, String>,
    versions: std::collections::HashMap<String, NpmVersion>,
}

#[derive(Deserialize)]
struct NpmVersion {
    dist: NpmDist,
}

#[derive(Deserialize)]
struct NpmDist {
    tarball: String,
    integrity: String,
}

struct PmSpec {
    name: String,
    version: String,
}

pub async fn run(ctx: &mut ActionCtx<'_>) -> Result<bool> {
    let platform = ctx.platform.clone();
    let arch = ctx
        .with("architecture")
        .map(String::from)
        .unwrap_or_else(|| platform.arch.clone());

    let mut plat = platform.clone();
    plat.arch = arch;

    let version_spec = resolve_version_spec(ctx)?;
    let version = resolve_version(ctx.tools, &version_spec).await?;
    install_node(ctx, &plat, &version).await?;

    let want_pm = ctx.with("package-manager");
    let cache = ctx.with("cache").map(str::to_string);
    let registry_url = ctx.with("registry-url").map(str::to_string);
    let pm = resolve_pm(ctx, want_pm.or(cache.as_deref()))?;
    if let Some(pm) = pm {
        install_pm(ctx, &pm).await?;
    }

    if let Some(ref cache_kind) = cache {
        apply_cache_redirect(ctx, cache_kind)?;
    }

    if let Some(ref url) = registry_url {
        write_npmrc(ctx, url).await?;
    }

    Ok(true)
}

pub async fn run_pnpm_only(ctx: &mut ActionCtx<'_>) -> Result<bool> {
    if ctx.env.get("NODE_VERSION").is_none() {
        let version = resolve_version(ctx.tools, "lts/*").await?;
        install_node(ctx, ctx.platform, &version).await?;
    }
    let pm = resolve_pm(ctx, Some("pnpm"))?.ok_or_else(|| anyhow!("pnpm version required"))?;
    install_pm(ctx, &pm).await?;
    Ok(true)
}

fn resolve_version_spec(ctx: &ActionCtx<'_>) -> Result<String> {
    if let Some(v) = ctx.with("node-version") {
        return Ok(v.to_string());
    }
    if let Some(file) = ctx.with("node-version-file") {
        return read_version_file(ctx.workspace, file);
    }
    Ok("lts/*".to_string())
}

fn read_version_file(workspace: &Path, rel: &str) -> Result<String> {
    let path = workspace.join(rel);
    let text = std::fs::read_to_string(&path)
        .with_context(|| format!("read node-version-file {}", path.display()))?;
    if rel.ends_with("package.json") {
        let pkg: serde_json::Value = serde_json::from_str(&text)?;
        if let Some(v) = pkg
            .pointer("/volta/node")
            .and_then(|v| v.as_str())
            .filter(|s| !s.is_empty())
        {
            return Ok(v.to_string());
        }
        if let Some(v) = pkg
            .pointer("/engines/node")
            .and_then(|v| v.as_str())
            .filter(|s| {
                !s.contains('|') && !s.contains('^') && !s.contains('>') && !s.contains('<')
            })
        {
            return Ok(v
                .trim_start_matches('=')
                .trim_start_matches('v')
                .to_string());
        }
        bail!("package.json has no exact node version in volta.node or engines.node");
    }
    Ok(text
        .lines()
        .next()
        .unwrap_or("")
        .trim()
        .trim_start_matches('v')
        .to_string())
}

async fn resolve_version(tools: &ToolCache, spec: &str) -> Result<String> {
    let spec = spec.trim().trim_start_matches('=').trim_start_matches('v');
    let releases: Vec<NodeRelease> = tools
        .fetch_json("https://nodejs.org/dist/index.json")
        .await?;

    if spec.eq_ignore_ascii_case("latest")
        || spec.eq_ignore_ascii_case("node")
        || spec.eq_ignore_ascii_case("current")
    {
        return Ok(releases[0].version.trim_start_matches('v').to_string());
    }
    if spec == "lts/*" {
        for r in &releases {
            if r.lts.is_string() {
                return Ok(r.version.trim_start_matches('v').to_string());
            }
        }
        bail!("no LTS release found in node index");
    }
    if let Some(name) = spec.strip_prefix("lts/") {
        for r in &releases {
            if r.lts.as_str().map(|s| s.eq_ignore_ascii_case(name)) == Some(true) {
                return Ok(r.version.trim_start_matches('v').to_string());
            }
        }
        bail!("no LTS codename {name} found");
    }
    // exact / X / X.Y
    let parts: Vec<&str> = spec.split('.').collect();
    for r in &releases {
        let ver = r.version.trim_start_matches('v');
        if ver == spec {
            return Ok(ver.to_string());
        }
        if parts.len() == 1 && ver.starts_with(&format!("{}.", spec)) {
            return Ok(ver.to_string());
        }
        if parts.len() == 2 {
            let prefix = format!("{}.", spec);
            if ver.starts_with(&prefix) || ver == spec {
                return Ok(ver.to_string());
            }
        }
    }
    bail!("unsupported node-version {spec:?}; supported: exact, X, X.Y, lts/*, lts/<name>, latest");
}

fn dist_asset(version: &str, platform: &Platform) -> Result<(String, ArchiveKind)> {
    let tag = platform.node_tag()?;
    let v = format!("v{version}");
    if platform.os == crate::backend::RunnerOS::Windows {
        Ok((format!("node-{v}-{tag}.zip"), ArchiveKind::Zip))
    } else {
        Ok((format!("node-{v}-{tag}.tar.gz"), ArchiveKind::TarGz))
    }
}

async fn sha256_for(tools: &ToolCache, version: &str, file: &str) -> Result<Checksum> {
    let v = format!("v{version}");
    let sums = tools
        .fetch_text(&format!("https://nodejs.org/dist/{v}/SHASUMS256.txt"))
        .await?;
    for line in sums.lines() {
        let mut parts = line.split_whitespace();
        if let (Some(hex), Some(name)) = (parts.next(), parts.next())
            && (name.trim_start_matches("./") == file || name.ends_with(file))
        {
            return Ok(Checksum::Sha256Hex(hex.to_string()));
        }
    }
    bail!("SHASUMS256.txt has no entry for {file}");
}

fn bin_subdir(platform: &Platform) -> &'static str {
    if platform.os == crate::backend::RunnerOS::Windows {
        ""
    } else {
        "bin"
    }
}

async fn install_node(ctx: &mut ActionCtx<'_>, platform: &Platform, version: &str) -> Result<()> {
    let (file, kind) = dist_asset(version, platform)?;
    let sum = sha256_for(ctx.tools, version, &file).await?;
    let url = format!("https://nodejs.org/dist/v{version}/{file}");
    let (dir, fresh) = ctx
        .tools
        .ensure(
            "node",
            version,
            &platform.cache_slug(),
            &url,
            kind,
            Some(sum),
            true,
        )
        .await?;
    if fresh {
        ctx.log(&format!("[setup-node] installed Node {version}\n"))
            .await;
    }
    let bin = if bin_subdir(platform).is_empty() {
        dir.clone()
    } else {
        dir.join(bin_subdir(platform))
    };
    ctx.env.prepend_path(ctx.visible(&bin)?);
    ctx.env.export("NODE_VERSION", version);
    Ok(())
}

fn resolve_pm(ctx: &ActionCtx<'_>, want: Option<&str>) -> Result<Option<PmSpec>> {
    let want = want.unwrap_or("auto");
    if want == "auto" {
        if let Ok(text) = std::fs::read_to_string(ctx.workspace.join("package.json"))
            && let Ok(pkg) = serde_json::from_str::<serde_json::Value>(&text)
            && let Some(pm) = pkg.get("packageManager").and_then(|v| v.as_str())
        {
            return parse_pm_field(pm);
        }
        for (lock, name) in [
            ("pnpm-lock.yaml", "pnpm"),
            ("yarn.lock", "yarn"),
            ("package-lock.json", "npm"),
        ] {
            if ctx.workspace.join(lock).exists() {
                return Ok(Some(PmSpec {
                    name: name.into(),
                    version: String::new(),
                }));
            }
        }
        return Ok(None);
    }
    if let Some((name, ver)) = want.split_once('@') {
        return Ok(Some(PmSpec {
            name: name.to_string(),
            version: ver.to_string(),
        }));
    }
    Ok(Some(PmSpec {
        name: want.to_string(),
        version: String::new(),
    }))
}

fn parse_pm_field(field: &str) -> Result<Option<PmSpec>> {
    let base = field.split('+').next().unwrap_or(field);
    if let Some((name, ver)) = base.split_once('@') {
        Ok(Some(PmSpec {
            name: name.to_string(),
            version: ver.to_string(),
        }))
    } else {
        Ok(Some(PmSpec {
            name: base.to_string(),
            version: String::new(),
        }))
    }
}

async fn install_pm(ctx: &mut ActionCtx<'_>, pm: &PmSpec) -> Result<PathBuf> {
    let version = if pm.version.is_empty() {
        resolve_pm_version(ctx.tools, &pm.name).await?
    } else {
        pm.version.clone()
    };
    let meta: NpmPackage = ctx
        .tools
        .fetch_json(&format!(
            "https://registry.npmjs.org/{}/{}",
            pm.name, version
        ))
        .await?;
    let ver_meta = meta
        .versions
        .get(&version)
        .ok_or_else(|| anyhow!("npm registry missing version {}", version))?;
    let url = &ver_meta.dist.tarball;
    let sum = Checksum::Sri(ver_meta.dist.integrity.clone());
    let (dir, _) = ctx
        .tools
        .ensure(
            &pm.name,
            &version,
            "js",
            url,
            ArchiveKind::TarGz,
            Some(sum),
            true,
        )
        .await?;
    let pkg_dir = dir.clone();
    let bin_root = dir.parent().unwrap().join("bin");
    std::fs::create_dir_all(&bin_root)?;
    for (name, script) in bin_entries(&pkg_dir)? {
        write_pm_launcher(&bin_root, &name, &script, ctx.platform)?;
    }
    ctx.env.prepend_path(ctx.visible(&bin_root)?);
    Ok(bin_root)
}

async fn resolve_pm_version(tools: &ToolCache, name: &str) -> Result<String> {
    let meta: NpmPackage = tools
        .fetch_json(&format!("https://registry.npmjs.org/{name}"))
        .await?;
    meta.dist_tags
        .get("latest")
        .cloned()
        .ok_or_else(|| anyhow!("no latest tag for {name}"))
}

fn bin_entries(pkg_dir: &Path) -> Result<Vec<(String, String)>> {
    let pkg_json = pkg_dir.join("package.json");
    let text = std::fs::read_to_string(&pkg_json)?;
    let pkg: serde_json::Value = serde_json::from_str(&text)?;
    let bin = pkg.get("bin").cloned().unwrap_or(serde_json::Value::Null);
    let mut out = Vec::new();
    match bin {
        serde_json::Value::String(s) => {
            out.push((
                pkg["name"]
                    .as_str()
                    .unwrap_or("bin")
                    .rsplit('/')
                    .next()
                    .unwrap_or("bin")
                    .to_string(),
                s,
            ));
        }
        serde_json::Value::Object(map) => {
            for (k, v) in map {
                if let Some(s) = v.as_str() {
                    out.push((k, s.to_string()));
                }
            }
        }
        _ => {}
    }
    Ok(out)
}

fn write_pm_launcher(bin_dir: &Path, name: &str, script: &str, platform: &Platform) -> Result<()> {
    if platform.os == crate::backend::RunnerOS::Windows {
        let path = bin_dir.join(format!("{name}.cmd"));
        std::fs::write(
            &path,
            format!("@echo off\r\nnode \"%~dp0..\\js\\{script}\" %*\r\n"),
        )?;
    } else {
        let path = bin_dir.join(name);
        std::fs::write(
            &path,
            format!("#!/bin/sh\nexec node \"$(dirname -- \"$0\")/../js/{script}\" \"$@\"\n"),
        )?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o755))?;
        }
    }
    Ok(())
}

fn apply_cache_redirect(ctx: &mut ActionCtx<'_>, cache: &str) -> Result<()> {
    let state = ctx.state.to_path_buf();
    match cache {
        "npm" => {
            let dir = state.join("pm").join("npm");
            std::fs::create_dir_all(&dir)?;
            ctx.env.export("npm_config_cache", ctx.visible(&dir)?);
        }
        "pnpm" => {
            let store = state.join("pm").join("pnpm-store");
            let home = state.join("pm").join("pnpm-home");
            std::fs::create_dir_all(&store)?;
            std::fs::create_dir_all(&home)?;
            ctx.env.export("npm_config_store_dir", ctx.visible(&store)?);
            ctx.env.export("PNPM_HOME", ctx.visible(&home)?);
        }
        "yarn" => {
            let cache_dir = state.join("pm").join("yarn");
            let global = state.join("pm").join("yarn-berry");
            std::fs::create_dir_all(&cache_dir)?;
            std::fs::create_dir_all(&global)?;
            ctx.env
                .export("YARN_CACHE_FOLDER", ctx.visible(&cache_dir)?);
            ctx.env.export("YARN_ENABLE_GLOBAL_CACHE", "true");
            ctx.env.export("YARN_GLOBAL_FOLDER", ctx.visible(&global)?);
        }
        other => bail!("unsupported cache: {other} (npm|pnpm|yarn)"),
    }
    Ok(())
}

async fn write_npmrc(ctx: &mut ActionCtx<'_>, registry_url: &str) -> Result<()> {
    let dir = ctx.state.join("npmrc");
    std::fs::create_dir_all(&dir)?;
    let path = dir.join(format!("{}.npmrc", ctx.job_id));
    let mut lines = vec![format!("registry={registry_url}")];
    if let Some(scope) = ctx.with("scope") {
        lines.push(format!("{scope}:registry={registry_url}"));
    }
    if ctx.with_bool("always-auth", false) {
        lines.push(format!(
            "//{}:_authToken=${{NODE_AUTH_TOKEN}}",
            registry_url
                .trim_start_matches("https://")
                .trim_start_matches("http://")
        ));
        lines.push("always-auth=true".into());
    }
    std::fs::write(&path, lines.join("\n") + "\n")?;
    ctx.env.export("NPM_CONFIG_USERCONFIG", ctx.visible(&path)?);
    Ok(())
}
