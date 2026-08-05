package runnercheck

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/autoscale"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/orgconfig"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
)

// ConfigReader is the minimal GitOps config dependency required for a
// preflight. It is an interface so tests can exercise the checks without a
// git repository or database.
type ConfigReader interface {
	Read(ctx context.Context, orgID uuid.UUID, name string) (*orgconfig.File, error)
}

// SecretLister exposes metadata only. Preflight must not call GetOrgValue,
// because it needs to establish presence, not decrypt cloud credentials.
type SecretLister interface {
	ListOrg(ctx context.Context, orgID uuid.UUID) ([]model.Secret, error)
}

// HistoryStore retains non-secret preflight results. The default implementation
// is bounded in memory and therefore explicitly reports its retention limit.
type HistoryStore interface {
	Append(orgID uuid.UUID, result Result)
	List(orgID uuid.UUID) []Result
}

// Service performs safe preflights and, when wired with durable dependencies,
// queues real isolated Runner probe lifecycles.
type Service struct {
	Configs ConfigReader
	Secrets SecretLister
	History HistoryStore
	// Audits + Pipelines enable the administrator-only real lifecycle. They
	// are optional to preserve the safe preflight-only service for unit tests
	// and deployments that have not applied the durable migration yet.
	Audits            *AuditStore
	Pipelines         *pipelinestore.Store
	AutoscalerEnabled bool

	now func() time.Time
}

// NewService wires a preflight service. Nil dependencies are represented in a
// result as an unavailable local check rather than being mistaken for a passed
// cloud probe.
func NewService(configs ConfigReader, secrets SecretLister, history HistoryStore) *Service {
	return &Service{
		Configs: configs,
		Secrets: secrets,
		History: history,
		now:     time.Now,
	}
}

// NewLifecycleService wires the real, durable probe lifecycle. It still uses
// Preflight first; a VM is never queued when the local configuration is
// structurally blocked.
func NewLifecycleService(
	configs ConfigReader,
	secrets SecretLister,
	audits *AuditStore,
	pipelines *pipelinestore.Store,
	autoscalerEnabled bool,
) *Service {
	service := NewService(configs, secrets, nil)
	service.Audits = audits
	service.Pipelines = pipelines
	service.AutoscalerEnabled = autoscalerEnabled
	return service
}

// List returns the legacy bounded in-memory preflight records for one
// organization. The administrator API uses ListAudits for durable records.
func (s *Service) List(orgID uuid.UUID) []Result {
	if s == nil || s.History == nil {
		return []Result{}
	}
	return s.History.List(orgID)
}

