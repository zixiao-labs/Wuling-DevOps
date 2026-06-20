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
	Region                  string `yaml:"region"`
	ZoneID                  string `yaml:"zone_id"`
	ImageID                 string `yaml:"image_id"`
	InstanceType            string `yaml:"instance_type"`
	VSwitchID               string `yaml:"vswitch_id"`
	SecurityGroupID         string `yaml:"security_group_id"`
	InternetMaxBandwidthOut int    `yaml:"internet_max_bandwidth_out"`
	Spot                    bool   `yaml:"spot"`
	CredentialsSecret       string `yaml:"credentials_secret"`
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
	case "aliyun":
		if p.Aliyun == nil {
			return fmt.Errorf("pool %q: provider is aliyun but the aliyun: block is missing", p.Name)
		}
	case "aws":
		if p.AWS == nil {
			return fmt.Errorf("pool %q: provider is aws but the aws: block is missing", p.Name)
		}
	case "proxmox":
		if p.Proxmox == nil {
			return fmt.Errorf("pool %q: provider is proxmox but the proxmox: block is missing", p.Name)
		}
	case "vcenter":
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
	return nil
}

// TierSpecFor returns the tier sizing for a pool, falling back to an empty
// spec when undefined (providers then use their own instance-type defaults).
func (c *Config) TierSpecFor(tier string) TierSpec {
	if ts, ok := c.Tiers[tier]; ok {
		return ts
	}
	return TierSpec{}
}
