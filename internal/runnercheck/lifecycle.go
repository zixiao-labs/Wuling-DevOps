package runnercheck

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/autoscale"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/pipeline"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
)

const (
	probeSecretEnv      = "WULING_SELF_CHECK_PROBE"
	probeDataDiskMarker = "WULING_SELF_CHECK_DATA_DISK=ok"
	probeMessage        = "wuling-runner-self-check/v1"
)

// ConfigLocator is implemented by orgconfig.Store. A real probe is materialized
// as an internal, no-checkout pipeline job in the same GitOps config repository
// that owns runner-config.yaml; this avoids creating a hidden user project or
// injecting the organization's normal project secrets.
type ConfigLocator interface {
	Locate(ctx context.Context, orgID uuid.UUID) (*model.Project, *model.Repo, string, error)
}

// StartResult is the safe, asynchronous result of an administrator request.
// Each durable audit row corresponds to exactly one selected pool and one
// billable ephemeral VM attempt. It intentionally contains no probe secret,
// runner token, credential value, or raw command log.
type StartResult struct {
	OrgSlug      string        `json:"org_slug"`
	RequestedAt  time.Time     `json:"requested_at"`
	ConfigCheck  Check         `json:"config_check"`
	Checks       []AuditRecord `json:"checks"`
	BlockedPools []PoolCheck   `json:"blocked_pools,omitempty"`
}

