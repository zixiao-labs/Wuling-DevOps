package autoscale

import (
	"strings"
	"testing"
	"time"

	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
)

const sampleConfig = `
version: 1
default_tier: medium
idle_timeout: 5m
tiers:
  low: {cpu: 2, memory: 4Gi, storage: 40Gi}
  medium: {cpu: 4, memory: 8Gi, storage: 80Gi}
pools:
  - name: aws-medium
    provider: aws
    tier: medium
    labels: [linux, docker]
    min: 0
    max: 5
    aws:
      region: us-west-2
      ami: ami-x
      instance_type: c6i.large
      credentials_secret: AWS_CREDS
  - name: aliyun-low
    provider: aliyun
    tier: low
    labels: [linux]
    min: 1
    max: 3
    aliyun:
      region: cn-hangzhou
      image_id: m-x
      instance_type: ecs.g7.large
      credentials_secret: ALIYUN_CREDS
`

func TestParseConfig(t *testing.T) {
	c, err := Parse([]byte(sampleConfig))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.DefaultTier != "medium" {
		t.Errorf("default_tier = %q", c.DefaultTier)
	}
	if c.IdleTimeoutOr(time.Minute) != 5*time.Minute {
		t.Errorf("idle_timeout = %v", c.IdleTimeout.Std())
	}
	if len(c.Pools) != 2 {
		t.Fatalf("pools = %d", len(c.Pools))
	}
	if c.Pools[0].AWS == nil || c.Pools[0].AWS.Region != "us-west-2" {
		t.Errorf("aws pool not parsed: %+v", c.Pools[0].AWS)
	}
	if c.TierSpecFor("medium").CPU != 4 {
		t.Errorf("medium tier cpu = %d", c.TierSpecFor("medium").CPU)
	}
}

