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
		// unsupported document version
		"version: 3\npools: []\n",
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

func TestV1NetworkFieldsRemainOptional(t *testing.T) {
	const legacy = `
version: 1
pools:
  - name: aws-legacy
    provider: aws
    aws:
      credentials_secret: AWS_CREDS
  - name: aliyun-legacy
    provider: aliyun
    aliyun:
      instance_type: ecs.g7.large
      credentials_secret: ALIYUN_CREDS
`
	if _, err := Parse([]byte(legacy)); err != nil {
		t.Fatalf("v1 config should not require v2 network fields: %v", err)
	}
}

func TestV2CloudNetworkValidation(t *testing.T) {
	const awsV2 = `
version: 2
pools:
  - name: aws
    provider: aws
    aws:
      region: us-west-2
      ami: ami-123
      instance_type: c6i.large
      vpc_id: vpc-123
      subnet_id: subnet-123
      security_group_ids: [sg-123]
      credentials_secret: AWS_CREDS
`
	const aliyunV2 = `
version: 2
pools:
  - name: aliyun
    provider: aliyun
    aliyun:
      region: cn-hangzhou
      image_id: m-123
      instance_type: ecs.g7.large
      vpc_id: vpc-123
      vswitch_id: vsw-123
      security_group_id: sg-123
      credentials_secret: ALIYUN_CREDS
`
	for name, config := range map[string]string{"aws": awsV2, "aliyun": aliyunV2} {
		if _, err := Parse([]byte(config)); err != nil {
			t.Fatalf("valid v2 %s config: %v", name, err)
		}
	}

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"aws region", strings.Replace(awsV2, "region: us-west-2", `region: ""`, 1), "aws.region"},
		{"aws ami", strings.Replace(awsV2, "ami: ami-123", `ami: ""`, 1), "aws.ami"},
		{"aws instance type", strings.Replace(awsV2, "instance_type: c6i.large", `instance_type: ""`, 1), "aws.instance_type"},
		{"aws vpc", strings.Replace(awsV2, "vpc_id: vpc-123", `vpc_id: ""`, 1), "aws.vpc_id"},
		{"aws subnet", strings.Replace(awsV2, "subnet_id: subnet-123", `subnet_id: ""`, 1), "aws.subnet_id"},
		{"aws security groups", strings.Replace(awsV2, "security_group_ids: [sg-123]", "security_group_ids: []", 1), "security_group_ids"},
		{"aliyun region", strings.Replace(aliyunV2, "region: cn-hangzhou", `region: ""`, 1), "aliyun.region"},
		{"aliyun image", strings.Replace(aliyunV2, "image_id: m-123", `image_id: ""`, 1), "aliyun.image_id"},
		{"aliyun instance type", strings.Replace(aliyunV2, "instance_type: ecs.g7.large", `instance_type: ""`, 1), "instance_type"},
		{"aliyun vpc", strings.Replace(aliyunV2, "vpc_id: vpc-123", `vpc_id: ""`, 1), "aliyun.vpc_id"},
		{"aliyun vswitch", strings.Replace(aliyunV2, "vswitch_id: vsw-123", `vswitch_id: ""`, 1), "aliyun.vswitch_id"},
		{"aliyun security group", strings.Replace(aliyunV2, "security_group_id: sg-123", `security_group_id: ""`, 1), "aliyun.security_group_id"},
	}
	for _, tc := range tests {
		_, err := Parse([]byte(tc.src))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Parse error = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestV2DataDiskValidation(t *testing.T) {
	const valid = `
version: 2
pools:
  - name: aws
    provider: aws
    runner_data_disk: runner
    aws:
      region: us-west-2
      ami: ami-123
      instance_type: c6i.large
      vpc_id: vpc-123
      subnet_id: subnet-123
      security_group_ids: [sg-123]
      data_disks:
        - name: runner
          size: 120Gi
          category: gp3
          encrypted: true
          device_name: /dev/sdf
          delete_with_instance: true
      credentials_secret: AWS_CREDS
`
	if _, err := Parse([]byte(valid)); err != nil {
		t.Fatalf("valid named data disk: %v", err)
	}
	legacyNamedDisks := strings.Replace(valid, "version: 2", "version: 1", 1)
	if _, err := Parse([]byte(legacyNamedDisks)); err == nil || !strings.Contains(err.Error(), "version: 2") {
		t.Errorf("v1 named data disks error = %v", err)
	}

	invalidRunner := strings.Replace(valid, "runner_data_disk: runner", "runner_data_disk: missing", 1)
	if _, err := Parse([]byte(invalidRunner)); err == nil || !strings.Contains(err.Error(), "runner_data_disk") {
		t.Errorf("unmatched runner_data_disk error = %v", err)
	}
	retainedRunner := strings.Replace(valid, "delete_with_instance: true", "delete_with_instance: false", 1)
	if _, err := Parse([]byte(retainedRunner)); err == nil || !strings.Contains(err.Error(), "delete_with_instance") {
		t.Errorf("retained runner_data_disk error = %v", err)
	}
	ambiguousRunner := strings.Replace(valid,
		"          delete_with_instance: true\n",
		"          delete_with_instance: true\n        - name: cache\n          size: 120Gi\n          category: gp3\n",
		1,
	)
	if _, err := Parse([]byte(ambiguousRunner)); err == nil || !strings.Contains(err.Error(), "unique among data_disks") {
		t.Errorf("ambiguous runner_data_disk error = %v", err)
	}

	pool := Pool{
		Name:     "too-many-disks",
		Provider: ProviderAWS,
		AWS:      &AWSPool{DataDisks: make([]DataDisk, maxDataDisks+1)},
	}
	if err := pool.validateDataDisks(); err == nil || !strings.Contains(err.Error(), "最多") {
		t.Errorf("too many data disks error = %v", err)
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
	got := assignDemand(pools, demand, "")
	// First-match with overlapping pools: pool "a" absorbs both medium jobs,
	// "b" gets none, "c" gets the low job.
	if got["a"] != 2 || got["b"] != 0 || got["c"] != 1 {
		t.Errorf("assignment = %+v", got)
	}
}

func TestAssignDemandUsesEffectiveTier(t *testing.T) {
	pools := []Pool{
		{Name: "untiered", Labels: []string{"linux"}},
	}
	demand := []pipelinestore.QueuedJob{
		{Tier: "high", RunsOn: []string{"linux"}},
		{Tier: "medium", RunsOn: []string{"linux"}},
	}
	got := assignDemand(pools, demand, "")
	if got["untiered"] != 1 {
		t.Errorf("untiered pool should only absorb medium jobs, got %+v", got)
	}
	gotDefault := assignDemand(pools, demand, "high")
	if gotDefault["untiered"] != 1 {
		t.Errorf("default_tier=high should absorb the high job, got %+v", gotDefault)
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
			Region:             "cn-hangzhou",
			ImageID:            "m-abc",
			InstanceType:       "ecs.g7.large",
			VSwitchID:          "vsw-x",
			SecurityGroupID:    "sg-x",
			Spot:               true,
			SpotStrategy:       "SpotWithPriceLimit",
			SpotPriceLimit:     0.5,
			SystemDiskCategory: "cloud_essd",
			DataDiskSize:       "100Gi",
			DataDiskCategory:   "cloud_essd",
			PasswordInherit:    true,
			Tags:               map[string]string{"env": "ci"},
			CredentialsSecret:  "C",
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
		"ClientToken":                   spec.IdempotencyKey(),
		"HostName":                      "wdeadbeef",
		"SystemDisk.Size":               "80",
		"SystemDisk.Category":           "cloud_essd",
		"DataDisk.1.Size":               "100",
		"DataDisk.1.Category":           "cloud_essd",
		"DataDisk.1.DeleteWithInstance": "true",
		"SpotStrategy":                  "SpotWithPriceLimit",
		"SpotPriceLimit":                "0.500",
		"PasswordInherit":               "true",
		"Tag.1.Key":                     "managed-by",
		"Tag.5.Key":                     "wuling-runner-id",
		"Tag.5.Value":                   spec.RunnerID.String(),
		"Tag.6.Key":                     "env",
		"IoOptimized":                   "optimized",
		"InstanceChargeType":            "PostPaid",
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

	auditJobID := uuid.MustParse("00000000-0000-0000-0000-000000000099")
	spec.IsolatedJobID = auditJobID
	isolated := p.runInstancesParams(spec, "ecs.g7.large", now)
	if isolated["Tag.6.Key"] != "wuling-isolated-job" || isolated["Tag.6.Value"] != auditJobID.String() {
		t.Errorf("Aliyun isolated audit tag = %q=%q", isolated["Tag.6.Key"], isolated["Tag.6.Value"])
	}
	if isolated["Tag.7.Key"] != "env" {
		t.Errorf("Aliyun user tags should follow reserved audit tags, got Tag.7.Key=%q", isolated["Tag.7.Key"])
	}
}

func TestAliyunNamedDataDiskParams(t *testing.T) {
	p := &aliyunProvider{pool: AliyunPool{
		Region:       "cn-hangzhou",
		ImageID:      "m-abc",
		InstanceType: "ecs.g7.large",
		VPCID:        "vpc-123",
		DataDisks: []DataDisk{
			{
				Name:               "runner",
				Size:               "120Gi",
				Category:           "cloud_essd",
				PerformanceLevel:   "PL1",
				DeleteWithInstance: true,
			},
			{
				Name:               "cache",
				Size:               "80Gi",
				Category:           "cloud_efficiency",
				DeleteWithInstance: false,
			},
		},
	}}
	params := p.runInstancesParams(LaunchSpec{RunnerName: "runner"}, "ecs.g7.large", time.Now())

	for key, want := range map[string]string{
		"DataDisk.1.DiskName":           "runner",
		"DataDisk.1.Size":               "120",
		"DataDisk.1.Category":           "cloud_essd",
		"DataDisk.1.PerformanceLevel":   "PL1",
		"DataDisk.1.DeleteWithInstance": "true",
		"DataDisk.2.DiskName":           "cache",
		"DataDisk.2.Size":               "80",
		"DataDisk.2.Category":           "cloud_efficiency",
		"DataDisk.2.DeleteWithInstance": "false",
	} {
		if params[key] != want {
			t.Errorf("params[%q] = %q, want %q", key, params[key], want)
		}
	}
	if _, exists := params["VpcId"]; exists {
		t.Error("VPCID is for config validation and must not be sent to ECS RunInstances")
	}
}

func TestAWSNamedDataDiskParams(t *testing.T) {
	p := &awsProvider{pool: AWSPool{
		Region:       "us-west-2",
		AMI:          "ami-123",
		InstanceType: "c6i.large",
		VPCID:        "vpc-123",
		DataDisks: []DataDisk{
			{
				Name:               "runner",
				Size:               "120Gi",
				Category:           "gp3",
				Encrypted:          true,
				DeviceName:         "/dev/sdf",
				DeleteWithInstance: true,
			},
			{
				Name:               "cache",
				Size:               "80Gi",
				Category:           "gp2",
				DeleteWithInstance: false,
			},
		},
	}}
	params := p.runInstancesParams(LaunchSpec{RunnerName: "runner"})

	for key, want := range map[string]string{
		"BlockDeviceMapping.1.DeviceName":              "/dev/sdf",
		"BlockDeviceMapping.1.Ebs.VolumeSize":          "120",
		"BlockDeviceMapping.1.Ebs.VolumeType":          "gp3",
		"BlockDeviceMapping.1.Ebs.DeleteOnTermination": "true",
		"BlockDeviceMapping.1.Ebs.Encrypted":           "true",
		"BlockDeviceMapping.2.DeviceName":              "/dev/sdg",
		"BlockDeviceMapping.2.Ebs.VolumeSize":          "80",
		"BlockDeviceMapping.2.Ebs.VolumeType":          "gp2",
		"BlockDeviceMapping.2.Ebs.DeleteOnTermination": "false",
		"BlockDeviceMapping.2.Ebs.Encrypted":           "false",
	} {
		if got := params.Get(key); got != want {
			t.Errorf("params[%q] = %q, want %q", key, got, want)
		}
	}
	if params.Get("BlockDeviceMapping.0.DeviceName") != "" {
		t.Error("data disk mapping must not replace the root disk mapping")
	}
	if params.Get("VpcId") != "" {
		t.Error("VPCID is for config validation and must not be sent to EC2 RunInstances")
	}

	runnerID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	jobID := uuid.MustParse("00000000-0000-0000-0000-000000000099")
	tagged := p.runInstancesParams(LaunchSpec{
		RunnerName: "runner",
		OrgID:      uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		RunnerID:   runnerID,
	})
	if got := tagged.Get("ClientToken"); got != runnerID.String() {
		t.Errorf("AWS ClientToken = %q, want runner id", got)
	}
	if got := tagged.Get("TagSpecification.1.Tag.6.Key"); got != "wuling-runner-id" {
		t.Errorf("runner-id recovery tag key = %q", got)
	}
	if got := tagged.Get("TagSpecification.1.Tag.6.Value"); got != runnerID.String() {
		t.Errorf("runner-id recovery tag value = %q", got)
	}
	isolated := p.runInstancesParams(LaunchSpec{
		RunnerName:    "runner",
		OrgID:         uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		RunnerID:      runnerID,
		IsolatedJobID: jobID,
	})
	if got := isolated.Get("TagSpecification.1.Tag.7.Key"); got != "wuling-isolated-job" {
		t.Errorf("isolated audit tag key = %q", got)
	}
	if got := isolated.Get("TagSpecification.1.Tag.7.Value"); got != jobID.String() {
		t.Errorf("isolated audit tag value = %q", got)
	}
}

func TestRunnerDataDiskUserData(t *testing.T) {
	pool := Pool{
		Name:           "runner-disk",
		Provider:       ProviderAWS,
		RunnerDataDisk: "runner",
		AWS: &AWSPool{DataDisks: []DataDisk{{
			Name: "runner", Size: "120Gi", Category: "gp3", DeleteWithInstance: true,
		}}},
	}
	linux := BuildUserData("https://wuling.example.com", "wlrt_secret", pool, mediumTier, "runner-01")
	for _, want := range []string{
		"wuling_runner_is_raw_data_disk",
		"wuling_runner_expected_data_disk_bytes=128849018880",
		"expected exactly one raw non-boot data disk with configured capacity",
		"mkfs.ext4 -F",
		"mount \"$runner_data_disk\" /var/lib/wuling-runner",
		"RequiresMountsFor=/var/lib/wuling-runner",
		"ConditionPathIsMountPoint=/var/lib/wuling-runner",
		"WULING_RUNNER_DATA_DISK_READY=1",
		"WULING_RUNNER_WORK_DIR=/var/lib/wuling-runner/work",
		"WULING_RUNNER_TOOLS_DIR=/var/lib/wuling-runner/tools",
		"WULING_RUNNER_STATE_DIR=/var/lib/wuling-runner/state",
	} {
		if !strings.Contains(linux, want) {
			t.Errorf("linux data-disk user-data missing %q\n---\n%s", want, linux)
		}
	}
	legacyLinux := BuildUserData("https://wuling.example.com", "wlrt_secret", Pool{}, mediumTier, "runner-01")
	if strings.Contains(legacyLinux, "wuling_runner_is_raw_data_disk") ||
		strings.Contains(legacyLinux, "WULING_RUNNER_WORK_DIR=") ||
		strings.Contains(legacyLinux, "WULING_RUNNER_DATA_DISK_READY=") {
		t.Errorf("legacy Linux user-data changed unexpectedly:\n%s", legacyLinux)
	}

	windowsPool := pool
	windowsPool.OS = "windows"
	windows := BuildWindowsUserData("https://wuling.example.com", "wlrt_secret", windowsPool, mediumTier, "runner-01")
	for _, want := range []string{
		"Get-Disk | Where-Object",
		"$_.PartitionStyle -eq 'RAW'",
		"$wulingRunnerExpectedDataDiskBytes = 128849018880",
		"exactly one raw, non-boot data disk with configured capacity",
		"Initialize-Disk",
		"New-Partition -DiskNumber $runnerDisk.Number -UseMaximumSize -DriveLetter W",
		"Format-Volume",
		"$runnerRoot = 'W:\\wuling-runner'",
		"WULING_RUNNER_DATA_DISK_READY=1",
		"WULING_RUNNER_WORK_DIR=W:\\wuling-runner\\work",
		"WULING_RUNNER_TOOLS_DIR=W:\\wuling-runner\\tools",
		"WULING_RUNNER_STATE_DIR=W:\\wuling-runner\\state",
	} {
		if !strings.Contains(windows, want) {
			t.Errorf("windows data-disk user-data missing %q\n---\n%s", want, windows)
		}
	}
	legacyWindows := BuildWindowsUserData("https://wuling.example.com", "wlrt_secret", Pool{OS: "windows"}, mediumTier, "runner-01")
	if strings.Contains(legacyWindows, "Get-Disk | Where-Object") ||
		strings.Contains(legacyWindows, "WULING_RUNNER_WORK_DIR=") ||
		strings.Contains(legacyWindows, "WULING_RUNNER_DATA_DISK_READY=") {
		t.Errorf("legacy Windows user-data changed unexpectedly:\n%s", legacyWindows)
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

func TestV2CloudTopologyResponses(t *testing.T) {
	awsSubnet := []byte(`<DescribeSubnetsResponse><subnetSet><item><subnetId>subnet-a</subnetId><vpcId>vpc-a</vpcId></item></subnetSet></DescribeSubnetsResponse>`)
	awsGroups := []byte(`<DescribeSecurityGroupsResponse><securityGroupInfo><item><groupId>sg-a</groupId><vpcId>vpc-a</vpcId></item><item><groupId>sg-b</groupId><vpcId>vpc-a</vpcId></item></securityGroupInfo></DescribeSecurityGroupsResponse>`)
	if err := validateAWSSubnetVPC(awsSubnet, "subnet-a", "vpc-a"); err != nil {
		t.Fatalf("validateAWSSubnetVPC: %v", err)
	}
	if err := validateAWSSecurityGroupVPC(awsGroups, []string{"sg-a", "sg-b"}, "vpc-a"); err != nil {
		t.Fatalf("validateAWSSecurityGroupVPC: %v", err)
	}
	if err := validateAWSSubnetVPC(awsSubnet, "subnet-a", "vpc-other"); err == nil {
		t.Fatal("AWS subnet in another VPC was accepted")
	}
	if err := validateAWSSubnetVPC([]byte(`<DescribeSubnetsResponse><subnetSet><item><subnetId>subnet-other</subnetId><vpcId>vpc-a</vpcId></item></subnetSet></DescribeSubnetsResponse>`), "subnet-a", "vpc-a"); err == nil {
		t.Fatal("unexpected AWS subnet was accepted")
	}
	if err := validateAWSSecurityGroupVPC([]byte(`<DescribeSecurityGroupsResponse><securityGroupInfo><item><groupId>sg-a</groupId><vpcId>vpc-other</vpcId></item></securityGroupInfo></DescribeSecurityGroupsResponse>`), []string{"sg-a"}, "vpc-a"); err == nil {
		t.Fatal("AWS security group in another VPC was accepted")
	}
	if err := validateAWSSecurityGroupVPC([]byte(`<DescribeSecurityGroupsResponse><securityGroupInfo><item><groupId>sg-other</groupId><vpcId>vpc-a</vpcId></item></securityGroupInfo></DescribeSecurityGroupsResponse>`), []string{"sg-a"}, "vpc-a"); err == nil {
		t.Fatal("unexpected AWS security group was accepted")
	}

	if err := validateAliyunVSwitchTopology([]byte(`{"VSwitchId":"vsw-a","VpcId":"vpc-a","ZoneId":"cn-hangzhou-i"}`), "vsw-a", "vpc-a", "cn-hangzhou-i"); err != nil {
		t.Fatalf("validateAliyunVSwitchTopology: %v", err)
	}
	if err := validateAliyunVSwitchTopology([]byte(`{"VSwitchId":"vsw-a","VpcId":"vpc-a","ZoneId":"cn-hangzhou-i"}`), "vsw-a", "vpc-a", "cn-hangzhou-j"); err == nil {
		t.Fatal("Aliyun vswitch in another zone was accepted")
	}
	if err := validateAliyunSecurityGroupVPC([]byte(`{"SecurityGroupId":"sg-a","VpcId":"vpc-a"}`), "sg-a", "vpc-a"); err != nil {
		t.Fatalf("validateAliyunSecurityGroupVPC: %v", err)
	}
	if err := validateAliyunSecurityGroupVPC([]byte(`{"SecurityGroupId":"sg-a","VpcId":"vpc-other"}`), "sg-a", "vpc-a"); err == nil {
		t.Fatal("Aliyun security group in another VPC was accepted")
	}
}

func TestParseRunnerInstanceRecovery(t *testing.T) {
	awsBody := []byte(`
		<DescribeInstancesResponse>
		  <reservationSet>
		    <item>
		      <instancesSet>
		        <item><instanceId>i-abc</instanceId></item>
		      </instancesSet>
		    </item>
		  </reservationSet>
		</DescribeInstancesResponse>`)
	id, found, err := parseAWSRunnerInstance(awsBody)
	if err != nil || !found || id != "i-abc" {
		t.Fatalf("parseAWSRunnerInstance = %q found=%v err=%v", id, found, err)
	}

	aliyunBody := []byte(`{"Instances":{"Instance":[{"InstanceId":"i-hangzhou123"}]}}`)
	id, found, err = parseAliyunRunnerInstance(aliyunBody)
	if err != nil || !found || id != "i-hangzhou123" {
		t.Fatalf("parseAliyunRunnerInstance = %q found=%v err=%v", id, found, err)
	}

	if _, _, err := parseAWSRunnerInstance([]byte(`
		<DescribeInstancesResponse>
		  <reservationSet>
		    <item><instancesSet>
		      <item><instanceId>i-1</instanceId></item>
		      <item><instanceId>i-2</instanceId></item>
		    </instancesSet></item>
		  </reservationSet>
		</DescribeInstancesResponse>`)); err == nil {
		t.Fatal("multiple AWS instances for one runner id were accepted")
	}
	if _, _, err := parseAliyunRunnerInstance([]byte(`{"Instances":{"Instance":[{"InstanceId":"i-1"},{"InstanceId":"i-2"}]}}`)); err == nil {
		t.Fatal("multiple Aliyun instances for one runner id were accepted")
	}
}
