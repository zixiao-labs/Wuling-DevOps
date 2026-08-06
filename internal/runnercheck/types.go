// Package runnercheck provides the safe preflight and durable lifecycle for
// administrator-initiated runner/autoscaler self-checks. Real work is queued
// as an explicit-pool isolated job, so provisioning -> runner registration ->
// probe execution -> cleanup follows the same control-plane path as CI.
package runnercheck

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// LifecyclePhase identifies where a self-check is in its lifecycle.
type LifecyclePhase string

const (
	PhasePreflight  LifecyclePhase = "preflight"
	PhaseProvision  LifecyclePhase = "provision"
	PhaseWaitRunner LifecyclePhase = "wait_runner"
	PhaseExecute    LifecyclePhase = "execute"
	PhaseCleanup    LifecyclePhase = "cleanup"
)

// ExecutionState records a durable lifecycle result.
type ExecutionState string

const (
	StatePreflight        ExecutionState = "preflight"
	StateQueued           ExecutionState = "queued"
	StateProvisioning     ExecutionState = "provisioning"
	StateWaitingForRunner ExecutionState = "waiting_for_runner"
	StateExecuting        ExecutionState = "executing"
	StateCleaningUp       ExecutionState = "cleaning_up"
	StateCleanupPending   ExecutionState = "cleanup_pending"
	StateSucceeded        ExecutionState = "succeeded"
	StateFailed           ExecutionState = "failed"
	StateCleaned          ExecutionState = "cleaned"
	StateNotRun           ExecutionState = "not_run"
)

// CheckStatus is the outcome of one non-secret preflight assertion.
type CheckStatus string

const (
	CheckPassed      CheckStatus = "passed"
	CheckFailed      CheckStatus = "failed"
	CheckUnsupported CheckStatus = "unsupported"
	CheckError       CheckStatus = "error"
	CheckNotRun      CheckStatus = "not_run"
)

// Readiness says whether the preflight found enough local configuration to
// hand a pool to a future, explicitly enabled real probe lifecycle.
type Readiness string

const (
	ReadinessReady   Readiness = "ready"
	ReadinessBlocked Readiness = "blocked"
)

// Check is a metadata-only assertion. Messages must never include secret
// values or the raw runner-config.yaml content.
type Check struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
}

// PoolCheck is one selected autoscaler pool's local preflight result.
type PoolCheck struct {
	PoolName         string         `json:"pool_name"`
	Provider         string         `json:"provider,omitempty"`
	OS               string         `json:"os,omitempty"`
	Phase            LifecyclePhase `json:"phase"`
	State            ExecutionState `json:"state"`
	Readiness        Readiness      `json:"readiness"`
	Checks           []Check        `json:"checks"`
	RunnerProbeState ExecutionState `json:"runner_probe_state"`
	RunnerProbeNote  string         `json:"runner_probe_note"`
}

// Result is retained for the safe synchronous preflight API and tests. The
// real administrator surface returns durable AuditRecord values instead.
type Result struct {
	ID               string         `json:"id"`
	OrgSlug          string         `json:"org_slug"`
	RequestedAt      time.Time      `json:"requested_at"`
	RequestedBy      string         `json:"requested_by"`
	Storage          string         `json:"storage"`
	Retention        string         `json:"retention"`
	Phase            LifecyclePhase `json:"phase"`
	State            ExecutionState `json:"state"`
	RunnerProbeState ExecutionState `json:"runner_probe_state"`
	ConfigCheck      Check          `json:"config_check"`
	Pools            []PoolCheck    `json:"pools"`
}

// Request identifies the target organization and optional pool subset.
type Request struct {
	OrgID       uuid.UUID
	OrgSlug     string
	RequestedBy uuid.UUID
	PoolNames   []string
}

// RunnerProbePlan is retained as a narrow integration shape for alternative
// probe executors. Credential resolution must remain inside the provider
// implementation and must never be copied into Result or any HTTP response.
type RunnerProbePlan struct {
	OrgID    uuid.UUID
	PoolName string
	Provider string
	OS       string
}

// ProvisionedRunner is an opaque provisioned resource. It may contain a cloud
// instance ID, but no credentials or registration token.
type ProvisionedRunner struct {
	ExternalID string
	RunnerID   uuid.UUID
}

// ProbeExecution describes an alternative runner-side probe without exposing
// its command or output through the HTTP contract.
type ProbeExecution struct {
	Name    string
	Timeout time.Duration
}

// ProbeExecutionResult is reserved for an alternative runner-side executor.
type ProbeExecutionResult struct {
	Summary string
}

// RunnerProbeRunner is an optional extension point for probe executors outside
// the standard isolated pipeline-job lifecycle.
type RunnerProbeRunner interface {
	Provision(ctx context.Context, plan RunnerProbePlan) (ProvisionedRunner, error)
	WaitForRunner(ctx context.Context, runner ProvisionedRunner) error
	Execute(ctx context.Context, runner ProvisionedRunner, probe ProbeExecution) (ProbeExecutionResult, error)
	Cleanup(ctx context.Context, runner ProvisionedRunner) error
}