// Start creates durable, explicit-pool isolated jobs for all selected pools
// that passed local preflight. The autoscaler then performs the actual cloud
// provisioning, so the request never blocks while a billed VM boots.
func (s *Service) Start(ctx context.Context, req Request) (*StartResult, error) {
	if s == nil || s.Audits == nil || s.Pipelines == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "runner self-check lifecycle is unavailable")
	}

	preflight := s.Preflight(ctx, req)
	out := &StartResult{
		OrgSlug:     preflight.OrgSlug,
		RequestedAt: preflight.RequestedAt,
		ConfigCheck: preflight.ConfigCheck,
		Checks:      []AuditRecord{},
	}
	if preflight.ConfigCheck.Status != CheckPassed {
		out.BlockedPools = preflight.Pools
		return out, nil
	}
	if !s.AutoscalerEnabled {
		out.BlockedPools = append([]PoolCheck(nil), preflight.Pools...)
		for i := range out.BlockedPools {
			out.BlockedPools[i].Readiness = ReadinessBlocked
			out.BlockedPools[i].Checks = append(out.BlockedPools[i].Checks, Check{
				Name:    "autoscaler_enabled",
				Status:  CheckFailed,
				Message: "autoscaler 当前未启用；不会创建临时 VM。",
			})
		}
		return out, nil
	}

	locator, ok := s.Configs.(ConfigLocator)
	if !ok {
		return nil, apperr.New(apperr.CodeUnavailable, "runner self-check cannot locate the GitOps config repository")
	}
	project, repo, _, err := locator.Locate(ctx, req.OrgID)
	if err != nil {
		return nil, err
	}
	if project == nil || repo == nil {
		return nil, apperr.NotFound("runner configuration repository")
	}

	file, err := s.Configs.Read(ctx, req.OrgID, "runner-config.yaml")
	if err != nil || file == nil || !file.Exists() {
		return nil, apperr.NotFound("runner-config.yaml")
	}
	cfg, err := autoscale.Parse(file.Content)
	if err != nil {
		// Preflight already returned a scrubbed failure above; do not forward
		// parser text from a GitOps file through this path.
		return nil, apperr.Validation("runner-config.yaml is no longer valid", nil)
	}
	selected, _ := selectPools(cfg.Pools, req.PoolNames)
	preflightByPool := make(map[string]PoolCheck, len(preflight.Pools))
	for _, pool := range preflight.Pools {
		preflightByPool[pool.PoolName] = pool
	}

	readyPools := make([]autoscale.Pool, 0, len(selected))
	for _, pool := range selected {
		poolResult, ok := preflightByPool[pool.Name]
		if !ok || poolResult.Readiness != ReadinessReady {
			continue
		}
		active, err := s.Audits.HasActive(ctx, req.OrgID, pool.Name)
		if err != nil {
			return nil, err
		}
		if active {
			return nil, apperr.Conflict("an active runner self-check already exists for pool " + pool.Name)
		}
		readyPools = append(readyPools, pool)
	}

	type preparedProbe struct {
		pool  autoscale.Pool
		audit *AuditRecord
	}
	prepared := make([]preparedProbe, 0, len(readyPools))

	// Reserve every audit slot before creating any queueable pipeline job. If
	// concurrent administrators race on one selected pool, the unique active
	// record rejects the whole request without leaving a subset of its other
	// pools able to create billable VMs.
	for _, pool := range readyPools {
		poolResult := preflightByPool[pool.Name]
		secret, err := newProbeSecret()
		if err != nil {
			for _, prior := range prepared {
				_ = s.Audits.MarkStartFailed(ctx, prior.audit.ID, "自检请求未完整排队；未创建云实例。")
			}
			return nil, apperr.Internal(err)
		}
		checks := append([]Check(nil), poolResult.Checks...)
		checks = append(checks, Check{
			Name:    "runner_probe",
			Status:  CheckNotRun,
			Message: "已排队：将创建一台一次性 Runner VM，验证命令、探针环境变量与流式日志脱敏后自动清理。",
		})
		audit, err := s.Audits.Create(ctx, CreateAuditParams{
			OrgID:       req.OrgID,
			RequestedBy: req.RequestedBy,
			PoolName:    pool.Name,
			Provider:    pool.Provider,
			OS:          poolOS(pool),
			Checks:      checks,
			ProbeSecret: secret,
		})
		if err != nil {
			for _, prior := range prepared {
				_ = s.Audits.MarkStartFailed(ctx, prior.audit.ID, "自检请求未完整排队；未创建云实例。")
			}
			return nil, err
		}
		prepared = append(prepared, preparedProbe{
			pool:  pool,
			audit: audit,
		})
	}

	for i := range prepared {
		probe := &prepared[i]
		run, jobID, err := s.createProbeRun(ctx, req, project, repo, file.CommitSHA, probe.pool)
		if err != nil {
			failSummary := "无法创建内部 Runner 探针任务；未创建云实例。"
			for _, pending := range prepared[i:] {
				_ = s.Audits.MarkStartFailed(ctx, pending.audit.ID, failSummary)
				pending.audit.Phase = PhaseCleanup
				pending.audit.State = StateFailed
				pending.audit.Summary = failSummary
				out.Checks = append(out.Checks, *pending.audit)
			}
			return nil, err
		}
		if err := s.Audits.LinkPipeline(ctx, probe.audit.ID, run.ID, jobID); err != nil {
			// The pipeline job exists but has not yet been handed to a runner.
			// Canceling it avoids an untracked billed VM if the audit linkage
			// cannot be committed. Record cancel failures so cleanup retries can
			// still see an uncanceled queued run.
			failSummary := "无法关联内部 Runner 探针任务；未创建云实例。"
			if cancelErr := s.Pipelines.CancelRun(ctx, run.ID); cancelErr != nil {
				failSummary = "无法关联内部 Runner 探针任务；取消排队任务失败，清理将重试。"
			}
			for _, pending := range prepared[i:] {
				_ = s.Audits.MarkStartFailed(ctx, pending.audit.ID, failSummary)
			}
			return nil, err
		}
		probe.audit.RunID = &run.ID
		probe.audit.JobID = &jobID
		probe.audit.Phase = PhaseProvision
		probe.audit.State = StateQueued
		probe.audit.StartedAt = ptrTime(s.clock().UTC())
		out.Checks = append(out.Checks, *probe.audit)
	}

	for _, pool := range preflight.Pools {
		if pool.Readiness != ReadinessReady {
			out.BlockedPools = append(out.BlockedPools, pool)
		}
	}
	return out, nil
}

// ListAudits returns restart-safe per-pool lifecycle records.
func (s *Service) ListAudits(ctx context.Context, orgID uuid.UUID) ([]AuditRecord, error) {
	if s == nil || s.Audits == nil {
		return nil, apperr.New(apperr.CodeUnavailable, "runner self-check lifecycle is unavailable")
	}
	return s.Audits.List(ctx, orgID, 50)
}

// ProbeSecretsForJob returns the sole one-time diagnostic secret for an
// internal self-check job. Normal organization/project secrets are expressly
// not resolved for this path.
func (s *Service) ProbeSecretsForJob(ctx context.Context, jobID uuid.UUID) (map[string]string, bool, error) {
	if s == nil || s.Audits == nil {
		return nil, false, nil
	}
	audit, err := s.Audits.GetByJob(ctx, jobID)
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	secret, err := s.Audits.ProbeSecret(ctx, audit.ID)
	if err != nil {
		return nil, true, err
	}
	return map[string]string{probeSecretEnv: secret}, true, nil
}

// MarkProbeExecuting makes the registered VM visible in the durable audit as
// soon as it acquires the reserved internal job.
func (s *Service) MarkProbeExecuting(ctx context.Context, jobID, runnerID uuid.UUID) {
	if s == nil || s.Audits == nil {
		return
	}
	if err := s.Audits.MarkExecutingForRunner(ctx, jobID, runnerID); err != nil && !isNotFound(err) {
		// The API path has already successfully acquired a job; audit telemetry
		// must never make runner dispatch fail or expose an internal DB error.
		return
	}
}

