package runnercheck

import (
	"sync"

	"github.com/google/uuid"
)

// MemoryHistory keeps a short, process-local audit trail until durable audit
// storage is intentionally introduced. It is concurrency-safe and copies
// records on both write and read so callers cannot mutate retained results.
type MemoryHistory struct {
	mu    sync.RWMutex
	limit int
	byOrg map[uuid.UUID][]Result
}

// NewMemoryHistory constructs a bounded store. A non-positive limit falls back
// to 25 records per organization.
func NewMemoryHistory(limit int) *MemoryHistory {
	if limit <= 0 {
		limit = 25
	}
	return &MemoryHistory{
		limit: limit,
		byOrg: make(map[uuid.UUID][]Result),
	}
}

// Append retains the newest result first and evicts the oldest one past limit.
func (h *MemoryHistory) Append(orgID uuid.UUID, result Result) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	records := h.byOrg[orgID]
	records = append([]Result{cloneResult(result)}, records...)
	if len(records) > h.limit {
		records = records[:h.limit]
	}
	h.byOrg[orgID] = records
}

// List returns newest-first detached copies.
func (h *MemoryHistory) List(orgID uuid.UUID) []Result {
	if h == nil {
		return []Result{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	records := h.byOrg[orgID]
	out := make([]Result, 0, len(records))
	for _, record := range records {
		out = append(out, cloneResult(record))
	}
	return out
}

func cloneResult(in Result) Result {
	out := in
	out.Pools = make([]PoolCheck, len(in.Pools))
	for i, pool := range in.Pools {
		out.Pools[i] = pool
		out.Pools[i].Checks = append([]Check(nil), pool.Checks...)
	}
	return out
}
