// Package autoscale reconciles an org's CI runner fleet against its
// GitOps-managed runner-config.yaml: it launches ephemeral runners on a cloud
// or hypervisor when jobs queue, and releases them once they've been idle past
// idle_timeout. Everything is org-scoped; there is no global pool.
//
// config.go parses and validates the runner-config.yaml schema documented in
// docs/pipelines.md and runners/config/runner-config.example.yaml.
package autoscale

import (
	"fmt"
	"strings"
	"time"

	"github.com/zixiao-labs/wuling-devops/internal/model"
	"gopkg.in/yaml.v3"
)

const (
	ProviderAliyun  = "aliyun"
	ProviderAWS     = "aws"
	ProviderProxmox = "proxmox"
	ProviderVCenter = "vcenter"
)

// Config is a parsed runner-config.yaml.
type Config struct {
	Version     int                 `yaml:"version"`
	DefaultTier string              `yaml:"default_tier"`
	IdleTimeout Duration            `yaml:"idle_timeout"`
	Tiers       map[string]TierSpec `yaml:"tiers"`
	Pools       []Pool              `yaml:"pools"`
}

// TierSpec maps an abstract tier to concrete machine sizing. Memory/Storage are
// human strings (e.g. "8Gi", "80Gi") passed through to providers, which
// interpret them per their own API.
type TierSpec struct {
	CPU     int    `yaml:"cpu"`
	Memory  string `yaml:"memory"`
	Storage string `yaml:"storage"`
}

// ContainerLimits derives the per-job container caps injected into a runner's
// runner.env. Memory is reduced by a host reserve so the container's OOM killer
// doesn't take out the runner process before the offending step. CPU is not
// reduced — autoscaled runners are WULING_RUNNER_CONCURRENCY=1, so the job
// should get the whole box. Returns (0, "") when the tier is empty so a
// hand-registered static runner keeps unlimited Stage-1 behavior.
func (t TierSpec) ContainerLimits() (cpus int, memory string) {
	if t.CPU == 0 && t.Memory == "" && t.Storage == "" {
		return 0, ""
	}
	cpus = t.CPU
	if t.Memory == "" {
		return cpus, ""
	}
	totalBytes, ok := ParseSizeBytes(t.Memory)
	if !ok {
		return cpus, ""
	}
	reserve := totalBytes * 15 / 100
	if reserve < 1<<30 {
		reserve = 1 << 30 // min 1Gi
	}
	containerBytes := totalBytes - reserve
	if containerBytes <= 0 {
		return cpus, ""
	}
	// Format as GiB when clean, otherwise MiB.
	if containerBytes%(1<<30) == 0 {
		return cpus, fmt.Sprintf("%dGi", containerBytes/(1<<30))
	}
	if containerBytes%(1<<20) == 0 {
		return cpus, fmt.Sprintf("%dMi", containerBytes/(1<<20))
	}
	return cpus, fmt.Sprintf("%dMi", (containerBytes+(1<<20)-1)/(1<<20))
}

// Pool binds one provider + tier and the labels its runners advertise.
type Pool struct {
	Name     string   `yaml:"name"`
	Provider string   `yaml:"provider"` // aliyun|aws|proxmox|vcenter
	Tier     string   `yaml:"tier"`
	OS       string   `yaml:"os"` // linux (default) | windows; macos is manual-only
	Labels   []string `yaml:"labels"`
	Min      int      `yaml:"min"`
	Max      int      `yaml:"max"`

	Aliyun  *AliyunPool  `yaml:"aliyun"`
	AWS     *AWSPool     `yaml:"aws"`
	Proxmox *ProxmoxPool `yaml:"proxmox"`
	VCenter *VCenterPool `yaml:"vcenter"`
}

