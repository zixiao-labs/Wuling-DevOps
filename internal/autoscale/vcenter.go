package autoscale

import (
	"context"
	"fmt"
)

// vcenterProvider targets VMware vCenter by cloning a template VM.
//
// vCenter's provisioning API is SOAP (govmomi) — meaningfully more involved
// than a signed REST call, and injecting the runner token requires a
// guest-customization spec or guestinfo extraConfig. Rather than vendor
// govmomi and ship an untestable SOAP path, vCenter provisioning is left
// unimplemented here; AWS and Aliyun pools are fully supported. Track in
// docs/pipelines.md (slated alongside the self-hosted K8s provider work).
type vcenterProvider struct {
	pool  VCenterPool
	creds vcenterCreds
}

func newVCenterProvider(pool Pool, creds vcenterCreds) (Provider, error) {
	if pool.VCenter == nil {
		return nil, fmt.Errorf("vcenter pool config missing")
	}
	if creds.Username == "" || creds.Password == "" {
		return nil, fmt.Errorf("vcenter credentials missing username/password")
	}
	if pool.VCenter.URL == "" || pool.VCenter.Template == "" {
		return nil, fmt.Errorf("vcenter pool requires url and template")
	}
	return &vcenterProvider{pool: *pool.VCenter, creds: creds}, nil
}

func (p *vcenterProvider) Name() string { return "vcenter" }

func (p *vcenterProvider) Launch(ctx context.Context, spec LaunchSpec) (Instance, error) {
	return Instance{}, fmt.Errorf("vcenter provisioning not implemented in this build (requires govmomi SOAP + guest customization); use an aws/aliyun pool — see docs/pipelines.md")
}

func (p *vcenterProvider) Terminate(ctx context.Context, externalID string) error {
	return fmt.Errorf("vcenter provisioning not implemented in this build")
}