// Preflight reads and parses the current runner-config.yaml, then validates
// only locally observable metadata:
//   - parser acceptance,
//   - org-secret presence (never its value),
//   - supported provider selection, and
//   - basic OS/network field structure.
//
// It deliberately does not construct a cloud provider, decrypt credentials,
// or call any launch API.
func (s *Service) Preflight(ctx context.Context, req Request) Result {
	result := Result{
		ID:               uuid.NewString(),
		OrgSlug:          strings.TrimSpace(req.OrgSlug),
		RequestedAt:      s.clock().UTC(),
		RequestedBy:      req.RequestedBy.String(),
		Storage:          "memory",
		Retention:        "bounded per process; lost on restart",
		Phase:            PhasePreflight,
		State:            StatePreflight,
		RunnerProbeState: StateNotRun,
		ConfigCheck: Check{
			Name:    "config_parse",
			Status:  CheckError,
			Message: "尚未读取 runner-config.yaml。",
		},
		Pools: []PoolCheck{},
	}

	if s == nil || s.Configs == nil {
		result.ConfigCheck = Check{
			Name:    "config_parse",
			Status:  CheckError,
			Message: "自检服务未配置 GitOps runner 配置读取器；未执行任何 runner 或 VM 操作。",
		}
		return s.record(req.OrgID, result)
	}

	file, err := s.Configs.Read(ctx, req.OrgID, orgconfig.RunnerConfigPath)
	if err != nil || file == nil {
		result.ConfigCheck = Check{
			Name:    "config_parse",
			Status:  CheckError,
			Message: "无法读取 runner-config.yaml；未执行任何 runner 或 VM 操作。",
		}
		return s.record(req.OrgID, result)
	}
	if !file.Exists() {
		result.ConfigCheck = Check{
			Name:    "config_parse",
			Status:  CheckFailed,
			Message: "未找到 runner-config.yaml；未执行任何 runner 或 VM 操作。",
		}
		return s.record(req.OrgID, result)
	}

	cfg, err := autoscale.Parse(file.Content)
	if err != nil {
		// Do not forward parser text: malformed YAML can include user-supplied
		// content, and a preflight response must never become a secret echo.
		result.ConfigCheck = Check{
			Name:    "config_parse",
			Status:  CheckFailed,
			Message: "runner-config.yaml 未通过 autoscaler 解析；未执行任何 runner 或 VM 操作。",
		}
		return s.record(req.OrgID, result)
	}
	result.ConfigCheck = Check{
		Name:    "config_parse",
		Status:  CheckPassed,
		Message: "runner-config.yaml 已通过 autoscaler 解析。",
	}

	secretNames, secretListErr := s.secretNames(ctx, req.OrgID)
	selected, missing := selectPools(cfg.Pools, req.PoolNames)
	for _, pool := range selected {
		result.Pools = append(result.Pools, poolPreflight(pool, secretNames, secretListErr))
	}
	for _, name := range missing {
		result.Pools = append(result.Pools, PoolCheck{
			PoolName:         name,
			Phase:            PhasePreflight,
			State:            StatePreflight,
			Readiness:        ReadinessBlocked,
			Checks:           []Check{{Name: "pool_selection", Status: CheckFailed, Message: "所选池不在当前 runner-config.yaml 中。"}},
			RunnerProbeState: StateNotRun,
			RunnerProbeNote:  "未运行：所选池不存在，因此不会创建 VM 或调用云 API。",
		})
	}
	if len(result.Pools) == 0 {
		result.Pools = []PoolCheck{}
	}
	return s.record(req.OrgID, result)
}

func (s *Service) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) record(orgID uuid.UUID, result Result) Result {
	if s != nil && s.History != nil {
		s.History.Append(orgID, result)
	}
	return result
}

func (s *Service) secretNames(ctx context.Context, orgID uuid.UUID) (map[string]struct{}, error) {
	if s.Secrets == nil {
		return nil, errSecretMetadataUnavailable{}
	}
	secrets, err := s.Secrets.ListOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		names[secret.Name] = struct{}{}
	}
	return names, nil
}

// errSecretMetadataUnavailable intentionally carries no implementation detail.
type errSecretMetadataUnavailable struct{}

func (errSecretMetadataUnavailable) Error() string { return "secret metadata unavailable" }

func selectPools(pools []autoscale.Pool, requested []string) (selected []autoscale.Pool, missing []string) {
	if len(requested) == 0 {
		return append([]autoscale.Pool(nil), pools...), nil
	}

	byName := make(map[string]autoscale.Pool, len(pools))
	for _, pool := range pools {
		byName[pool.Name] = pool
	}
	seen := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, alreadySeen := seen[name]; alreadySeen {
			continue
		}
		seen[name] = struct{}{}
		if pool, ok := byName[name]; ok {
			selected = append(selected, pool)
			continue
		}
		missing = append(missing, name)
	}
	return selected, missing
}

func poolPreflight(pool autoscale.Pool, secretNames map[string]struct{}, secretListErr error) PoolCheck {
	checks := []Check{
		{
			Name:    "config_parse",
			Status:  CheckPassed,
			Message: "该 pool 来自已通过 autoscaler 解析的 runner-config.yaml。",
		},
		providerCheck(pool),
		credentialCheck(pool, secretNames, secretListErr),
		osNetworkCheck(pool),
	}
	if pool.RunnerDataDisk != "" {
		checks = append(checks, Check{
			Name:    "runner_data_disk",
			Status:  CheckPassed,
			Message: "已配置 Runner 非系统数据盘；只有初始化、格式化和挂载成功后 Runner 才会启动。",
		})
	}
	readiness := ReadinessReady
	for _, check := range checks {
		if check.Status != CheckPassed {
			readiness = ReadinessBlocked
			break
		}
	}
	os := pool.OS
	if os == "" {
		os = model.OSLinux
	}
	return PoolCheck{
		PoolName:         pool.Name,
		Provider:         pool.Provider,
		OS:               os,
		Phase:            PhasePreflight,
		State:            StatePreflight,
		Readiness:        readiness,
		Checks:           checks,
		RunnerProbeState: StateNotRun,
		RunnerProbeNote:  "尚未运行：此为本地前置检查结果；只有 ready pool 才会排队真实 VM 探针。",
	}
}