func TestIdleTimeoutFallback(t *testing.T) {
	c, err := Parse([]byte("version: 1\npools: []\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := c.IdleTimeoutOr(5 * time.Minute); got != 5*time.Minute {
		t.Errorf("fallback idle = %v, want 5m", got)
	}
}

func TestConfigValidationErrors(t *testing.T) {
	bad := []string{
		// unknown provider
		"pools:\n  - {name: p, provider: gcp, tier: low}\n",
		// two provider blocks
		"tiers: {low: {cpu: 1}}\npools:\n  - name: p\n    provider: aws\n    tier: low\n    aws: {region: r}\n    aliyun: {region: r}\n",
		// tier not defined
		"pools:\n  - name: p\n    provider: aws\n    tier: ghost\n    aws: {region: r}\n",
		// min > max
		"tiers: {low: {cpu: 1}}\npools:\n  - name: p\n    provider: aws\n    tier: low\n    min: 5\n    max: 1\n    aws: {region: r}\n",
		// duplicate pool name
		"tiers: {low: {cpu: 1}}\npools:\n  - {name: p, provider: aws, tier: low, aws: {region: r}}\n  - {name: p, provider: aws, tier: low, aws: {region: r}}\n",
	}
	for i, src := range bad {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

func TestAssignDemandFirstMatch(t *testing.T) {
	pools := []Pool{
		{Name: "a", Tier: "medium", Labels: []string{"linux", "docker"}},
		{Name: "b", Tier: "medium", Labels: []string{"linux"}},
		{Name: "c", Tier: "low", Labels: []string{"linux"}},
	}
	demand := []pipelinestore.QueuedJob{
		{Tier: "medium", RunsOn: []string{"linux", "docker"}}, // -> a
		{Tier: "medium", RunsOn: []string{"linux"}},           // -> a too (a offers linux+docker ⊇ linux)
		{Tier: "low", RunsOn: []string{"linux"}},              // -> c
		{Tier: "high", RunsOn: nil},                           // -> unmatched
	}
	got := assignDemand(pools, demand)
	// First-match with overlapping pools: pool "a" absorbs both medium jobs,
	// "b" gets none, "c" gets the low job.
	if got["a"] != 2 || got["b"] != 0 || got["c"] != 1 {
		t.Errorf("assignment = %+v", got)
	}
}

func TestLabelsSatisfied(t *testing.T) {
	if !labelsSatisfied([]string{"linux", "docker"}, []string{"linux"}) {
		t.Error("superset should satisfy")
	}
	if labelsSatisfied([]string{"linux"}, []string{"linux", "docker"}) {
		t.Error("missing docker should not satisfy")
	}
	if !labelsSatisfied([]string{"linux"}, nil) {
		t.Error("empty requirement should always satisfy")
	}
}

func TestOSValidation(t *testing.T) {
	pool := func(os string) string {
		return "tiers: {low: {cpu: 1}}\npools:\n  - name: p\n    provider: aws\n    tier: low\n    os: " + os + "\n    aws: {region: r, credentials_secret: C}\n"
	}
	// An empty os defaults to linux; linux + windows are valid autoscaled OSes.
	for _, ok := range []string{"", "linux", "windows"} {
		if _, err := Parse([]byte(pool(ok))); err != nil {
			t.Errorf("os %q: unexpected validation error: %v", ok, err)
		}
	}
	// macos is manual-only; solaris is unknown — both must be rejected.
	for _, bad := range []string{"macos", "solaris"} {
		if _, err := Parse([]byte(pool(bad))); err == nil {
			t.Errorf("os %q: expected a validation error, got nil", bad)
		}
	}
}

func TestWindowsUserData(t *testing.T) {
	pool := Pool{Name: "win", OS: "windows", Provider: "aws", Labels: []string{"windows", "msvc"}}
	ud := BuildWindowsUserData("https://wuling.example.com", "wlrt_deadbeef_secret", pool, "win-01")
	for _, want := range []string{
		"<powershell>",
		"</powershell>",
		"WULING_RUNNER_SERVER_URL=https://wuling.example.com",
		"WULING_RUNNER_TOKEN=wlrt_deadbeef_secret",
		"WULING_RUNNER_LABELS=windows,msvc",
		"WULING_RUNNER_CONCURRENCY=1",
		"schtasks /Run /TN 'wuling-runner'",
	} {
		if !strings.Contains(ud, want) {
			t.Errorf("windows user-data missing %q\n---\n%s", want, ud)
		}
	}

	// The dispatcher picks the Windows script for a windows pool and the Linux
	// (systemd/bash) one for anything else.
	if !strings.Contains(BuildUserDataForPool("s", "t", pool, "n"), "<powershell>") {
		t.Error("dispatcher should pick Windows user-data for a windows pool")
	}
	linux := BuildUserDataForPool("s", "t", Pool{Name: "lin", Labels: []string{"linux"}}, "n")
	if !strings.Contains(linux, "systemctl") || strings.Contains(linux, "<powershell>") {
		t.Errorf("dispatcher should pick Linux user-data for a non-windows pool:\n%s", linux)
	}
}

// Alibaba Cloud's Vminit agent dispatches Windows user-data on a bare
// `[powershell]` marker that must be the first line, and rejects the
// <powershell> tags EC2Launch v2 uses. Emitting the wrong one fails silently —
// the agent just never recognises the payload as a script — so pin both forms.
func TestWindowsUserDataIsProviderAware(t *testing.T) {
	pool := Pool{Name: "win", OS: "windows", Provider: "aliyun", Labels: []string{"windows"}}
	ud := BuildWindowsUserData("https://wuling.example.com", "wlrt_tok", pool, "win-01")

	if !strings.HasPrefix(ud, "[powershell]\n") {
		t.Errorf("aliyun user-data must begin with the [powershell] marker, got:\n%.40q", ud)
	}
	if strings.Contains(ud, "<powershell>") || strings.Contains(ud, "</powershell>") {
		t.Errorf("aliyun user-data must not carry EC2Launch tags:\n%s", ud)
	}
	// The payload itself is provider-independent.
	if !strings.Contains(ud, "WULING_RUNNER_TOKEN=wlrt_tok") {
		t.Errorf("aliyun user-data lost its payload:\n%s", ud)
	}
	// Aliyun rejects a script that writes under C:\Users at init time (no user
	// profile is mounted yet), so the env file must stay in ProgramData.
	if !strings.Contains(ud, `C:\ProgramData\wuling-runner`) || strings.Contains(ud, `C:\Users`) {
		t.Errorf("aliyun user-data must write to ProgramData, never C:\\Users:\n%s", ud)
	}
	// Aliyun accepts half-width characters only.
	for _, r := range ud {
		if r > 127 {
			t.Errorf("aliyun user-data must be ASCII-only, found %q", r)
			break
		}
	}

	if !strings.HasPrefix(BuildUserDataForPool("s", "t", pool, "n"), "[powershell]\n") {
		t.Error("dispatcher must carry the provider through to the Windows renderer")
	}
}
