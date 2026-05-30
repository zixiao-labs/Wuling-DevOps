package autoscale

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
	"github.com/zixiao-labs/wuling-devops/internal/secretstore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// ConfigFileName is the runner config blob read from each org's config repo.
const ConfigFileName = "runner-config.yaml"

// bootTimeout is how long an ephemeral runner may stay 'offline' (never
// checked in) before the autoscaler reaps it as a failed boot.
const bootTimeout = 5 * time.Minute

// Reconciler is the autoscaler control loop.
type Reconciler struct {
	Pipelines *pipelinestore.Store
	Runners   *runnerstore.Store
	Secrets   *secretstore.Store
	Users     *userstore.Store
	Layout    *repostore.Layout
	Log       *slog.Logger

	// ConfigProject/ConfigRepo locate each org's config repo.
	ConfigProject string
	ConfigRepo    string
	// ServerURL is injected into runner user-data (the control-plane origin).
	ServerURL string
	// DefaultIdleTimeout applies when runner-config.yaml omits idle_timeout.
	DefaultIdleTimeout time.Duration
	// Interval between reconcile passes.
	Interval time.Duration
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
	runners, err := r.Runners.ListForAutoscale(ctx, orgID)
	if err != nil {
		return err
	}

	pendingByPool := assignDemand(cfg.Pools, demand)
	runnersByPool := groupRunners(runners)

	for i := range cfg.Pools {
		pool := cfg.Pools[i]
		r.reconcilePool(ctx, orgID, cfg, pool, pendingByPool[pool.Name], runnersByPool[pool.Name], idleTimeout)
	}
	return nil
}

func (r *Reconciler) reconcilePool(
	ctx context.Context,
	orgID uuid.UUID,
	cfg *Config,
	pool Pool,
	pending int,
	poolRunners []runnerstore.AutoscaleRunner,
	idleTimeout time.Duration,
) {
	// Lazily build the provider only when this pool needs to act.
	var provider Provider
	getProvider := func() Provider {
		if provider != nil {
			return provider
		}
		creds, err := r.Secrets.GetOrgValue(ctx, orgID, pool.CredentialSecretName())
		if err != nil {
			r.Log.Warn("pool credentials unavailable", "pool", pool.Name, "err", err)
			return nil
		}
		p, err := NewProvider(pool, creds)
		if err != nil {
			r.Log.Warn("build provider failed", "pool", pool.Name, "err", err)
			return nil
		}
		provider = p
		return provider
	}

	now := time.Now()
	var idle, offline, busy int
	for _, rn := range poolRunners {
		switch rn.Status {
		case "idle":
			idle++
		case "busy":
			busy++
		default:
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
	if pool.Max > 0 && desired > pool.Max {
		desired = pool.Max
	}
	toLaunch := desired - inFlight
	for k := 0; k < toLaunch; k++ {
		p := getProvider()
		if p == nil {
			break
		}
		if err := r.launchOne(ctx, orgID, cfg, pool, p); err != nil {
			r.Log.Warn("launch failed", "pool", pool.Name, "err", err)
			break
		}
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
		if rn.ExternalID != "" {
			if err := p.Terminate(ctx, rn.ExternalID); err != nil {
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
	runner, err := r.Runners.CreateEphemeralRunner(ctx, orgID, "", pool.Labels, pool.Tier, pool.Provider, pool.Name)
	if err != nil {
		return err
	}
	userData := BuildUserData(r.ServerURL, runner.Token, pool, runner.Name)
	inst, err := p.Launch(ctx, LaunchSpec{
		Pool:       pool,
		TierSpec:   cfg.TierSpecFor(pool.Tier),
		RunnerName: runner.Name,
		UserData:   userData,
	})
	if err != nil {
		_ = r.Runners.Delete(ctx, orgID, runner.ID)
		return err
	}
	if err := r.Runners.SetExternalID(ctx, runner.ID, inst.ExternalID); err != nil {
		return err
	}
	return nil
}

// loadOrgConfig reads + parses an org's runner-config.yaml from its config
// repo. Returns (nil, nil) when the org has no config repo / file (i.e. it
// hasn't opted into autoscaling), and an error only on a real parse/IO fault.
func (r *Reconciler) loadOrgConfig(ctx context.Context, orgID uuid.UUID) (*Config, error) {
	project, err := r.Users.GetProjectBySlug(ctx, orgID, r.ConfigProject)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	repo, err := r.Users.GetRepoBySlug(ctx, project.ID, r.ConfigRepo)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	repoPath := r.Layout.Path(orgID, project.ID, repo.ID)
	sha, err := git.Resolve(repoPath, repo.DefaultBranch)
	if err != nil {
		if git.IsNotFound(err) {
			return nil, nil // empty repo
		}
		return nil, err
	}
	entries, err := git.ReadTree(repoPath, sha)
	if err != nil {
		return nil, err
	}
	var blobOID string
	for _, e := range entries {
		if e.Kind == "blob" && e.Name == ConfigFileName {
			blobOID = e.OID
			break
		}
	}
	if blobOID == "" {
		return nil, nil // repo exists but no runner-config.yaml
	}
	blob, err := git.ReadBlob(repoPath, blobOID)
	if err != nil {
		return nil, err
	}
	return Parse(blob.Data)
}

// ---- helpers ---------------------------------------------------------------

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

func isNotFound(err error) bool {
	if ae := apperr.As(err); ae != nil {
		return ae.Code == apperr.CodeNotFound
	}
	return false
}
