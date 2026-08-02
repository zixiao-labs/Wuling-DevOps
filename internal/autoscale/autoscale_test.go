package autoscale

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
      vswitch_id: vsw-x
      security_group_id: sg-x
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
		"tiers: {low: {cpu: 1}}\npools:\n  - name: p\n    provider: aws\n    tier: low\n    aws: {region: r, credentials_secret: C}\n    aliyun: {region: r}\n",
		// tier not defined
		"pools:\n  - name: p\n    provider: aws\n    tier: ghost\n    aws: {region: r, credentials_secret: C}\n",
		// min > max
		"tiers: {low: {cpu: 1}}\npools:\n  - name: p\n    provider: aws\n    tier: low\n    min: 5\n    max: 1\n    aws: {region: r, credentials_secret: C}\n",
		// duplicate pool name
		"tiers: {low: {cpu: 1}}\npools:\n  - {name: p, provider: aws, tier: low, aws: {region: r, credentials_secret: C}}\n  - {name: p, provider: aws, tier: low, aws: {region: r, credentials_secret: C}}\n",
		// aliyun windows non-ASCII label
		"tiers: {low: {cpu: 1}}\npools:\n  - name: win\n    provider: aliyun\n    tier: low\n    os: windows\n    labels: [构建机]\n    aliyun:\n      region: r\n      image_id: m-x\n      instance_type: ecs.g7.large\n      vswitch_id: vsw-x\n      security_group_id: sg-x\n      password_secret: P\n      credentials_secret: C\n",
		// aliyun windows non-ASCII pool name
		"tiers: {low: {cpu: 1}}\npools:\n  - name: 构建机\n    provider: aliyun\n    tier: low\n    os: windows\n    labels: [windows]\n    aliyun:\n      region: r\n      image_id: m-x\n      instance_type: ecs.g7.large\n      vswitch_id: vsw-x\n      security_group_id: sg-x\n      password_secret: P\n      credentials_secret: C\n",
		// aliyun missing instance type
		"tiers: {low: {cpu: 1}}\npools:\n  - name: p\n    provider: aliyun\n    tier: low\n    aliyun:\n      region: r\n      image_id: m-x\n      vswitch_id: vsw-x\n      security_group_id: sg-x\n      credentials_secret: C\n",
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

var mediumTier = TierSpec{CPU: 4, Memory: "8Gi", Storage: "80Gi"}

func TestWindowsUserData(t *testing.T) {
	pool := Pool{Name: "win", OS: "windows", Provider: "aws", Labels: []string{"windows", "msvc"}}
	ud := BuildWindowsUserData("https://wuling.example.com", "wlrt_deadbeef_secret", pool, mediumTier, "win-01")
	for _, want := range []string{
		"<powershell>",
		"</powershell>",
		"WULING_RUNNER_SERVER_URL=https://wuling.example.com",
		"WULING_RUNNER_TOKEN=wlrt_deadbeef_secret",
		"WULING_RUNNER_LABELS=windows,msvc",
		"WULING_RUNNER_CONCURRENCY=1",
		"WULING_RUNNER_CPUS=4",
		"WULING_RUNNER_MEMORY=",
		"WULING_RUNNER_PIDS_LIMIT=4096",
		"schtasks /Run /TN 'wuling-runner'",
	} {
		if want == "WULING_RUNNER_MEMORY=" {
			if !strings.Contains(ud, "WULING_RUNNER_MEMORY=") {
				t.Errorf("windows user-data missing memory limit\n---\n%s", ud)
			}
			continue
		}
		if !strings.Contains(ud, want) {
			t.Errorf("windows user-data missing %q\n---\n%s", want, ud)
		}
	}

	if !strings.Contains(BuildUserDataForPool("s", "t", pool, mediumTier, "n"), "<powershell>") {
		t.Error("dispatcher should pick Windows user-data for a windows pool")
	}
	linux := BuildUserDataForPool("s", "t", Pool{Name: "lin", Labels: []string{"linux"}}, mediumTier, "n")
	if !strings.Contains(linux, "systemctl") || strings.Contains(linux, "<powershell>") {
		t.Errorf("dispatcher should pick Linux user-data for a non-windows pool:\n%s", linux)
	}
}

func TestWindowsUserDataIsProviderAware(t *testing.T) {
	pool := Pool{Name: "win", OS: "windows", Provider: "aliyun", Labels: []string{"windows"}}
	ud := BuildWindowsUserData("https://wuling.example.com", "wlrt_tok", pool, mediumTier, "win-01")

	if !strings.HasPrefix(ud, "[powershell]\n") {
		t.Errorf("aliyun user-data must begin with the [powershell] marker, got:\n%.40q", ud)
	}
	if strings.Contains(ud, "<powershell>") || strings.Contains(ud, "</powershell>") {
		t.Errorf("aliyun user-data must not carry EC2Launch tags:\n%s", ud)
	}
	if !strings.Contains(ud, "WULING_RUNNER_TOKEN=wlrt_tok") {
		t.Errorf("aliyun user-data lost its payload:\n%s", ud)
	}
	if !strings.Contains(ud, `C:\ProgramData\wuling-runner`) || strings.Contains(ud, `C:\Users`) {
		t.Errorf("aliyun user-data must write to ProgramData, never C:\\Users:\n%s", ud)
	}
	for _, r := range ud {
		if r > 127 {
			t.Errorf("aliyun user-data must be ASCII-only, found %q", r)
			break
		}
	}

	if !strings.HasPrefix(BuildUserDataForPool("s", "t", pool, mediumTier, "n"), "[powershell]\n") {
		t.Error("dispatcher must carry the provider through to the Windows renderer")
	}
}