func providerCheck(pool autoscale.Pool) Check {
	switch pool.Provider {
	case autoscale.ProviderAliyun, autoscale.ProviderAWS:
		return Check{
			Name:    "provider_support",
			Status:  CheckPassed,
			Message: "provider 受当前自检生命周期支持；本地前置检查尚未进行云端认证或资源调用。",
		}
	default:
		return Check{
			Name:    "provider_support",
			Status:  CheckUnsupported,
			Message: "当前 runner 自检只支持 aliyun 和 aws；未进行云端认证或资源调用。",
		}
	}
}

func credentialCheck(pool autoscale.Pool, names map[string]struct{}, listErr error) Check {
	if listErr != nil {
		return Check{
			Name:    "credential_secret",
			Status:  CheckError,
			Message: "无法检查组织级凭据机密的元数据；未读取任何机密值。",
		}
	}
	name := pool.CredentialSecretName()
	if name == "" {
		return Check{
			Name:    "credential_secret",
			Status:  CheckFailed,
			Message: "池未引用组织级凭据机密；未读取任何机密值。",
		}
	}
	if _, ok := names[name]; !ok {
		return Check{
			Name:    "credential_secret",
			Status:  CheckFailed,
			Message: "未找到池引用的组织级凭据机密；未读取任何机密值。",
		}
	}
	return Check{
		Name:    "credential_secret",
		Status:  CheckPassed,
		Message: "已确认池引用的组织级凭据机密存在；未读取任何机密值。",
	}
}

func osNetworkCheck(pool autoscale.Pool) Check {
	os := pool.OS
	if os == "" {
		os = model.OSLinux
	}
	if os != model.OSLinux && os != model.OSWindows {
		return Check{
			Name:    "os_network_structure",
			Status:  CheckFailed,
			Message: "OS 字段不是可 autoscale 的 linux 或 windows 值。",
		}
	}

	var missing []string
	switch pool.Provider {
	case autoscale.ProviderAliyun:
		if pool.Aliyun == nil {
			return Check{Name: "os_network_structure", Status: CheckFailed, Message: "缺少 aliyun 配置块。"}
		}
		if pool.Aliyun.Region == "" {
			missing = append(missing, "region")
		}
		if pool.Aliyun.ImageID == "" {
			missing = append(missing, "image_id")
		}
		if pool.Aliyun.VSwitchID == "" {
			missing = append(missing, "vswitch_id")
		}
		if pool.Aliyun.SecurityGroupID == "" {
			missing = append(missing, "security_group_id")
		}
	case autoscale.ProviderAWS:
		if pool.AWS == nil {
			return Check{Name: "os_network_structure", Status: CheckFailed, Message: "缺少 aws 配置块。"}
		}
		if pool.AWS.Region == "" {
			missing = append(missing, "region")
		}
		if pool.AWS.AMI == "" {
			missing = append(missing, "ami")
		}
		if pool.AWS.InstanceType == "" {
			missing = append(missing, "instance_type")
		}
		if pool.AWS.SubnetID == "" {
			missing = append(missing, "subnet_id")
		}
		if len(pool.AWS.SecurityGroupIDs) == 0 {
			missing = append(missing, "security_group_ids")
		}
	default:
		return Check{
			Name:    "os_network_structure",
			Status:  CheckNotRun,
			Message: "未评估 OS/网络字段：provider 不在当前自检支持范围内。",
		}
	}

	if len(missing) > 0 {
		return Check{
			Name:    "os_network_structure",
			Status:  CheckFailed,
			Message: "OS/网络字段结构不完整，缺少：" + strings.Join(missing, ", ") + "。",
		}
	}
	return Check{
		Name:    "os_network_structure",
		Status:  CheckPassed,
		Message: "OS 与基础网络字段结构完整；未进行云端连通性、认证或 VM 操作。",
	}
}
