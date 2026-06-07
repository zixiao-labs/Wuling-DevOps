package autoscale

import (
	"context"
	"encoding/json"
	"fmt"
)

// Instance is a launched runner VM, identified by the provider-specific id the
// autoscaler stores on the runner row for later termination.
type Instance struct {
	ExternalID string
}

// LaunchSpec is everything a provider needs to start one runner VM.
type LaunchSpec struct {
	Pool       Pool
	TierSpec   TierSpec
	RunnerName string
	// UserData is the cloud-init / startup script that injects the server URL
	// and the runner's wlrt_ token so the VM self-configures on boot.
	UserData string
}

// Provider launches and terminates ephemeral runner instances on one backend.
type Provider interface {
	Name() string
	Launch(ctx context.Context, spec LaunchSpec) (Instance, error)
	Terminate(ctx context.Context, externalID string) error
}

// Credential shapes, decoded from the JSON stored in the org secret named by
// each pool's credentials_secret.

type aliyunCreds struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
}

type awsCreds struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token"`
}

type proxmoxCreds struct {
	TokenID     string `json:"token_id"`
	TokenSecret string `json:"token_secret"`
}

type vcenterCreds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// CredentialSecretName returns the org-secret name a pool references for its
// provider credentials, or "" if none.
func (p Pool) CredentialSecretName() string {
	switch {
	case p.Aliyun != nil:
		return p.Aliyun.CredentialsSecret
	case p.AWS != nil:
		return p.AWS.CredentialsSecret
	case p.Proxmox != nil:
		return p.Proxmox.CredentialsSecret
	case p.VCenter != nil:
		return p.VCenter.CredentialsSecret
	}
	return ""
}

// NewProvider builds a Provider for a pool from its decrypted credentials JSON.
func NewProvider(pool Pool, credsJSON string) (Provider, error) {
	switch pool.Provider {
	case "aliyun":
		var c aliyunCreds
		if err := decodeCreds(credsJSON, &c); err != nil {
			return nil, err
		}
		return newAliyunProvider(pool, c)
	case "aws":
		var c awsCreds
		if err := decodeCreds(credsJSON, &c); err != nil {
			return nil, err
		}
		return newAWSProvider(pool, c)
	case "proxmox", "vcenter":
		// Provisioning for these is a placeholder (see proxmox.go / vcenter.go):
		// cloud-init/snippet and govmomi/SOAP injection are deployment-specific
		// and untestable without real infrastructure. Reject at creation time so
		// a misconfigured pool fails fast in reconcile instead of churning a
		// runner row on every Launch attempt. Use an aws/aliyun pool meanwhile.
		return nil, fmt.Errorf("provider %q is not supported in this build (VM provisioning is a placeholder); use aws or aliyun — see docs/pipelines.md §7", pool.Provider)
	}
	return nil, fmt.Errorf("unknown provider %q", pool.Provider)
}

func decodeCreds(s string, v any) error {
	if s == "" {
		return fmt.Errorf("provider credentials secret is empty")
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("decode provider credentials JSON: %w", err)
	}
	return nil
}
