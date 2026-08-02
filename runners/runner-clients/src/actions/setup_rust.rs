//! `actions/setup-rust` and rust-toolchain aliases.

use std::path::Path;

use anyhow::{Result, anyhow, bail};
use serde::Deserialize;

use crate::actions::ActionCtx;
use crate::toolcache::{ArchiveKind, Checksum};

#[derive(Deserialize, Default)]
struct RustToolchainFile {
    #[serde(default)]
    toolchain: RustToolchainSection,
}

#[derive(Deserialize, Default)]
struct RustToolchainSection {
    #[serde(default)]
    channel: String,
    #[serde(default)]
    profile: String,
    #[serde(default)]
    components: Vec<String>,
    #[serde(default)]
    targets: Vec<String>,
}

#[derive(Deserialize)]
struct RustupRelease {
    version: String,
}

pub async fn run(ctx: &mut ActionCtx<'_>) -> Result<bool> {
    let section = resolve_toolchain(ctx)?;
    let profile = ctx.with("profile").map(String::from).unwrap_or_else(|| {
        if section.profile.is_empty() {
            "minimal".into()
        } else {
            section.profile.clone()
        }
    });

    let components = {
        let from_with = ctx.with_list("components");
        if !from_with.is_empty() {
            from_with
        } else {
            section.components.clone()
        }
    };
    let targets = {
        let from_with = ctx.with_list("targets");
        if !from_with.is_empty() {
            from_with
        } else {
            section.targets.clone()
        }
    };

    let channel = section.channel.clone();
    if channel.is_empty() {
        bail!("no rust toolchain channel resolved");
    }

    let rustup_ver = rustup_version(ctx.tools).await?;
    let triple = ctx.platform.rust_triple();
    let init_name = if ctx.platform.os == crate::backend::RunnerOS::Windows {
        "rustup-init.exe"
    } else {
        "rustup-init"
    };
    let url =
        format!("https://static.rust-lang.org/rustup/archive/{rustup_ver}/{triple}/{init_name}");
    let sum = init_checksum(ctx.tools, &url).await?;
    let (init_dir, _) = ctx
        .tools
        .ensure(
            "rustup-init",
            &rustup_ver,
            &ctx.platform.cache_slug(),
            &url,
            ArchiveKind::Raw,
            Some(sum),
            false,
        )
        .await?;

    let rustup_home = ctx.state.join("rustup");
    let cargo_home = ctx.state.join("cargo");
    std::fs::create_dir_all(&rustup_home)?;
    std::fs::create_dir_all(&cargo_home)?;

    ctx.env.export("RUSTUP_HOME", ctx.visible(&rustup_home)?);
    ctx.env.export("CARGO_HOME", ctx.visible(&cargo_home)?);
    ctx.env.export("RUSTUP_TOOLCHAIN", &channel);
    ctx.env.export("CARGO_TERM_COLOR", "always");
    if let Some(flags) = ctx.with("rustflags").map(str::to_string) {
        ctx.env.export("RUSTFLAGS", flags);
    }
    ctx.env.prepend_path(ctx.visible(&cargo_home.join("bin"))?);

    let init_visible = ctx.visible(&init_dir.join(init_name))?;
    let cargo_bin = cargo_home.join("bin").join("rustup");
    let first_install = !tokio::fs::try_exists(&cargo_bin).await.unwrap_or(false);

    let mut cmd_parts = Vec::new();
    if first_install {
        cmd_parts.push(format!(
            "\"{init_visible}\" -y --no-modify-path --profile {profile}"
        ));
        if !channel.is_empty() {
            cmd_parts.push(format!("--default-toolchain {channel}"));
        }
    } else {
        cmd_parts.push(format!(
            "rustup toolchain install {channel} --profile {profile}"
        ));
    }
    for c in &components {
        cmd_parts.push(format!("-c {c}"));
    }
    for t in &targets {
        cmd_parts.push(format!("-t {t}"));
    }
    let script = cmd_parts.join(" \\\n  ");

    let lock_key = format!("rustup/{}", ctx.state.display());
    let _guard = ctx.tools.lock_key(&lock_key).await;

    let ok = ctx
        .backend
        .run_internal(
            ctx.api,
            ctx.job_id,
            "setup-rust",
            &script,
            ctx.env,
            ctx.timeout_minutes(),
        )
        .await?;
    if !ok {
        return Ok(false);
    }

    ctx.log(&format!("[setup-rust] toolchain {channel} ready\n"))
        .await;
    Ok(true)
}

fn resolve_toolchain(ctx: &ActionCtx<'_>) -> Result<RustToolchainSection> {
    if let Some(t) = ctx.with("toolchain") {
        if t == "file" {
            return read_toolchain_file(ctx.workspace)
                .and_then(|o| o.ok_or_else(|| anyhow!("rust-toolchain.toml not found")));
        }
        return Ok(RustToolchainSection {
            channel: normalize_channel(t),
            ..Default::default()
        });
    }
    if !ctx.uses_ref.is_empty()
        && (ctx.step.uses.starts_with("dtolnay/rust-toolchain@")
            || ctx
                .step
                .uses
                .starts_with("actions-rust-lang/setup-rust-toolchain@"))
    {
        return Ok(RustToolchainSection {
            channel: normalize_channel(ctx.uses_ref),
            ..Default::default()
        });
    }
    if let Some(file) = read_toolchain_file(ctx.workspace)? {
        return Ok(file);
    }
    Ok(RustToolchainSection {
        channel: "stable".into(),
        ..Default::default()
    })
}

fn normalize_channel(s: &str) -> String {
    s.trim().trim_start_matches("rust:").to_string()
}

fn read_toolchain_file(workspace: &Path) -> Result<Option<RustToolchainSection>> {
    let toml_path = workspace.join("rust-toolchain.toml");
    if toml_path.exists() {
        let text = std::fs::read_to_string(&toml_path)?;
        let file: RustToolchainFile = toml::from_str(&text)?;
        if file.toolchain.channel.is_empty() {
            bail!("rust-toolchain.toml missing channel");
        }
        return Ok(Some(file.toolchain));
    }
    let bare = workspace.join("rust-toolchain");
    if bare.exists() {
        let ch = std::fs::read_to_string(&bare)?.trim().to_string();
        if ch.is_empty() {
            bail!("rust-toolchain file is empty");
        }
        return Ok(Some(RustToolchainSection {
            channel: ch,
            ..Default::default()
        }));
    }
    Ok(None)
}

async fn rustup_version(tools: &crate::toolcache::ToolCache) -> Result<String> {
    let rel: RustupRelease = tools
        .fetch_json("https://static.rust-lang.org/rustup/release-stable.toml")
        .await?;
    Ok(rel.version.trim_matches('\'').to_string())
}

async fn init_checksum(tools: &crate::toolcache::ToolCache, url: &str) -> Result<Checksum> {
    let text = tools.fetch_text(&format!("{url}.sha256")).await?;
    let hex = text.split_whitespace().next().unwrap_or("").to_string();
    if hex.is_empty() {
        bail!("empty sha256 sidecar for {url}");
    }
    Ok(Checksum::Sha256Hex(hex))
}
