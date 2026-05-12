// Package insightstore is the persistence layer for the Insights domain.
// It owns the commit index that the receive-pack hook populates, plus the
// pure-SQL aggregations behind /insights/activity, /insights/contributors,
// and the libgit2-backed /insights/languages walker.
//
// We deliberately do not import the HTTP layer here; errors come back as
// apperr.* wrapped values, the same convention as issuestore / mrstore.
package insightstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// MaxCommitsPerWalk caps how many commits a single index pass will look at
// per branch tip, so a push of a giant force-history can't pin a CPU.
const MaxCommitsPerWalk = 5000

// MaxLanguageBlobs caps the language-tree walk so an enormous monorepo
// can't blow the request budget. Anything past this is reported back via
// LanguageStats.Truncated so the client knows the totals are a lower bound.
const MaxLanguageBlobs = 10000

// Store is the data-access object for Insights.
type Store struct {
	pool *db.Pool
	log  *slog.Logger
}

// New returns a Store backed by pool. log is used for the async indexer's
// background errors (the request context is gone by the time it runs).
func New(pool *db.Pool, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	return &Store{pool: pool, log: log}
}

// ----------------------------------------------------------------------------
// Indexing (called from githttp + sshd on successful receive-pack)
// ----------------------------------------------------------------------------

// IndexAsync walks the repo's refs in a background goroutine and upserts
// commits into repo_commit_index. Safe to call from a request hot path —
// it never blocks the caller and bounds work via MaxCommitsPerWalk.
func (s *Store) IndexAsync(repoID uuid.UUID, repoPath string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := s.IndexNow(ctx, repoID, repoPath); err != nil {
			s.log.Warn("commit index failed",
				"repo_id", repoID, "path", repoPath, "err", err)
		}
	}()
}