// AliyunPool configures an Alibaba Cloud ECS pool.
type AliyunPool struct {
	Region  string `yaml:"region"`
	ZoneID  string `yaml:"zone_id"`
	ImageID string `yaml:"image_id"`

	// InstanceType pins one ECS spec. InstanceTypes is an ordered fallback
	// list tried left-to-right on OperationDenied.NoStock.
	InstanceType       string   `yaml:"instance_type"`
	InstanceTypes      []string `yaml:"instance_types"`
	InstanceTypeFamily string   `yaml:"instance_type_family"` // reserved for future discovery

	VSwitchID               string `yaml:"vswitch_id"`
	SecurityGroupID         string `yaml:"security_group_id"`
	InternetMaxBandwidthOut int    `yaml:"internet_max_bandwidth_out"` // Mbit/s, 0~100
	InternetChargeType      string `yaml:"internet_charge_type"`       // PayByTraffic | PayByBandwidth

	SystemDiskSize             string `yaml:"system_disk_size"`
	SystemDiskCategory         string `yaml:"system_disk_category"`
	SystemDiskPerformanceLevel string `yaml:"system_disk_performance_level"`
	DataDiskSize               string `yaml:"data_disk_size"`
	DataDiskCategory           string `yaml:"data_disk_category"`

	InstanceChargeType       string  `yaml:"instance_charge_type"` // PostPaid | PrePaid
	Spot                     bool    `yaml:"spot"`
	SpotStrategy             string  `yaml:"spot_strategy"`
	SpotPriceLimit           float64 `yaml:"spot_price_limit"`
	SpotDuration             *int    `yaml:"spot_duration"`
	SpotInterruptionBehavior string  `yaml:"spot_interruption_behavior"`
	AutoReleaseHours         int     `yaml:"auto_release_hours"`

	PasswordSecret  string `yaml:"password_secret"`
	PasswordInherit bool   `yaml:"password_inherit"`
	KeyPairName     string `yaml:"key_pair_name"`
	RAMRoleName     string `yaml:"ram_role_name"`

	ResourceGroupID string            `yaml:"resource_group_id"`
	Tags            map[string]string `yaml:"tags"`

	CredentialsSecret string `yaml:"credentials_secret"`
}

// AWSPool configures an AWS EC2 pool.
type AWSPool struct {
	Region             string   `yaml:"region"`
	AMI                string   `yaml:"ami"`
	InstanceType       string   `yaml:"instance_type"`
	SubnetID           string   `yaml:"subnet_id"`
	SecurityGroupIDs   []string `yaml:"security_group_ids"`
	IAMInstanceProfile string   `yaml:"iam_instance_profile"`
	Spot               bool     `yaml:"spot"`
	CredentialsSecret  string   `yaml:"credentials_secret"`
}

// ProxmoxPool configures a Proxmox VE pool (clone a template VM).
type ProxmoxPool struct {
	APIURL            string `yaml:"api_url"`
	Node              string `yaml:"node"`
	TemplateVMID      int    `yaml:"template_vmid"`
	Storage           string `yaml:"storage"`
	Bridge            string `yaml:"bridge"`
	FullClone         bool   `yaml:"full_clone"`
	InsecureTLS       bool   `yaml:"insecure_tls"`
	CredentialsSecret string `yaml:"credentials_secret"`
}

// VCenterPool configures a VMware vCenter pool (clone a template VM).
type VCenterPool struct {
	URL               string `yaml:"url"`
	Datacenter        string `yaml:"datacenter"`
	Cluster           string `yaml:"cluster"`
	Datastore         string `yaml:"datastore"`
	ResourcePool      string `yaml:"resource_pool"`
	Folder            string `yaml:"folder"`
	Template          string `yaml:"template"`
	Network           string `yaml:"network"`
	InsecureTLS       bool   `yaml:"insecure_tls"`
	CredentialsSecret string `yaml:"credentials_secret"`
}

// Duration is a yaml-friendly time.Duration that accepts "5m", "1h", etc.
type Duration time.Duration

