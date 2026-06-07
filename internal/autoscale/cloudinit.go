package autoscale

import (
	"fmt"
	"strings"
)

// BuildUserData renders the VM startup script that self-configures the runner.
//
// It assumes the pool's image/template ships the runner binary and a systemd
// unit `wuling-runner.service` whose EnvironmentFile is
// /etc/wuling-runner/runner.env (documented in runner-config.example.yaml).
// The script writes that env file with the injected token + server URL, then
// starts the unit. The wlrt_ token is passed directly so the VM authenticates
// without a register round-trip — the autoscaler already owns the runner row.
func BuildUserData(serverURL, token string, pool Pool, runnerName string) string {
	labels := strings.Join(pool.Labels, ",")
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -e\n")
	// The env file holds the runner's bearer token — keep it root-only. umask
	// guards the here-doc redirect; the explicit modes are belt-and-braces.
	b.WriteString("umask 077\n")
	b.WriteString("mkdir -p -m 0700 /etc/wuling-runner\n")
	b.WriteString("cat >/etc/wuling-runner/runner.env <<'WULING_EOF'\n")
	fmt.Fprintf(&b, "WULING_RUNNER_SERVER_URL=%s\n", serverURL)
	fmt.Fprintf(&b, "WULING_RUNNER_TOKEN=%s\n", token)
	fmt.Fprintf(&b, "WULING_RUNNER_NAME=%s\n", runnerName)
	fmt.Fprintf(&b, "WULING_RUNNER_LABELS=%s\n", labels)
	b.WriteString("WULING_RUNNER_CONCURRENCY=1\n")
	b.WriteString("WULING_EOF\n")
	b.WriteString("chmod 600 /etc/wuling-runner/runner.env\n")
	b.WriteString("systemctl enable --now wuling-runner.service\n")
	return b.String()
}

// BuildUserDataForPool renders the startup script appropriate to the pool's OS:
// Linux cloud-init/bash (BuildUserData) or Windows PowerShell
// (BuildWindowsUserData). An empty/unknown OS is treated as linux. macOS pools
// are rejected at config validation, so they never reach here.
func BuildUserDataForPool(serverURL, token string, pool Pool, runnerName string) string {
	if pool.OS == "windows" {
		return BuildWindowsUserData(serverURL, token, pool, runnerName)
	}
	return BuildUserData(serverURL, token, pool, runnerName)
}

// BuildWindowsUserData renders the Windows VM startup script. Like the Linux
// variant, it assumes the image ships the runner binary and a Scheduled Task
// `wuling-runner` (running as SYSTEM) whose wrapper loads
// C:\ProgramData\wuling-runner\runner.env (documented in docs/pipelines.md
// §7.1). The script writes that env file with the injected token + server URL,
// then (re)starts the task. A Scheduled Task — not a service — because the
// runner is a plain console binary, so this needs no third-party service shim.
// The whole script is wrapped in <powershell></powershell> so EC2Launch v2 /
// cloudbase-init execute it as PowerShell on first boot.
func BuildWindowsUserData(serverURL, token string, pool Pool, runnerName string) string {
	labels := strings.Join(pool.Labels, ",")
	var b strings.Builder
	b.WriteString("<powershell>\n")
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	b.WriteString("$dir = 'C:\\ProgramData\\wuling-runner'\n")
	b.WriteString("New-Item -ItemType Directory -Force -Path $dir | Out-Null\n")
	// runner.env uses the same KEY=VALUE lines the Linux systemd unit reads. A
	// single-quoted here-string is fully literal, so a token/URL containing `$`
	// or a backtick is never interpreted by PowerShell.
	b.WriteString("$runnerEnv = @'\n")
	fmt.Fprintf(&b, "WULING_RUNNER_SERVER_URL=%s\n", serverURL)
	fmt.Fprintf(&b, "WULING_RUNNER_TOKEN=%s\n", token)
	fmt.Fprintf(&b, "WULING_RUNNER_NAME=%s\n", runnerName)
	fmt.Fprintf(&b, "WULING_RUNNER_LABELS=%s\n", labels)
	b.WriteString("WULING_RUNNER_CONCURRENCY=1\n")
	b.WriteString("'@\n")
	b.WriteString("Set-Content -Path \"$dir\\runner.env\" -Value $runnerEnv -Encoding ascii\n")
	// The env file holds the runner's bearer token — restrict it to Administrators
	// and SYSTEM (the Linux side does the equivalent with chmod 600 + root-only).
	b.WriteString("icacls \"$dir\\runner.env\" /inheritance:r /grant:r \"*S-1-5-32-544:F\" \"*S-1-5-18:F\" | Out-Null\n")
	// (Re)start the image-provided Scheduled Task now that runner.env is written.
	b.WriteString("& schtasks /End /TN 'wuling-runner' 2>$null | Out-Null\n")
	b.WriteString("& schtasks /Run /TN 'wuling-runner'\n")
	b.WriteString("</powershell>\n")
	return b.String()
}
