package autoscale

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/orgconfig"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
	"github.com/zixiao-labs/wuling-devops/internal/secretstore"
)

// ConfigFileName is the runner config blob read from each org's config repo.
const ConfigFileName = "runner-config.yaml"

// bootTimeout is how long a newly provisioned VM may wait to register and
// acquire its reserved isolated job before the autoscaler reaps it. Regular
// ephemeral pools use the same value for an offline never-checked-in runner.
const bootTimeout = 5 * time.Minute

// IsolatedLifecycle observes the cloud-resource lifecycle of selected
// per-job runners. It is intentionally defined in autoscale rather than
// importing the administrator self-check package, so ordinary isolated jobs
// retain no dependency on that feature. Implementations must treat unknown
// job IDs as a no-op.
type IsolatedLifecycle interface {
	MarkIsolatedProvisioned(ctx context.Context, jobID, runnerID uuid.UUID, externalID string) error
	MarkIsolatedProvisioningFailure(ctx context.Context, jobID uuid.UUID, summary string) error
	MarkIsolatedCleanupPending(ctx context.Context, jobID uuid.UUID, summary string, next time.Time) error
	MarkIsolatedCleaned(ctx context.Context, jobID uuid.UUID) error
}

// Reconciler is the autoscaler control loop.
type Reconciler struct {
	Pipelines *pipelinestore.Store
	Runners   *runnerstore.Store
	Secrets   *secretstore.Store
	Log       *slog.Logger
	// IsolatedLifecycle is optional; the administrator self-check audit store
	// uses it to make VM provisioning and cleanup observable across restarts.
	IsolatedLifecycle IsolatedLifecycle

	// OrgConfig reads each org's runner-config.yaml from its config repo.
	OrgConfig *orgconfig.Store
	// ServerURL is injected into runner user-data (the control-plane origin).
	ServerURL string
	// DefaultIdleTimeout applies when runner-config.yaml omits idle_timeout.
	DefaultIdleTimeout time.Duration
	// Interval between reconcile passes.
	Interval time.Duration

	// LaunchBackoff is the cooldown applied to a pool after a failed launch,
	// doubled per consecutive failure up to MaxLaunchBackoff.
	LaunchBackoff    time.Duration // default 1m
	MaxLaunchBackoff time.Duration // default 15m

	mu       sync.Mutex
	cooldown map[poolKey]coolState
}

type poolKey struct {
	Org  uuid.UUID
	Pool string
}

type coolState struct {
	Until   time.Time
	Strikes int
}

func (r *Reconciler) launchBackoff() time.Duration {
	if r.LaunchBackoff > 0 {
		return r.LaunchBackoff
	}
	return time.Minute
}

func (r *Reconciler) maxLaunchBackoff() time.Duration {
	if r.MaxLaunchBackoff > 0 {
		return r.MaxLaunchBackoff
	}
	return 15 * time.Minute
}

func (r *Reconciler) poolReady(k poolKey, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cooldown == nil {
		return true
	}
	st, ok := r.cooldown[k]
	return !ok || !now.Before(st.Until)
}

func (r *Reconciler) penalizeLaunch(k poolKey, now time.Time, outOfCapacity bool) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cooldown == nil {
		r.cooldown = map[poolKey]coolState{}
	}
	st := r.cooldown[k]
	st.Strikes++
	var d time.Duration
	if outOfCapacity {
		d = r.maxLaunchBackoff()
	} else {
		d = r.launchBackoff() << (st.Strikes - 1)
		if d > r.maxLaunchBackoff() {
			d = r.maxLaunchBackoff()
		}
	}
	st.Until = now.Add(d)
	r.cooldown[k] = st
	return d
}

func (r *Reconciler) clearLaunchPenalty(k poolKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cooldown == nil {
		return
	}
	delete(r.cooldown, k)
}