// UnmarshalYAML parses a Go duration string.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the standard library duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Parse unmarshals and validates a runner-config.yaml.
func Parse(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse runner-config: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// IdleTimeoutOr returns the configured idle timeout, or def when unset.
func (c *Config) IdleTimeoutOr(def time.Duration) time.Duration {
	if c.IdleTimeout.Std() > 0 {
		return c.IdleTimeout.Std()
	}
	return def
}

func (c *Config) validate() error {
	seen := map[string]bool{}
	for i := range c.Pools {
		p := &c.Pools[i]
		if p.Name == "" {
			return fmt.Errorf("pool #%d: name is required", i+1)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate pool name %q", p.Name)
		}
		seen[p.Name] = true
		if p.Tier != "" {
			if _, ok := c.Tiers[p.Tier]; !ok {
				return fmt.Errorf("pool %q: tier %q is not defined under tiers", p.Name, p.Tier)
			}
		}
		if p.Min < 0 || p.Max < 0 || (p.Max > 0 && p.Min > p.Max) {
			return fmt.Errorf("pool %q: require 0 <= min <= max", p.Name)
		}
		if err := p.validateProvider(); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pool) validateProvider() error {
	count := 0
	for _, set := range []bool{p.Aliyun != nil, p.AWS != nil, p.Proxmox != nil, p.VCenter != nil} {
		if set {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("pool %q: exactly one provider config block is required", p.Name)
	}
	// The provider field must name the one block that is set; otherwise a typo
	// like `provider: aws` with only an `aliyun:` block would pass the count
	// check yet pick the wrong backend at launch.
	switch p.Provider {
	case ProviderAliyun:
		if p.Aliyun == nil {
			return fmt.Errorf("pool %q: provider is aliyun but the aliyun: block is missing", p.Name)
		}
		if err := p.validateAliyun(); err != nil {
			return err
		}
	case ProviderAWS:
		if p.AWS == nil {
			return fmt.Errorf("pool %q: provider is aws but the aws: block is missing", p.Name)
		}
	case ProviderProxmox:
		if p.Proxmox == nil {
			return fmt.Errorf("pool %q: provider is proxmox but the proxmox: block is missing", p.Name)
		}
	case ProviderVCenter:
		if p.VCenter == nil {
			return fmt.Errorf("pool %q: provider is vcenter but the vcenter: block is missing", p.Name)
		}
	default:
		return fmt.Errorf("pool %q: provider must be aliyun|aws|proxmox|vcenter", p.Name)
	}
	// Cloud credentials live in an org Secret referenced by name. Without one the
	// autoscaler cannot authenticate and the provider API answers 401, so fail
	// GitOps validation up front rather than silently never launching.
	if p.CredentialSecretName() == "" {
		return fmt.Errorf("pool %q: credentials_secret is required — set it to the name of the org Secret holding this provider's access keys", p.Name)
	}
	// OS defaults to linux. Autoscaled pools may be linux or windows; macOS is
	// manual-registration only (Apple licensing requires Apple hardware), so a
	// macos pool could never be launched and is rejected here.
	switch p.OS {
	case "", model.OSLinux, model.OSWindows:
	case model.OSMacOS:
		return fmt.Errorf("pool %q: os %s cannot be autoscaled — register macOS runners manually (see docs/pipelines.md §7)", p.Name, model.OSMacOS)
	default:
		return fmt.Errorf("pool %q: os must be %s or %s (got %q)", p.Name, model.OSLinux, model.OSWindows, p.OS)
	}
	// Aliyun's Windows user-data plugin accepts half-width (ASCII) characters
	// only; a Chinese pool name or label would silently break instance bootstrap.
	if p.Provider == ProviderAliyun && p.OS == model.OSWindows {
		if !isASCII(p.Name) {
			return fmt.Errorf("pool %q: name must be ASCII — 阿里云 Windows 自定义数据只接受半角字符（见 docs/pipelines.md §7.1）", p.Name)
		}
		for _, l := range p.Labels {
			if !isASCII(l) {
				return fmt.Errorf("pool %q: label %q must be ASCII — 阿里云 Windows 自定义数据只接受半角字符（见 docs/pipelines.md §7.1）", p.Name, l)
			}
		}
	}
	return nil
}

func (p *Pool) validateAliyun() error {
	a := p.Aliyun
	if a.PasswordInherit && a.PasswordSecret != "" {
		return fmt.Errorf("pool %q: password_inherit 与 password_secret 互斥", p.Name)
	}
	if p.OS == model.OSWindows && !a.PasswordInherit && a.PasswordSecret == "" {
		return fmt.Errorf("pool %q: 阿里云 Windows 池必须设置 password_secret 或 password_inherit —— ECS 在 Windows 实例上忽略 key_pair_name", p.Name)
	}
	if a.SpotStrategy == "SpotWithPriceLimit" && a.SpotPriceLimit <= 0 {
		return fmt.Errorf("pool %q: spot_strategy=SpotWithPriceLimit 需要 spot_price_limit > 0", p.Name)
	}
	if a.SpotDuration != nil && (*a.SpotDuration < 0 || *a.SpotDuration > 6) {
		return fmt.Errorf("pool %q: spot_duration 只能是 0~6", p.Name)
	}
	if a.InstanceType != "" && len(a.InstanceTypes) > 0 {
		return fmt.Errorf("pool %q: instance_type 与 instance_types 二选一", p.Name)
	}
	if a.InstanceType == "" && len(a.InstanceTypes) == 0 {
		return fmt.Errorf("pool %q: 必须设置 instance_type 或 instance_types", p.Name)
	}
	if len(a.Tags) > 16 {
		return fmt.Errorf("pool %q: tags 最多 16 条（ECS 上限 20，其中 4 条由 autoscaler 占用）", p.Name)
	}
	for k := range a.Tags {
		if strings.HasPrefix(k, "aliyun") || strings.HasPrefix(k, "acs:") {
			return fmt.Errorf("pool %q: 标签键 %q 不能以 aliyun / acs: 开头（ECS 限制）", p.Name, k)
		}
	}
	return nil
}

// PasswordSecretName returns the org-secret name holding the instance login
// password, or "" when the pool needs none.
func (p Pool) PasswordSecretName() string {
	if p.Aliyun != nil {
		return p.Aliyun.PasswordSecret
	}
	return ""
}

// TierSpecFor returns the tier sizing for a pool, falling back to an empty
// spec when undefined (providers then use their own instance-type defaults).
func (c *Config) TierSpecFor(tier string) TierSpec {
	if ts, ok := c.Tiers[tier]; ok {
		return ts
	}
	return TierSpec{}
}
