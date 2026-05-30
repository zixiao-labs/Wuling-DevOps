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
	b.WriteString("mkdir -p /etc/wuling-runner\n")
	b.WriteString("cat >/etc/wuling-runner/runner.env <<'WULING_EOF'\n")
	fmt.Fprintf(&b, "WULING_RUNNER_SERVER_URL=%s\n", serverURL)
	fmt.Fprintf(&b, "WULING_RUNNER_TOKEN=%s\n", token)
	fmt.Fprintf(&b, "WULING_RUNNER_NAME=%s\n", runnerName)
	fmt.Fprintf(&b, "WULING_RUNNER_LABELS=%s\n", labels)
	b.WriteString("WULING_RUNNER_CONCURRENCY=1\n")
	b.WriteString("WULING_EOF\n")
	b.WriteString("systemctl enable --now wuling-runner.service\n")
	return b.String()
}
