package autoscale

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// proxmoxProvider targets a Proxmox VE cluster by cloning a template VM.
//
// Cloning, power-on, and deletion are straightforward REST calls. The one
// genuinely awkward piece on Proxmox is injecting *raw* cloud-init user-data
// (the runner token): Proxmox sources it from a `cicustom` snippet file on a
// snippets-enabled storage, and there is no first-class API to upload a
// snippet. Wiring that (snippet upload via the storage's filesystem, or a
// shared NFS snippets dir) is deployment-specific, so VM provisioning is left
// unimplemented here rather than shipped half-working. AWS and Aliyun pools
// are fully supported; track this in docs/pipelines.md.
type proxmoxProvider struct {
	pool  ProxmoxPool
	creds proxmoxCreds
	http  *http.Client
}

func newProxmoxProvider(pool Pool, creds proxmoxCreds) (Provider, error) {
	if pool.Proxmox == nil {
		return nil, fmt.Errorf("proxmox pool config missing")
	}
	if creds.TokenID == "" || creds.TokenSecret == "" {
		return nil, fmt.Errorf("proxmox credentials missing token_id/token_secret")
	}
	if pool.Proxmox.APIURL == "" || pool.Proxmox.Node == "" || pool.Proxmox.TemplateVMID == 0 {
		return nil, fmt.Errorf("proxmox pool requires api_url, node, template_vmid")
	}
	return &proxmoxProvider{pool: *pool.Proxmox, creds: creds, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (p *proxmoxProvider) Name() string { return "proxmox" }

func (p *proxmoxProvider) Launch(ctx context.Context, spec LaunchSpec) (Instance, error) {
	return Instance{}, fmt.Errorf("proxmox provisioning not implemented in this build (cloud-init snippet injection is deployment-specific); use an aws/aliyun pool or wire snippet storage — see docs/pipelines.md")
}

func (p *proxmoxProvider) Terminate(ctx context.Context, externalID string) error {
	return fmt.Errorf("proxmox provisioning not implemented in this build")
}
