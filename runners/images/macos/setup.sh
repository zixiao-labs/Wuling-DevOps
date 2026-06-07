#!/usr/bin/env bash
# Install the prebuilt wuling-runner binary + a launchd LaunchDaemon on a macOS
# host. macOS runners are MANUAL only — they are never autoscaled (Apple
# licensing requires Apple hardware), so this just sets up one physical machine.
# Downloads a release artifact; no Rust toolchain needed.
#
# Usage:
#   WULING_RUNNER_VERSION=v0.3.0 ./setup.sh
set -euo pipefail

REPO="${WULING_REPO:-zixiao-labs/Wuling-DevOps}"
VERSION="${WULING_RUNNER_VERSION:?set WULING_RUNNER_VERSION to a release tag, e.g. v0.3.0}"

case "$(uname -m)" in
  arm64) ARCH=arm64 ;;
  x86_64) ARCH=amd64 ;;
  *)
    echo "unsupported architecture $(uname -m)" >&2
    exit 1
    ;;
esac

url="https://github.com/${REPO}/releases/download/${VERSION}/wuling-runner-darwin-${ARCH}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $url"
curl -fsSL "$url" -o "$tmp/runner.tar.gz"
curl -fsSL "${url}.sha256" -o "$tmp/runner.tar.gz.sha256"
(cd "$tmp" && shasum -a 256 -c runner.tar.gz.sha256)
tar -xzf "$tmp/runner.tar.gz" -C "$tmp"
sudo install -m 0755 "$tmp/wuling-runner" /usr/local/bin/wuling-runner

# Write a LaunchDaemon template the operator fills in (server URL + token).
share=/usr/local/share/wuling-runner
sudo mkdir -p "$share"
sudo tee "$share/com.wuling.runner.plist" >/dev/null <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.wuling.runner</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/wuling-runner</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>WULING_RUNNER_SERVER_URL</key><string>https://wuling.example.com</string>
    <key>WULING_RUNNER_REGISTRATION_TOKEN</key><string>wlreg_REPLACE_ME</string>
    <key>WULING_RUNNER_OS</key><string>macos</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/wuling-runner.log</string>
  <key>StandardErrorPath</key><string>/var/log/wuling-runner.log</string>
</dict>
</plist>
PLIST

echo "Installed /usr/local/bin/wuling-runner."
echo "Template LaunchDaemon written to $share/com.wuling.runner.plist."
echo
echo "To register THIS machine:"
echo "  1. Mint a registration token in the org UI (Runners → Add)."
echo "  2. Edit the plist: set WULING_RUNNER_SERVER_URL and WULING_RUNNER_REGISTRATION_TOKEN."
echo "  3. sudo cp $share/com.wuling.runner.plist /Library/LaunchDaemons/"
echo "     sudo launchctl load -w /Library/LaunchDaemons/com.wuling.runner.plist"