func (r *Reconciler) markIsolatedProvisioned(ctx context.Context, jobID, runnerID uuid.UUID, externalID string) {
	if r.IsolatedLifecycle == nil {
		return
	}
	if err := r.IsolatedLifecycle.MarkIsolatedProvisioned(ctx, jobID, runnerID, externalID); err != nil {
		r.Log.Warn("record isolated runner provisioning lifecycle failed", "job", jobID, "err", err)
	}
}

func (r *Reconciler) markIsolatedProvisioningFailure(ctx context.Context, jobID uuid.UUID) {
	if r.IsolatedLifecycle == nil {
		return
	}
	if err := r.IsolatedLifecycle.MarkIsolatedProvisioningFailure(
		ctx, jobID, "云实例未能完成创建；autoscaler 将按退避策略重试。",
	); err != nil {
		r.Log.Warn("record isolated runner provisioning failure failed", "job", jobID, "err", err)
	}
}

func (r *Reconciler) markIsolatedCleanupPending(ctx context.Context, jobID uuid.UUID) {
	if r.IsolatedLifecycle == nil {
		return
	}
	if err := r.IsolatedLifecycle.MarkIsolatedCleanupPending(
		ctx, jobID, "临时实例清理未完成；autoscaler 将重试，并继续将其计入池容量。", time.Now().Add(time.Minute),
	); err != nil {
		r.Log.Warn("record isolated runner cleanup retry failed", "job", jobID, "err", err)
	}
}

func (r *Reconciler) markIsolatedCleaned(ctx context.Context, jobID uuid.UUID) {
	if r.IsolatedLifecycle == nil {
		return
	}
	if err := r.IsolatedLifecycle.MarkIsolatedCleaned(ctx, jobID); err != nil {
		r.Log.Warn("record isolated runner cleanup completion failed", "job", jobID, "err", err)
	}
}

// Run drives the reconcile loop until ctx is canceled.
func (r *Reconciler) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = 20 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	r.Log.Info("autoscaler started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			r.Log.Info("autoscaler stopped")
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) {
	orgs, err := r.orgsToReconcile(ctx)
	if err != nil {
		r.Log.Warn("list orgs to reconcile failed", "err", err)
		return
	}
	for _, orgID := range orgs {
		if err := r.reconcileOrg(ctx, orgID); err != nil {
			r.Log.Warn("reconcile org failed", "org_id", orgID, "err", err)
		}
	}
}