// IndexNow walks the repo's refs and upserts commits synchronously. Used by
// tests and by callers that want to wait for the index to settle.
func (s *Store) IndexNow(ctx context.Context, repoID uuid.UUID, repoPath string) error {
	refs, err := git.ListRefs(repoPath)
	if err != nil {
		return fmt.Errorf("list refs: %w", err)
	}
	seen := make(map[string]struct{}, 256)
	var batch []model.ActivityDay // unused; use direct INSERTs below
	_ = batch

	type row struct {
		oid       string
		name      string
		email     string
		authorAt  time.Time
	}
	var rows []row

	for _, ref := range refs {
		// Only walk branch tips; tags often point at the same commits anyway
		// and walking them would just duplicate work.
		if !ref.IsBranch {
			continue
		}
		commits, err := git.Log(repoPath, ref.OID, MaxCommitsPerWalk)
		if err != nil {
			// Skip a bad ref instead of failing the whole index; a partial
			// index beats none.
			s.log.Debug("log ref failed; skipping",
				"repo_id", repoID, "ref", ref.Name, "err", err)
			continue
		}
		for _, c := range commits {
			if _, ok := seen[c.OID]; ok {
				continue
			}
			seen[c.OID] = struct{}{}
			rows = append(rows, row{
				oid:      c.OID,
				name:     c.Author.Name,
				email:    c.Author.Email,
				authorAt: c.Author.When,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}

	// Bulk-insert with ON CONFLICT DO NOTHING. We batch into 1000-row chunks
	// to keep the parameter count well under the 65535 pgx limit (4 params
	// per row = 4000 params per batch).
	const batchSize = 1000
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		var sb strings.Builder
		sb.WriteString("INSERT INTO repo_commit_index (repo_id, oid, author_name, author_email, author_time) VALUES ")
		args := make([]any, 0, (end-start)*4+1)
		args = append(args, repoID)
		for i, r := range rows[start:end] {
			if i > 0 {
				sb.WriteByte(',')
			}
			base := i*4 + 2 // $2..; $1 is repo_id
			fmt.Fprintf(&sb, "($1,$%d,$%d,$%d,$%d)", base, base+1, base+2, base+3)
			args = append(args, r.oid, r.name, r.email, r.authorAt)
		}
		sb.WriteString(" ON CONFLICT (repo_id, oid) DO NOTHING")
		if _, err := s.pool.Exec(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("bulk upsert: %w", err)
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Query: Activity (per-day rollup)
// ----------------------------------------------------------------------------

// Activity returns one row per UTC day in [now-since, now] with the per-day
// issue / MR / commit counts for every repo in the project.
func (s *Store) Activity(ctx context.Context, projectID uuid.UUID, since time.Duration) ([]model.ActivityDay, error) {
	if since <= 0 {
		since = 30 * 24 * time.Hour
	}
	now := time.Now().UTC()
	windowStart := now.Add(-since)
	// Truncated start is only used for prefilling the day buckets and the
	// index map — the SQL filter uses the precise windowStart so commits
	// from before windowStart on the same calendar day aren't included.
	truncatedStart := windowStart.Truncate(24 * time.Hour)

	// We pre-fill every day in the range so a quiet project still gets a
	// continuous timeline back, which is what the UI wants.
	days := make([]model.ActivityDay, 0, int(since/(24*time.Hour))+1)
	for d := truncatedStart; !d.After(now); d = d.Add(24 * time.Hour) {
		days = append(days, model.ActivityDay{Date: d.Format("2006-01-02")})
	}
	if len(days) == 0 {
		return days, nil
	}
	idx := make(map[string]*model.ActivityDay, len(days))
	for i := range days {
		idx[days[i].Date] = &days[i]
	}

	// Issues opened.
	if err := scanDayCounts(ctx, s.pool, `
		SELECT to_char(date_trunc('day', created_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD') AS d, COUNT(*)
		FROM issues
		WHERE project_id = $1 AND created_at >= $2
		GROUP BY d`, projectID, windowStart, idx, func(d *model.ActivityDay, n int64) { d.IssuesOpened = n }); err != nil {
		return nil, err
	}
	// Issues closed.
	if err := scanDayCounts(ctx, s.pool, `
		SELECT to_char(date_trunc('day', closed_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD') AS d, COUNT(*)
		FROM issues
		WHERE project_id = $1 AND closed_at IS NOT NULL AND closed_at >= $2
		GROUP BY d`, projectID, windowStart, idx, func(d *model.ActivityDay, n int64) { d.IssuesClosed = n }); err != nil {
		return nil, err
	}
	// MRs opened.
	if err := scanDayCounts(ctx, s.pool, `
		SELECT to_char(date_trunc('day', created_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD') AS d, COUNT(*)
		FROM merge_requests
		WHERE project_id = $1 AND created_at >= $2
		GROUP BY d`, projectID, windowStart, idx, func(d *model.ActivityDay, n int64) { d.MRsOpened = n }); err != nil {
		return nil, err
	}
	// MRs merged.
	if err := scanDayCounts(ctx, s.pool, `
		SELECT to_char(date_trunc('day', merged_at AT TIME ZONE 'UTC'), 'YYYY-MM-DD') AS d, COUNT(*)
		FROM merge_requests
		WHERE project_id = $1 AND merged_at IS NOT NULL AND merged_at >= $2
		GROUP BY d`, projectID, windowStart, idx, func(d *model.ActivityDay, n int64) { d.MRsMerged = n }); err != nil {
		return nil, err
	}
	// Commits — joined to repos so we get every repo in the project.
	if err := scanDayCounts(ctx, s.pool, `
		SELECT to_char(date_trunc('day', ci.author_time AT TIME ZONE 'UTC'), 'YYYY-MM-DD') AS d, COUNT(*)
		FROM repo_commit_index ci
		JOIN repos r ON r.id = ci.repo_id
		WHERE r.project_id = $1 AND ci.author_time >= $2
		GROUP BY d`, projectID, windowStart, idx, func(d *model.ActivityDay, n int64) { d.Commits = n }); err != nil {
		return nil, err
	}

	return days, nil
}

// scanDayCounts runs a (date, count) query and applies each row via assign.
func scanDayCounts(ctx context.Context, pool *db.Pool, sql string,
	projectID uuid.UUID, start time.Time,
	idx map[string]*model.ActivityDay,
	assign func(*model.ActivityDay, int64)) error {
	rows, err := pool.Query(ctx, sql, projectID, start)
	if err != nil {
		return apperr.Internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var date string
		var n int64
		if err := rows.Scan(&date, &n); err != nil {
			return apperr.Internal(err)
		}
		if d, ok := idx[date]; ok {
			assign(d, n)
		}
	}
	return rows.Err()
}

// ----------------------------------------------------------------------------
// Query: Contributors
// ----------------------------------------------------------------------------

// Contributors returns top contributors over the time window, scoped to one
// repo (resolved by the HTTP layer). limit is capped to 100.
func (s *Store) Contributors(ctx context.Context, repoID uuid.UUID, since time.Duration, limit int) ([]model.ContributorStat, error) {
	if since <= 0 {
		since = 30 * 24 * time.Hour
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	start := time.Now().UTC().Add(-since)
	rows, err := s.pool.Query(ctx, `
		SELECT LOWER(author_email) AS email, MIN(author_name) AS name, COUNT(*) AS commits
		FROM repo_commit_index
		WHERE repo_id = $1 AND author_time >= $2
		GROUP BY LOWER(author_email)
		ORDER BY commits DESC, email ASC
		LIMIT $3
	`, repoID, start, limit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.ContributorStat, 0)
	for rows.Next() {
		var c model.ContributorStat
		if err := rows.Scan(&c.Email, &c.Name, &c.Commits); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// Query: Language stats (libgit2-backed tree walk)
// ----------------------------------------------------------------------------

// Languages walks the tree at refSpec (defaulting to repoDefault) and sums
// bytes and file counts per language detected by extension. Returns empty
// maps for an empty repo. Recursion is capped at MaxLanguageBlobs.
func (s *Store) Languages(repoPath, refSpec, repoDefault string) (model.LanguageStats, error) {
	out := model.LanguageStats{
		Bytes: map[string]int64{},
		Files: map[string]int64{},
	}
	spec := refSpec
	if spec == "" {
		spec = repoDefault
	}
	if spec == "" {
		spec = "main"
	}
	oid, err := git.Resolve(repoPath, spec)
	if err != nil {
		if git.IsNotFound(err) {
			return out, apperr.NotFound("ref")
		}
		return out, apperr.Wrap(apperr.CodeInternal, "resolve", err)
	}
	visited := 0
	if err := walkTreeBlobs(repoPath, oid, "", &visited, &out); err != nil {
		return out, err
	}
	return out, nil
}

func walkTreeBlobs(repoPath, treeOID, prefix string, visited *int, out *model.LanguageStats) error {
	if *visited >= MaxLanguageBlobs {
		out.Truncated = true
		return nil
	}
	entries, err := git.ReadTree(repoPath, treeOID)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "read tree", err)
	}
	for _, e := range entries {
		if *visited >= MaxLanguageBlobs {
			out.Truncated = true
			return nil
		}
		full := e.Name
		if prefix != "" {
			full = prefix + "/" + e.Name
		}
		switch e.Kind {
		case "tree":
			if err := walkTreeBlobs(repoPath, e.OID, full, visited, out); err != nil {
				return err
			}
		case "blob":
			*visited++
			lang := LanguageFromFilename(full)
			if lang == "" {
				continue
			}
			// Pull the blob to count bytes. Cheap on libgit2 because the
			// object is already on disk and we don't read it into Go memory
			// for anything except its size.
			b, berr := git.ReadBlob(repoPath, e.OID)
			if berr != nil {
				return apperr.Wrap(apperr.CodeInternal, "read blob", berr)
			}
			if b.IsBinary {
				continue
			}
			out.Bytes[lang] += int64(len(b.Data))
			out.Files[lang]++
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// ParseSince accepts "7d", "30d", "12w", "1y", or a bare integer (seconds).
// Empty returns 30 days.
func ParseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 30 * 24 * time.Hour, nil
	}
	// Strip a trailing unit if present.
	unit := byte(0)
	if last := s[len(s)-1]; last < '0' || last > '9' {
		unit = last
		s = s[:len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, apperr.Validation("invalid 'since' duration", nil)
	}
	switch unit {
	case 0:
		return time.Duration(n) * time.Second, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	case 'y':
		// 365 days is good enough for charting; nobody cares about leap years
		// in an Insights window.
		return time.Duration(n) * 365 * 24 * time.Hour, nil
	default:
		return 0, apperr.Validation("'since' must end in d/w/y or be a number of seconds", nil)
	}
}

// SortedKeys is a small helper for the HTTP layer that wants deterministic
// JSON key ordering when iterating LanguageStats maps. Not strictly necessary
// (clients should treat objects as unordered) but useful for golden tests.
func SortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// guard against unused imports during incremental edits.
var _ = errors.New