// CompleteProbe validates a non-secret HMAC proof, command marker, and the
// intentional redaction canary after the runner completed the pipeline job.
// It only updates an existing self-check audit and is safe to call for every
// normal job completion.
func (s *Service) CompleteProbe(ctx context.Context, jobID uuid.UUID, conclusion string) {
	if s == nil || s.Audits == nil || s.Pipelines == nil {
		return
	}
	audit, err := s.Audits.GetByJob(ctx, jobID)
	if err != nil {
		return
	}
	secret, err := s.Audits.ProbeSecret(ctx, audit.ID)
	if err != nil {
		_ = s.Audits.MarkProbeFinished(ctx, jobID, false, "无法验证一次性探针证明；将在清理后销毁临时实例。")
		return
	}
	log, _, err := s.Pipelines.ReadLog(ctx, jobID, 0, pipelinestore.MaxLogReadBytes)
	if err != nil {
		_ = s.Audits.MarkProbeFinished(ctx, jobID, false, "无法读取 Runner 探针日志；将在清理后销毁临时实例。")
		return
	}

	expectedProof := probeProof(secret)
	commandOK := strings.Contains(string(log), "WULING_SELF_CHECK_COMMAND=ok")
	proofOK := strings.Contains(string(log), "WULING_SELF_CHECK_PROOF="+expectedProof)
	redacted := strings.Contains(string(log), "[REDACTED]")
	leaked := strings.Contains(string(log), secret)
	requiresDataDisk := auditUsesRunnerDataDisk(audit.Checks)
	dataDiskOK := !requiresDataDisk || strings.Contains(string(log), probeDataDiskMarker)
	passed := conclusion == "success" && commandOK && proofOK && redacted && !leaked && dataDiskOK
	checks := []Check{
		probeCheck("command_execution", commandOK, "Runner 已执行探针命令。", "Runner 未返回命令执行成功标记。"),
		probeCheck("probe_secret_injection", proofOK, "一次性探针值已安全注入；服务端仅验证 HMAC 证明。", "未收到有效的一次性探针 HMAC 证明。"),
		probeCheck("stream_log_redaction", redacted && !leaked, "探针故意输出的值已被流式日志脱敏，未发现明文。", "未能确认日志脱敏，或发现一次性探针值明文。"),
	}
	if requiresDataDisk {
		checks = append(checks, probeCheck(
			"runner_data_disk_boot",
			dataDiskOK,
			"Runner 已确认其非 OS 数据盘初始化后才启动并执行探针。",
			"Runner 未确认非 OS 数据盘初始化；临时实例将被回收。",
		))
	}
	summary := "Runner 探针通过；正在自动回收临时实例。"
	if !passed {
		summary = "Runner 探针未通过；正在自动回收临时实例。"
	}
	if err := s.Audits.AppendChecks(ctx, jobID, checks); err != nil {
		_ = s.Audits.MarkProbeFinished(ctx, jobID, false, "无法持久化探针检查结果；正在自动回收临时实例。")
		return
	}
	_ = s.Audits.MarkProbeFinished(ctx, jobID, passed, summary)
}

func (s *Service) createProbeRun(
	ctx context.Context,
	req Request,
	project *model.Project,
	repo *model.Repo,
	commitSHA string,
	pool autoscale.Pool,
) (*model.PipelineRun, uuid.UUID, error) {
	if commitSHA == "" {
		// A runner-config.yaml cannot normally exist without a commit, but a
		// non-empty sentinel keeps the internal no-checkout job schema valid.
		commitSHA = "runner-self-check"
	}
	os := poolOS(pool)
	job := pipeline.Job{
		Name:      "Runner 自检",
		RunsOn:    pipeline.StringList(pool.Labels),
		Resource:  poolTier(pool),
		Execution: &pipeline.Execution{Mode: pipeline.ExecutionModeIsolated, Pool: pool.Name},
		Env: map[string]string{
			"WULING_SELF_CHECK_KIND": "runner-probe-v1",
		},
		Steps: []pipeline.Step{{
			Name: "验证 Runner 环境与日志脱敏",
			Run:  probeScript(os),
		}},
	}
	if strings.TrimSpace(pool.RunnerDataDisk) != "" {
		job.Env["WULING_SELF_CHECK_REQUIRE_DATA_DISK"] = "1"
	}
	if os == model.OSLinux {
		// Python's stdlib gives a reliable HMAC implementation on minimal
		// Linux images without assuming openssl/coreutils are installed.
		job.Container = pipeline.Container{Image: "python:3.12-alpine"}
	}
	workflow := &pipeline.Workflow{
		Name: "系统 Runner 自检",
		On:   pipeline.Triggers{WorkflowDispatch: true},
		Jobs: map[string]pipeline.Job{"runner_probe": job},
	}
	run, err := s.Pipelines.CreateRun(ctx, pipelinestore.CreateRunParams{
		OrgID:         req.OrgID,
		ProjectID:     project.ID,
		RepoID:        repo.ID,
		WorkflowPath:  ".wuling/internal/runner-self-check.yml",
		Event:         "manual",
		GitRef:        repo.DefaultBranch,
		CommitSHA:     commitSHA,
		CommitMessage: "system runner self-check",
		TriggeredBy:   req.RequestedBy,
		Workflow:      workflow,
		DefaultTier:   poolTier(pool),
	})
	if err != nil {
		return nil, uuid.Nil, err
	}
	if len(run.Jobs) != 1 {
		_ = s.Pipelines.CancelRun(ctx, run.ID)
		return nil, uuid.Nil, fmt.Errorf("self-check pipeline has %d jobs, expected one", len(run.Jobs))
	}
	return run, run.Jobs[0].ID, nil
}

func newProbeSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func probeProof(secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(probeMessage))
	return hex.EncodeToString(mac.Sum(nil))
}

func probeScript(os string) string {
	if os == model.OSWindows {
		return `$ErrorActionPreference = 'Stop'
$secret = $env:WULING_SELF_CHECK_PROBE
if ([string]::IsNullOrWhiteSpace($secret)) { throw 'probe environment value is missing' }
if ($env:WULING_SELF_CHECK_REQUIRE_DATA_DISK -eq '1') {
  if ($env:WULING_RUNNER_DATA_DISK_READY -ne '1') { throw 'runner data-disk bootstrap marker is missing' }
  Write-Output 'WULING_SELF_CHECK_DATA_DISK=ok'
}
$hmac = [System.Security.Cryptography.HMACSHA256]::new([System.Text.Encoding]::UTF8.GetBytes($secret))
$proof = [Convert]::ToHexString($hmac.ComputeHash([System.Text.Encoding]::UTF8.GetBytes('wuling-runner-self-check/v1'))).ToLowerInvariant()
Write-Output 'WULING_SELF_CHECK_COMMAND=ok'
Write-Output ('WULING_SELF_CHECK_PROOF=' + $proof)
Write-Output ('WULING_SELF_CHECK_SECRET_ECHO=' + $secret)`
	}
	return `python3 - <<'PY'
import hashlib
import hmac
import os
import sys

secret = os.environ.get("WULING_SELF_CHECK_PROBE", "")
if not secret:
    raise SystemExit("probe environment value is missing")
if os.environ.get("WULING_SELF_CHECK_REQUIRE_DATA_DISK") == "1":
    if os.environ.get("WULING_RUNNER_DATA_DISK_READY") != "1":
        raise SystemExit("runner data-disk bootstrap marker is missing")
    print("WULING_SELF_CHECK_DATA_DISK=ok")
proof = hmac.new(secret.encode(), b"wuling-runner-self-check/v1", hashlib.sha256).hexdigest()
print("WULING_SELF_CHECK_COMMAND=ok")
print("WULING_SELF_CHECK_PROOF=" + proof)
print("WULING_SELF_CHECK_SECRET_ECHO=" + secret)
PY`
}

func poolOS(pool autoscale.Pool) string {
	if pool.OS == "" {
		return model.OSLinux
	}
	return pool.OS
}

func poolTier(pool autoscale.Pool) string {
	if pool.Tier == "" {
		return model.TierMedium
	}
	return pool.Tier
}

func probeCheck(name string, passed bool, passMessage, failMessage string) Check {
	if passed {
		return Check{Name: name, Status: CheckPassed, Message: passMessage}
	}
	return Check{Name: name, Status: CheckFailed, Message: failMessage}
}

func auditUsesRunnerDataDisk(raw []byte) bool {
	// The durable preflight check does not need to expose raw configuration.
	// Its message already reports whether the pool passed all data-disk
	// validation. poolPreflight (service.go) adds the exact check name.
	var checks []Check
	if err := json.Unmarshal(raw, &checks); err != nil {
		return false
	}
	for _, check := range checks {
		if check.Name == "runner_data_disk" {
			return true
		}
	}
	return false
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func isNotFound(err error) bool {
	if apiErr := apperr.As(err); apiErr != nil {
		return apiErr.Code == apperr.CodeNotFound
	}
	return false
}