// orgsToReconcile unions orgs with queued jobs and orgs holding ephemeral
// runners (the latter must be visited to scale down once idle).
func (r *Reconciler) orgsToReconcile(ctx context.Context) ([]uuid.UUID, error) {
	set := map[uuid.UUID]struct{}{}
	queued, err := r.Pipelines.OrgsWithQueuedJobs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range queued {
		set[id] = struct{}{}
	}
	withRunners, err := r.Runners.OrgsWithEphemeralRunners(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range withRunners {
		set[id] = struct{}{}
	}
	out := make([]uuid.UUID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out, nil
}

func (r *Reconciler) reconcileOrg(ctx context.Context, orgID uuid.UUID) error {
	cfg, err := r.loadOrgConfig(ctx, orgID)
	if err != nil {
		return err
	}
	if cfg == nil {
		// No config repo / no runner-config.yaml: nothing to autoscale.
		return nil
	}
	idleTimeout := cfg.IdleTimeoutOr(r.DefaultIdleTimeout)

	demand, err := r.Pipelines.QueuedDemand(ctx, orgID)
	if err != nil {
		return err
	}
	isolatedDemand, err := r.Pipelines.QueuedIsolatedDemand(ctx, orgID)
	if err != nil {
		return err
	}
	runners, err := r.Runners.ListForAutoscale(ctx, orgID)
	if err != nil {
		return err
	}

	pendingByPool := assignDemand(cfg.Pools, demand)
	isolatedByPool := groupIsolatedDemand(isolatedDemand)
	runnersByPool := groupRunners(runners)

	for i := range cfg.Pools {
		pool := cfg.Pools[i]
		remainingIsolated, launchedIsolated := r.reconcileIsolatedPool(
			ctx, orgID, cfg, pool, runnersByPool[pool.Name], isolatedByPool[pool.Name],
		)
		r.reconcilePool(
			ctx, orgID, cfg, pool, pendingByPool[pool.Name],
			sharedPoolRunners(runnersByPool[pool.Name]), idleTimeout,
			remainingIsolated+launchedIsolated,
		)
	}
	return nil
}

// providerForPool resolves the organization Secret only when reconciliation
// needs to make a cloud call. The caller must never include the resolved values
// in an error returned to the user or in logs.
func (r *Reconciler) providerForPool(ctx context.Context, orgID uuid.UUID, pool Pool) (Provider, error) {
	creds, err := r.Secrets.GetOrgValue(ctx, orgID, pool.CredentialSecretName())
	if err != nil {
		return nil, err
	}
	secrets := ProviderSecrets{Credentials: creds}
	if pwdName := pool.PasswordSecretName(); pwdName != "" {
		pwd, err := r.Secrets.GetOrgValue(ctx, orgID, pwdName)
		if err != nil {
			return nil, err
		}
		secrets.WindowsPassword = pwd
	}
	return NewProvider(pool, secrets)
}

// reconcileIsolatedPool gives every queued isolated job a brand-new VM in its
// explicitly named pool. Existing isolated runners are excluded from generic
// capacity, and terminal/failed boots are released immediately rather than
// waiting for the normal idle timeout.
//
// It returns the count of existing isolated VMs still consuming pool capacity
// plus the number created in this pass; reconcilePool uses that total to honor
// pool.max while maintaining a separate shared warm floor.
func (r *Reconciler) reconcileIsolatedPool(
	ctx context.Context,
	orgID uuid.UUID,
	cfg *Config,
	pool Pool,
	poolRunners []runnerstore.AutoscaleRunner,
	demand []pipelinestore.IsolatedJobDemand,
) (remaining, launched int) {
	now := time.Now()
	pk := poolKey{Org: orgID, Pool: pool.Name}
	var provider Provider
	getProvider := func() Provider {
		if provider != nil {
			return provider
		}
		p, err := r.providerForPool(ctx, orgID, pool)
		if err != nil {
			r.Log.Warn("build provider for isolated runner failed", "pool", pool.Name, "err", err)
			return nil
		}
		provider = p
		return provider
	}

	// All existing rows count toward pool.max until cleanup succeeds. A failed
	// deletion deliberately remains counted so repeated provider errors cannot
	// cause an unbounded billed-VM leak.
	inFlight := len(poolRunners)
	for _, rn := range poolRunners {
		if rn.IsolatedJobID == nil {
			continue
		}

		job, err := r.Pipelines.GetIsolatedJobStatus(ctx, *rn.IsolatedJobID)
		if err != nil && !isNotFoundError(err) {
			r.Log.Warn("read isolated job state failed", "runner", rn.ID, "job", *rn.IsolatedJobID, "err", err)
			remaining++
			continue
		}

		releaseReservation := false
		reason := ""
		switch {
		case err != nil:
			reason = "orphaned-job"
		case job.Terminal():
			reason = "job-terminal"
		case job.ReservedRunnerID == nil || *job.ReservedRunnerID != rn.ID:
			// A reservation was explicitly released or rebound. Never leave
			// its old VM able to receive shared work.
			reason = "reservation-released"
		case job.Status == "queued" && now.Sub(rn.CreatedAt) > bootTimeout:
			// Registration alone is not success: an isolated VM which checks
			// in but never acquires its one reserved job would otherwise stay
			// idle and bill forever. Bound the entire provision-to-acquire
			// phase, then retry on a brand-new VM.
			reason = "acquire-timeout"
			releaseReservation = true
		}
		if reason == "" {
			remaining++
			continue
		}
		if reason == "acquire-timeout" {
			r.markIsolatedProvisioningFailure(ctx, *rn.IsolatedJobID)
		}

		externalID := rn.ExternalID
		if externalID == "" {
			p := getProvider()
			if p == nil {
				remaining++
				continue
			}
			recovered, ok := r.resolveExternalID(ctx, p, rn.ID)
			if !ok {
				// Tag lookup failed; keep the row so a later reconcile can retry
				// instead of deleting the only durable handle on a live VM.
				remaining++
				continue
			}
			externalID = recovered
		}
		if externalID != "" {
			p := getProvider()
			if p == nil {
				remaining++
				continue
			}
			if err := p.Terminate(ctx, externalID); err != nil {
				r.Log.Warn("terminate isolated runner failed; cleanup will retry",
					"pool", pool.Name, "runner", rn.ID, "external_id", externalID, "reason", reason, "err", err)
				r.markIsolatedCleanupPending(ctx, *rn.IsolatedJobID)
				remaining++
				continue
			}
		}
		if releaseReservation {
			if _, err := r.Pipelines.ReleaseIsolatedReservation(ctx, *rn.IsolatedJobID, rn.ID); err != nil {
				r.Log.Warn("release isolated reservation failed; cleanup will retry",
					"pool", pool.Name, "runner", rn.ID, "job", *rn.IsolatedJobID, "err", err)
				r.markIsolatedCleanupPending(ctx, *rn.IsolatedJobID)
				remaining++
				continue
			}
		}
		if err := r.Runners.Delete(ctx, orgID, rn.ID); err != nil {
			r.Log.Warn("delete isolated runner row failed; cleanup will retry",
				"pool", pool.Name, "runner", rn.ID, "reason", reason, "err", err)
			r.markIsolatedCleanupPending(ctx, *rn.IsolatedJobID)
			remaining++
			continue
		}
		inFlight--
		if releaseReservation {
			// An acquire timeout re-queues the same job for a fresh VM. In
			// particular, a self-check's one-time probe value must remain
			// available to that retry; marking its audit cleaned here would
			// shred it and make the next reserved runner fail at acquire.
			r.Log.Info("released isolated runner for retry", "pool", pool.Name, "runner", rn.Name, "reason", reason)
			continue
		}
		r.markIsolatedCleaned(ctx, *rn.IsolatedJobID)
		r.Log.Info("released isolated runner", "pool", pool.Name, "runner", rn.Name, "reason", reason)
	}

	for _, job := range demand {
		if pool.Max > 0 && inFlight >= pool.Max {
			break
		}
		if pool.Tier != "" && job.Tier != "" && pool.Tier != job.Tier {
			r.Log.Warn("isolated job pool tier does not match job tier",
				"pool", pool.Name, "job", job.JobID, "pool_tier", pool.Tier, "job_tier", job.Tier)
			continue
		}
		if !labelsSatisfied(pool.Labels, job.RunsOn) {
			r.Log.Warn("isolated job pool labels do not satisfy job runs-on",
				"pool", pool.Name, "job", job.JobID, "runs_on", job.RunsOn)
			continue
		}
		if !r.poolReady(pk, now) {
			break
		}
		p := getProvider()
		if p == nil {
			break
		}
		created, err := r.launchIsolatedOne(ctx, orgID, cfg, pool, p, job.JobID)
		if err != nil {
			r.markIsolatedProvisioningFailure(ctx, job.JobID)
			cd := r.penalizeLaunch(pk, now, IsAliyunOutOfCapacity(err))
			r.Log.Warn("launch isolated runner failed", "pool", pool.Name, "job", job.JobID, "err", err, "cooldown", cd)
			break
		}
		if !created {
			// The job changed state between queued-demand discovery and the
			// reservation transaction. No VM was started, so move on.
			continue
		}
		r.clearLaunchPenalty(pk)
		inFlight++
		launched++
		r.Log.Info("launched isolated runner", "pool", pool.Name, "provider", pool.Provider, "job", job.JobID)
	}
	return remaining, launched
}

// launchIsolatedOne persists a runner/job binding before starting a VM. That
// ordering is the safety boundary: an instance can only register and acquire
// its one reserved job, never unrelated queued work.
func (r *Reconciler) launchIsolatedOne(
	ctx context.Context,
	orgID uuid.UUID,
	cfg *Config,
	pool Pool,
	p Provider,
	jobID uuid.UUID,
) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	runner, err := r.Runners.CreateIsolatedEphemeralRunner(
		ctx, orgID, "", pool.Labels, pool.Tier, pool.Provider, pool.Name, pool.OS, jobID,
	)
	if err != nil {
		return false, err
	}
	reserved, err := r.Pipelines.ReserveIsolatedJob(ctx, jobID, runner.ID)
	if err != nil {
		_ = r.Runners.Delete(ctx, orgID, runner.ID)
		return false, err
	}
	if !reserved {
		_ = r.Runners.Delete(ctx, orgID, runner.ID)
		return false, nil
	}

	tier := cfg.TierSpecFor(pool.Tier)
	userData := BuildUserDataForPool(r.ServerURL, runner.Token, pool, tier, runner.Name)
	inst, err := p.Launch(ctx, LaunchSpec{
		Pool:          pool,
		TierSpec:      tier,
		RunnerName:    runner.Name,
		UserData:      userData,
		OrgID:         orgID,
		RunnerID:      runner.ID,
		IsolatedJobID: jobID,
	})
	if err != nil {
		_, _ = r.Pipelines.ReleaseIsolatedReservation(ctx, jobID, runner.ID)
		_ = r.Runners.Delete(ctx, orgID, runner.ID)
		return false, err
	}
	if err := r.persistExternalIDOrRollback(ctx, orgID, pool, p, runner.ID, inst.ExternalID, &jobID); err != nil {
		return false, err
	}
	r.markIsolatedProvisioned(ctx, jobID, runner.ID, inst.ExternalID)
	return true, nil
}

func (r *Reconciler) reconcilePool(
	ctx context.Context,
	orgID uuid.UUID,
	cfg *Config,
	pool Pool,
	pending int,
	poolRunners []runnerstore.AutoscaleRunner,
	idleTimeout time.Duration,
	isolatedCapacity int,
) {
	// Lazily build the provider only when this pool needs to act.
	var provider Provider
	getProvider := func() Provider {
		if provider != nil {
			return provider
		}
		p, err := r.providerForPool(ctx, orgID, pool)
		if err != nil {
			r.Log.Warn("build provider failed", "pool", pool.Name, "err", err)
			return nil
		}
		provider = p
		return provider
	}

	pk := poolKey{Org: orgID, Pool: pool.Name}

	now := time.Now()
	var idle, offline, busy int
	for _, rn := range poolRunners {
		switch rn.Status {
		case "idle":
			idle++
		case "busy":
			busy++
		default:
			// An offline runner that never checked in past the boot window is
			// about to be reaped below; don't count it as live capacity or the
			// needed-launch math skips a launch we actually need.
			if rn.LastSeenAt == nil && now.Sub(rn.CreatedAt) > bootTimeout {
				continue
			}
			offline++
		}
	}
	inFlight := len(poolRunners)

	// --- scale up -----------------------------------------------------------
	// Jobs not already covered by an idle or still-booting runner.
	needed := pending - (idle + offline)
	desired := inFlight
	if needed > 0 {
		desired = inFlight + needed
	}
	if desired < pool.Min {
		desired = pool.Min
	}
	if pool.Max > 0 {
		availableForShared := pool.Max - isolatedCapacity
		if availableForShared < 0 {
			availableForShared = 0
		}
		if desired > availableForShared {
			desired = availableForShared
		}
	}
	toLaunch := desired - inFlight
	for k := 0; k < toLaunch; k++ {
		if !r.poolReady(pk, now) {
			break
		}
		p := getProvider()
		if p == nil {
			break
		}
		if err := r.launchOne(ctx, orgID, cfg, pool, p); err != nil {
			cd := r.penalizeLaunch(pk, now, IsAliyunOutOfCapacity(err))
			r.Log.Warn("launch failed", "pool", pool.Name, "err", err, "cooldown", cd)
			break
		}
		r.clearLaunchPenalty(pk)
		r.Log.Info("launched runner", "pool", pool.Name, "provider", pool.Provider)
	}

	// --- scale down + boot reap --------------------------------------------
	// Never drop below min. Count how many we may remove.
	removable := inFlight - pool.Min
	for _, rn := range poolRunners {
		if removable <= 0 {
			break
		}
		reason := ""
		switch {
		case rn.Status == "idle" && idleSince(rn, now) > idleTimeout:
			reason = "idle"
		case rn.Status == "offline" && now.Sub(rn.CreatedAt) > bootTimeout && rn.LastSeenAt == nil:
			reason = "boot-timeout"
		}
		if reason == "" {
			continue
		}
		p := getProvider()
		if p == nil {
			break
		}
		externalID := rn.ExternalID
		if externalID == "" {
			recovered, ok := r.resolveExternalID(ctx, p, rn.ID)
			if !ok {
				continue
			}
			externalID = recovered
		}
		if externalID != "" {
			if err := p.Terminate(ctx, externalID); err != nil {
				r.Log.Warn("terminate failed", "pool", pool.Name, "runner", rn.ID, "err", err)
				continue
			}
		}
		if err := r.Runners.Delete(ctx, orgID, rn.ID); err != nil {
			r.Log.Warn("delete runner row failed", "runner", rn.ID, "err", err)
			continue
		}
		removable--
		r.Log.Info("released runner", "pool", pool.Name, "runner", rn.Name, "reason", reason)
	}
}

// launchOne pre-provisions a runner row + token, builds user-data, and asks
// the provider to start a VM. On provider failure the half-created row is
// removed so it doesn't linger as phantom capacity.
func (r *Reconciler) launchOne(ctx context.Context, orgID uuid.UUID, cfg *Config, pool Pool, p Provider) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	runner, err := r.Runners.CreateEphemeralRunner(ctx, orgID, "", pool.Labels, pool.Tier, pool.Provider, pool.Name, pool.OS)
	if err != nil {
		return err
	}
	tier := cfg.TierSpecFor(pool.Tier)
	userData := BuildUserDataForPool(r.ServerURL, runner.Token, pool, tier, runner.Name)
	inst, err := p.Launch(ctx, LaunchSpec{
		Pool:       pool,
		TierSpec:   tier,
		RunnerName: runner.Name,
		UserData:   userData,
		OrgID:      orgID,
		RunnerID:   runner.ID,
	})
	if err != nil {
		_ = r.Runners.Delete(ctx, orgID, runner.ID)
		return err
	}
	return r.persistExternalIDOrRollback(ctx, orgID, pool, p, runner.ID, inst.ExternalID, nil)
}

