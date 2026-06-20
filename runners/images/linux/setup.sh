#!/usr/bin/env bash
# Install the prebuilt wuling-runner binary + systemd unit into a Linux runner
# image. Downloads a release artifact — NO Rust toolchain is installed, keeping
# the image small (the musl build is fully static, so no libc/openssl either).
#
# Usage (in your image build, as root):
#   WULING_RUNNER_VERSION=v0.3.0 ./setup.sh
set -euo pipefail

REPO="${WULING_REPO:-zixiao-labs/Wuling-DevOps}"
VERSION="${WULING_RUNNER_VERSION:?set WULING_RUNNER_VERSION to a release tag, e.g. v0.3.0}"

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *)
    echo "unsupported architecture $(uname -m)" >&2
    exit 1
    ;;
esac

url="https://github.com/${REPO}/releases/download/${VERSION}/wuling-runner-linux-${ARCH}.tar.gz"
artifact="$(basename "$url")"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $url"
# Keep the release filename: the .sha256 manifest lists the original artifact
# name, so `sha256sum -c` must find a file by that same name.
curl -fsSL "$url" -o "$tmp/$artifact"
curl -fsSL "${url}.sha256" -o "$tmp/$artifact.sha256"
(cd "$tmp" && sha256sum -c "$artifact.sha256")
tar -xzf "$tmp/$artifact" -C "$tmp"
install -m 0755 "$tmp/wuling-runner" /usr/local/bin/wuling-runner

# systemd unit. EnvironmentFile is written per instance by cloud-init/user-data
# (autoscaled) or by hand (static); see docs/pipelines.md §7.1.
cat >/etc/systemd/system/wuling-runner.service <<'UNIT'
[Unit]
Description=Wuling CI runner
After=network-online.target docker.service
Wants=network-online.target

[Service]
EnvironmentFile=/etc/wuling-runner/runner.env
ExecStart=/usr/local/bin/wuling-runner
Restart=on-failure

[Install]
WantedBy=multi-user.target
UNIT

echo "Installed /usr/local/bin/wuling-runner and wuling-runner.service."
echo
echo "Do NOT 'systemctl enable' it in the image — the autoscaler's user-data"
echo "writes /etc/wuling-runner/runner.env and enables it per instance."
echo
echo "For a MANUAL static runner, mint a registration token in the org UI, then:"
echo "  install -d -m 0700 /etc/wuling-runner"
echo "  cat >/etc/wuling-runner/runner.env <<EOF"
echo "  WULING_RUNNER_SERVER_URL=https://wuling.example.com"
echo "  WULING_RUNNER_REGISTRATION_TOKEN=wlreg_..."
echo "  EOF"
echo "  systemctl enable --now wuling-runner"