func TestParseSizeGiB(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"80Gi", 80, true},
		{"80GiB", 80, true},
		{"81920Mi", 80, true},
		{"80", 80, true},
		{"", 0, false},
		{"bogus", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseSizeGiB(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseSizeGiB(%q) ok=%v want %v", tc.in, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("ParseSizeGiB(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
	// 8192 bytes = 1 GiB rounded up? 8192 bytes is tiny, should round up to 1 GiB minimum if ok
	bytes, ok := ParseSizeBytes("8192")
	if !ok || bytes != 8192 {
		t.Errorf("ParseSizeBytes(8192) = %d, %v", bytes, ok)
	}
}

func TestContainerLimits(t *testing.T) {
	cpus, mem := TierSpec{}.ContainerLimits()
	if cpus != 0 || mem != "" {
		t.Errorf("empty tier: got cpus=%d mem=%q", cpus, mem)
	}
	cpus, mem = mediumTier.ContainerLimits()
	if cpus != 4 {
		t.Errorf("cpus = %d, want 4", cpus)
	}
	if mem == "" {
		t.Error("expected non-empty memory limit for medium tier")
	}
}

func TestAliyunHostName(t *testing.T) {
	if got := aliyunHostName("aliyun-medium-a1b2c3d4", "linux"); got != "aliyun-medium-a1b2c3d4" {
		t.Errorf("linux hostname = %q", got)
	}
	if got := aliyunHostName("aliyun-medium-a1b2c3d4", "windows"); got != "wa1b2c3d4" {
		t.Errorf("windows hostname = %q, want wa1b2c3d4", got)
	}
	if got := aliyunHostName("aliyun-medium-12345678", "windows"); got != "w12345678" {
		t.Errorf("all-digit suffix must get w prefix: %q", got)
	}
	if len(aliyunHostName("12345678901234567890", "windows")) > 15 {
		t.Error("windows hostname exceeds 15 chars")
	}
}

func TestRunInstancesParams(t *testing.T) {
	pool := Pool{
		Name:     "aliyun-medium",
		Provider: ProviderAliyun,
		OS:       "windows",
		Aliyun: &AliyunPool{
			Region:                  "cn-hangzhou",
			ImageID:                 "m-abc",
			InstanceType:            "ecs.g7.large",
			VSwitchID:               "vsw-x",
			SecurityGroupID:         "sg-x",
			Spot:                    true,
			SpotStrategy:            "SpotWithPriceLimit",
			SpotPriceLimit:          0.5,
			SystemDiskCategory:      "cloud_essd",
			PasswordInherit:         true,
			Tags:                    map[string]string{"env": "ci"},
			CredentialsSecret:       "C",
		},
	}
	p := &aliyunProvider{pool: *pool.Aliyun, password: "secret"}
	spec := LaunchSpec{
		Pool:       pool,
		TierSpec:   mediumTier,
		RunnerName: "aliyun-medium-deadbeef",
		UserData:   "#!/bin/bash\n",
		OrgID:      uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		RunnerID:   uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	params := p.runInstancesParams(spec, "ecs.g7.large", now)

	for k, want := range map[string]string{
		"ClientToken":              spec.IdempotencyKey(),
		"HostName":                 "wdeadbeef",
		"SystemDisk.Size":          "80",
		"SystemDisk.Category":      "cloud_essd",
		"SpotStrategy":             "SpotWithPriceLimit",
		"SpotPriceLimit":           "0.500",
		"PasswordInherit":          "true",
		"Tag.1.Key":                "managed-by",
		"Tag.5.Key":                "env",
		"IoOptimized":              "optimized",
		"InstanceChargeType":       "PostPaid",
	} {
		if params[k] != want {
			t.Errorf("params[%q] = %q, want %q", k, params[k], want)
		}
	}
	if _, ok := params["Password"]; ok {
		t.Error("Password must be absent when PasswordInherit is true")
	}

	params2 := p.runInstancesParams(spec, "ecs.g7.large", now)
	for k, v := range params {
		if params2[k] != v {
			t.Errorf("params not stable across calls: %q = %q vs %q", k, v, params2[k])
		}
	}
}

func TestAliyunAPIError(t *testing.T) {
	err := &aliyunAPIError{HTTPStatus: 429, Code: "Throttling"}
	if !err.retryable() {
		t.Error("429 should be retryable")
	}
	err = &aliyunAPIError{Code: "OperationDenied.NoStock"}
	if !err.outOfCapacity() {
		t.Error("NoStock should be outOfCapacity")
	}
	err = &aliyunAPIError{Code: "InvalidInstanceId.NotFound"}
	if !err.notFound() {
		t.Error("NotFound should be notFound")
	}
}