// persistExternalIDOrRollback stores the cloud instance id. When that write
// fails it terminates the just-launched VM; if termination also fails, the
// runner row is retained so a later reconcile can recover the id via
// RunnerInstanceFinder instead of leaking a billed instance.
func (r *Reconciler) persistExternalIDOrRollback(
	ctx context.Context,
	orgID uuid.UUID,
	pool Pool,
	p Provider,
	runnerID uuid.UUID,
	externalID string,
	isolatedJobID *uuid.UUID,
) error {
	if err := r.Runners.SetExternalID(ctx, runnerID, externalID); err != nil {
		if terr := p.Terminate(ctx, externalID); terr != nil {
			r.Log.Warn("rollback terminate failed after set-external-id error; retaining runner for recovery",
				"pool", pool.Name, "runner", runnerID, "external_id", externalID, "err", terr)
			return err
		}
		if isolatedJobID != nil {
			_, _ = r.Pipelines.ReleaseIsolatedReservation(ctx, *isolatedJobID, runnerID)
		}
		_ = r.Runners.Delete(ctx, orgID, runnerID)
		return err
	}
	return nil
}

// resolveExternalID recovers a missing provider instance id from immutable
// runner tags. ok=false means the lookup itself failed and the caller should
// retry later rather than delete the only durable handle on a possibly live VM.
// ok=true with an empty string means no instance was found (safe to drop).
func (r *Reconciler) resolveExternalID(ctx context.Context, p Provider, runnerID uuid.UUID) (string, bool) {
	finder, ok := p.(RunnerInstanceFinder)
	if !ok {
		return "", true
	}
	inst, found, err := finder.FindRunnerInstance(ctx, runnerID)
	if err != nil {
		r.Log.Warn("recover runner instance id failed", "runner", runnerID, "err", err)
		return "", false
	}
	if !found || inst.ExternalID == "" {
		return "", true
	}
	if err := r.Runners.SetExternalID(ctx, runnerID, inst.ExternalID); err != nil {
		r.Log.Warn("persist recovered runner instance id failed",
			"runner", runnerID, "external_id", inst.ExternalID, "err", err)
	}
	return inst.ExternalID, true
}

// loadOrgConfig reads + parses an org's runner-config.yaml from its config
// repo. Returns (nil, nil) when the org has no config repo / file (i.e. it
// hasn't opted into autoscaling), and an error only on a real parse/IO fault.
func (r *Reconciler) loadOrgConfig(ctx context.Context, orgID uuid.UUID) (*Config, error) {
	if r.OrgConfig == nil {
		return nil, nil
	}
	f, err := r.OrgConfig.Read(ctx, orgID, orgconfig.RunnerConfigPath)
	if err != nil {
		return nil, err
	}
	if !f.Exists() {
		return nil, nil
	}
	return Parse(f.Content)
}

// ---- helpers ---------------------------------------------------------------

func isNotFoundError(err error) bool {
	if apiErr := apperr.As(err); apiErr != nil {
		return apiErr.Code == apperr.CodeNotFound
	}
	return false
}

// assignDemand greedily assigns each queued job to the first pool (in config
// order) whose tier matches and whose labels are a superset of the job's
// runs-on, returning per-pool pending counts. First-match prevents one job
// from inflating several overlapping pools.
func assignDemand(pools []Pool, demand []pipelinestore.QueuedJob) map[string]int {
	out := map[string]int{}
	for _, job := range demand {
		for i := range pools {
			p := pools[i]
			if p.Tier != "" && p.Tier != job.Tier {
				continue
			}
			if !labelsSatisfied(p.Labels, job.RunsOn) {
				continue
			}
			out[p.Name]++
			break
		}
	}
	return out
}

func groupRunners(runners []runnerstore.AutoscaleRunner) map[string][]runnerstore.AutoscaleRunner {
	out := map[string][]runnerstore.AutoscaleRunner{}
	for _, rn := range runners {
		out[rn.PoolName] = append(out[rn.PoolName], rn)
	}
	return out
}

func groupIsolatedDemand(demand []pipelinestore.IsolatedJobDemand) map[string][]pipelinestore.IsolatedJobDemand {
	out := map[string][]pipelinestore.IsolatedJobDemand{}
	for _, job := range demand {
		out[job.Pool] = append(out[job.Pool], job)
	}
	return out
}

// sharedPoolRunners removes per-job reserved machines from ordinary
// scale-up/down math. An isolated VM is never a warm Runner, even while it is
// idle between boot and its one assigned acquire request.
func sharedPoolRunners(runners []runnerstore.AutoscaleRunner) []runnerstore.AutoscaleRunner {
	out := make([]runnerstore.AutoscaleRunner, 0, len(runners))
	for _, runner := range runners {
		if runner.IsolatedJobID == nil {
			out = append(out, runner)
		}
	}
	return out
}

// labelsSatisfied reports whether every required label is offered by the pool.
func labelsSatisfied(poolLabels, required []string) bool {
	have := make(map[string]struct{}, len(poolLabels))
	for _, l := range poolLabels {
		have[l] = struct{}{}
	}
	for _, want := range required {
		if _, ok := have[want]; !ok {
			return false
		}
	}
	return true
}

func idleSince(rn runnerstore.AutoscaleRunner, now time.Time) time.Duration {
	since := rn.CreatedAt
	if rn.LastJobAt != nil {
		since = *rn.LastJobAt
	}
	return now.Sub(since)
}
