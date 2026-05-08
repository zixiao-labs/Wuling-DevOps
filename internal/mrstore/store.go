// Package mrstore is the persistence layer for the Merge Requests domain:
// merge requests, comments, and reviews. It mirrors issuestore's shape so the
// HTTP layer stays parallel between Issues and MRs.
//
// Like issuestore it never imports the HTTP layer and returns apperr-wrapped
// errors. The libgit2 wrapper is invoked at the HTTP layer (mrhttp), not
// here — this package is database-only.
package mrstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// Store is the data-access object for the MR domain.
type Store struct{ pool *db.Pool }

// New returns a Store backed by pool.
func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// ----------------------------------------------------------------------------
// Merge requests
// ----------------------------------------------------------------------------

// CreateMRParams holds inputs to CreateMR. The caller is responsible for
// having resolved source/target refs to OIDs via libgit2 already; this layer
// just records the snapshot.
type CreateMRParams struct {
	RepoID          uuid.UUID
	ProjectID       uuid.UUID
	AuthorID        uuid.UUID
	Title           string
	Body            string
	SourceRef       string
	TargetRef       string
	SourceOIDAtOpen string
	TargetOIDAtOpen string
}

// CreateMR inserts a merge request, allocating the next per-project number
// atomically via mr_number_seq inside the same transaction. State starts at
// 'open'.
func (s *Store) CreateMR(ctx context.Context, p CreateMRParams) (*model.MergeRequest, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, apperr.Validation("title cannot be empty", nil)
	}
	if p.SourceRef == p.TargetRef {
		return nil, apperr.Validation("source_ref and target_ref must differ", nil)
	}
	if len(p.SourceOIDAtOpen) != 40 || len(p.TargetOIDAtOpen) != 40 {
		return nil, apperr.Validation("source/target OIDs must be 40-char hex", nil)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Allocate the next MR number for this project. Same UPSERT pattern as
	// issue_number_seq — concurrent inserts serialise on the row lock.
	var number int64
	err = tx.QueryRow(ctx, `
		INSERT INTO mr_number_seq (project_id, next_value)
		VALUES ($1, 2)
		ON CONFLICT (project_id) DO UPDATE
		   SET next_value = mr_number_seq.next_value + 1
		RETURNING next_value - 1
	`, p.ProjectID).Scan(&number)
	if err != nil {
		return nil, mapInsertErr(err, "MR number")
	}

	mr := &model.MergeRequest{
		ID:              uuid.New(),
		RepoID:          p.RepoID,
		ProjectID:       p.ProjectID,
		Number:          number,
		Title:           strings.TrimSpace(p.Title),
		Body:            p.Body,
		State:           "open",
		SourceRef:       p.SourceRef,
		TargetRef:       p.TargetRef,
		SourceOIDAtOpen: p.SourceOIDAtOpen,
		TargetOIDAtOpen: p.TargetOIDAtOpen,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO merge_requests
			(id, repo_id, project_id, number, title, body,
			 source_ref, target_ref, source_oid_at_open, target_oid_at_open,
			 author_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING created_at, updated_at
	`,
		mr.ID, mr.RepoID, mr.ProjectID, mr.Number, mr.Title, mr.Body,
		mr.SourceRef, mr.TargetRef, mr.SourceOIDAtOpen, mr.TargetOIDAtOpen,
		p.AuthorID).Scan(&mr.CreatedAt, &mr.UpdatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "merge request")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}

	// Re-read so the response carries the author ref populated from the same
	// authoritative source as the GET path.
	return s.GetMRByNumber(ctx, p.ProjectID, number)
}

// GetMRByNumber returns an MR by its (project, number) identity.
func (s *Store) GetMRByNumber(ctx context.Context, projectID uuid.UUID, number int64) (*model.MergeRequest, error) {
	var mr model.MergeRequest
	var author = model.UserRef{}
	var mergedBy, closedBy *uuid.UUID
	var mergedByU, mergedByD, closedByU, closedByD *string
	err := s.pool.QueryRow(ctx, `
		SELECT m.id, m.repo_id, m.project_id, m.number, m.title, m.body, m.state,
		       m.source_ref, m.target_ref, m.source_oid_at_open, m.target_oid_at_open,
		       m.merge_strategy, m.merge_commit_oid,
		       m.merged_at, m.closed_at, m.created_at, m.updated_at,
		       a.id, a.username, a.display_name,
		       mb.id, mb.username, mb.display_name,
		       cb.id, cb.username, cb.display_name,
		       (SELECT COUNT(*) FROM mr_comments c WHERE c.mr_id = m.id),
		       (SELECT COUNT(*) FROM mr_reviews  r WHERE r.mr_id = m.id)
		FROM merge_requests m
		JOIN users a       ON a.id  = m.author_id
		LEFT JOIN users mb ON mb.id = m.merged_by_id
		LEFT JOIN users cb ON cb.id = m.closed_by_id
		WHERE m.project_id = $1 AND m.number = $2
	`, projectID, number).Scan(
		&mr.ID, &mr.RepoID, &mr.ProjectID, &mr.Number, &mr.Title, &mr.Body, &mr.State,
		&mr.SourceRef, &mr.TargetRef, &mr.SourceOIDAtOpen, &mr.TargetOIDAtOpen,
		&mr.MergeStrategy, &mr.MergeCommitOID,
		&mr.MergedAt, &mr.ClosedAt, &mr.CreatedAt, &mr.UpdatedAt,
		&author.ID, &author.Username, &author.DisplayName,
		&mergedBy, &mergedByU, &mergedByD,
		&closedBy, &closedByU, &closedByD,
		&mr.CommentCnt, &mr.ReviewCnt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("merge request")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	mr.Author = &author
	if mergedBy != nil && mergedByU != nil && mergedByD != nil {
		mr.MergedBy = &model.UserRef{ID: *mergedBy, Username: *mergedByU, DisplayName: *mergedByD}
	}
	if closedBy != nil && closedByU != nil && closedByD != nil {
		mr.ClosedBy = &model.UserRef{ID: *closedBy, Username: *closedByU, DisplayName: *closedByD}
	}
	return &mr, nil
}

// ListMRsFilter narrows ListMRs. Zero values mean "no filter".
type ListMRsFilter struct {
	State     string    // "open", "merged", "closed", or "" for all
	TargetRef string    // exact match on target_ref; empty = no filter
	AuthorID  uuid.UUID // uuid.Nil = no filter
	Limit     int       // capped to 100
}

// ListMRs returns MRs in a repo matching filter, newest first.
func (s *Store) ListMRs(ctx context.Context, repoID uuid.UUID, f ListMRsFilter) ([]model.MergeRequest, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	args := []any{repoID}
	conds := []string{"m.repo_id = $1"}
	add := func(arg any, cond string) {
		args = append(args, arg)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}

	switch f.State {
	case "open", "merged", "closed":
		add(f.State, "m.state = $%d")
	}
	if r := strings.TrimSpace(f.TargetRef); r != "" {
		add(r, "m.target_ref = $%d")
	}
	if f.AuthorID != uuid.Nil {
		add(f.AuthorID, "m.author_id = $%d")
	}

	args = append(args, limit)
	sql := `
		SELECT m.id, m.repo_id, m.project_id, m.number, m.title, m.body, m.state,
		       m.source_ref, m.target_ref, m.source_oid_at_open, m.target_oid_at_open,
		       m.merge_strategy, m.merge_commit_oid,
		       m.merged_at, m.closed_at, m.created_at, m.updated_at,
		       a.id, a.username, a.display_name,
		       (SELECT COUNT(*) FROM mr_comments c WHERE c.mr_id = m.id),
		       (SELECT COUNT(*) FROM mr_reviews  r WHERE r.mr_id = m.id)
		FROM merge_requests m
		JOIN users a ON a.id = m.author_id
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY m.created_at DESC, m.number DESC
		LIMIT $` + fmt.Sprintf("%d", len(args))

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.MergeRequest, 0)
	for rows.Next() {
		var mr model.MergeRequest
		var author model.UserRef
		if err := rows.Scan(
			&mr.ID, &mr.RepoID, &mr.ProjectID, &mr.Number, &mr.Title, &mr.Body, &mr.State,
			&mr.SourceRef, &mr.TargetRef, &mr.SourceOIDAtOpen, &mr.TargetOIDAtOpen,
			&mr.MergeStrategy, &mr.MergeCommitOID,
			&mr.MergedAt, &mr.ClosedAt, &mr.CreatedAt, &mr.UpdatedAt,
			&author.ID, &author.Username, &author.DisplayName,
			&mr.CommentCnt, &mr.ReviewCnt,
		); err != nil {
			return nil, apperr.Internal(err)
		}
		mr.Author = &author
		out = append(out, mr)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// PatchMRParams holds inputs to PatchMR. Pointer fields mean "leave alone if
// nil"; non-nil pointers replace.
type PatchMRParams struct {
	Title *string
	Body  *string
}

// PatchMR updates title/body on an open MR. State transitions live in the
// dedicated MarkMerged / MarkClosed / MarkReopened methods so each carries
// its own validation and audit field updates.
func (s *Store) PatchMR(ctx context.Context, projectID uuid.UUID, number int64, p PatchMRParams) (*model.MergeRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var mrID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM merge_requests
		WHERE project_id = $1 AND number = $2
		FOR UPDATE
	`, projectID, number).Scan(&mrID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("merge request")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	sets := []string{}
	args := []any{}
	nextArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if p.Title != nil {
		t := strings.TrimSpace(*p.Title)
		if t == "" {
			return nil, apperr.Validation("title cannot be empty", nil)
		}
		sets = append(sets, "title = "+nextArg(t))
	}
	if p.Body != nil {
		sets = append(sets, "body = "+nextArg(*p.Body))
	}

	if len(sets) > 0 {
		sets = append(sets, "updated_at = now()")
		idArg := nextArg(mrID)
		sql := "UPDATE merge_requests SET " + strings.Join(sets, ", ") + " WHERE id = " + idArg
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			return nil, mapInsertErr(err, "merge request")
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	return s.GetMRByNumber(ctx, projectID, number)
}

// MarkMerged transitions an open MR to merged, recording the strategy used,
// the resulting commit OID, and who performed the merge. Returns
// apperr.Conflict if the MR is not in 'open' state.
func (s *Store) MarkMerged(ctx context.Context, projectID uuid.UUID, number int64, strategy, mergeOID string, actorID uuid.UUID) (*model.MergeRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var mrID uuid.UUID
	var state string
	err = tx.QueryRow(ctx, `
		SELECT id, state FROM merge_requests
		WHERE project_id = $1 AND number = $2
		FOR UPDATE
	`, projectID, number).Scan(&mrID, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("merge request")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if state != "open" {
		return nil, apperr.Conflict("merge request is not open (current state: " + state + ")")
	}

	if _, err := tx.Exec(ctx, `
		UPDATE merge_requests
		SET state            = 'merged',
		    merge_strategy   = $1,
		    merge_commit_oid = $2,
		    merged_at        = now(),
		    merged_by_id     = $3,
		    updated_at       = now()
		WHERE id = $4
	`, strategy, mergeOID, actorID, mrID); err != nil {
		return nil, mapInsertErr(err, "merge request")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	return s.GetMRByNumber(ctx, projectID, number)
}

// MarkClosed transitions an open MR to closed (without merging).
func (s *Store) MarkClosed(ctx context.Context, projectID uuid.UUID, number int64, actorID uuid.UUID) (*model.MergeRequest, error) {
	return s.transitionState(ctx, projectID, number, "open", "closed", &actorID, true /*setClosedAt*/)
}

// MarkReopened transitions a closed MR back to open. Merged MRs cannot be
// reopened — there's nothing to "un-merge" and the state CHECK constraint
// would refuse it anyway because merge_* fields would have to be cleared.
func (s *Store) MarkReopened(ctx context.Context, projectID uuid.UUID, number int64) (*model.MergeRequest, error) {
	return s.transitionState(ctx, projectID, number, "closed", "open", nil, false)
}

func (s *Store) transitionState(
	ctx context.Context,
	projectID uuid.UUID, number int64,
	fromState, toState string,
	actorID *uuid.UUID, setClosedAt bool,
) (*model.MergeRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var mrID uuid.UUID
	var state string
	err = tx.QueryRow(ctx, `
		SELECT id, state FROM merge_requests
		WHERE project_id = $1 AND number = $2
		FOR UPDATE
	`, projectID, number).Scan(&mrID, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("merge request")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if state != fromState {
		return nil, apperr.Conflict(fmt.Sprintf(
			"cannot transition merge request from %q to %q (current state: %q)",
			fromState, toState, state))
	}

	sets := []string{"state = $1", "updated_at = now()"}
	args := []any{toState}
	if setClosedAt {
		sets = append(sets, "closed_at = now()")
		if actorID != nil {
			args = append(args, *actorID)
			sets = append(sets, fmt.Sprintf("closed_by_id = $%d", len(args)))
		}
	} else {
		sets = append(sets, "closed_at = NULL", "closed_by_id = NULL")
	}
	args = append(args, mrID)
	sql := "UPDATE merge_requests SET " + strings.Join(sets, ", ") +
		fmt.Sprintf(" WHERE id = $%d", len(args))

	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return nil, mapInsertErr(err, "merge request")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	return s.GetMRByNumber(ctx, projectID, number)
}

// ----------------------------------------------------------------------------
// Comments
// ----------------------------------------------------------------------------

// CreateMRCommentParams holds inputs to CreateMRComment.
type CreateMRCommentParams struct {
	ProjectID uuid.UUID
	Number    int64
	AuthorID  uuid.UUID
	Body      string
}

// CreateMRComment posts a comment on an MR. Bumps the parent MR's updated_at
// so listings re-sort correctly when an idle MR gets new activity.
func (s *Store) CreateMRComment(ctx context.Context, p CreateMRCommentParams) (*model.MRComment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var mrID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM merge_requests WHERE project_id = $1 AND number = $2`,
		p.ProjectID, p.Number).Scan(&mrID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("merge request")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	c := &model.MRComment{
		ID:   uuid.New(),
		MRID: mrID,
		Body: p.Body,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO mr_comments (id, mr_id, author_id, body)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at
	`, c.ID, mrID, p.AuthorID, p.Body).Scan(&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "comment")
	}
	if _, err := tx.Exec(ctx,
		`UPDATE merge_requests SET updated_at = now() WHERE id = $1`, mrID); err != nil {
		return nil, apperr.Internal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}

	var a model.UserRef
	if err := s.pool.QueryRow(ctx,
		`SELECT id, username, display_name FROM users WHERE id = $1`, p.AuthorID).
		Scan(&a.ID, &a.Username, &a.DisplayName); err != nil {
		return nil, apperr.Internal(err)
	}
	c.Author = &a
	return c, nil
}

// ListMRComments returns the comments on an MR ordered oldest -> newest.
func (s *Store) ListMRComments(ctx context.Context, projectID uuid.UUID, number int64) ([]model.MRComment, error) {
	var mrID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM merge_requests WHERE project_id = $1 AND number = $2`,
		projectID, number).Scan(&mrID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("merge request")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.mr_id, c.body, c.created_at, c.updated_at,
		       a.id, a.username, a.display_name
		FROM mr_comments c
		JOIN users a ON a.id = c.author_id
		WHERE c.mr_id = $1
		ORDER BY c.created_at ASC, c.id ASC
	`, mrID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.MRComment, 0)
	for rows.Next() {
		var c model.MRComment
		var a model.UserRef
		if err := rows.Scan(&c.ID, &c.MRID, &c.Body, &c.CreatedAt, &c.UpdatedAt,
			&a.ID, &a.Username, &a.DisplayName); err != nil {
			return nil, apperr.Internal(err)
		}
		c.Author = &a
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// Reviews
// ----------------------------------------------------------------------------

// CreateMRReviewParams holds inputs to CreateMRReview.
type CreateMRReviewParams struct {
	ProjectID uuid.UUID
	Number    int64
	AuthorID  uuid.UUID
	State     string // "approved" | "changes_requested" | "commented"
	Body      string
}

// CreateMRReview posts a review event on an MR. Multiple reviews from the
// same user are intentionally allowed — each is its own event. Aggregating
// "what's the current decision" is left to consumers (and to Stage 2 branch
// protection logic).
func (s *Store) CreateMRReview(ctx context.Context, p CreateMRReviewParams) (*model.MRReview, error) {
	switch p.State {
	case "approved", "changes_requested", "commented":
		// ok
	default:
		return nil, apperr.Validation("state must be 'approved', 'changes_requested', or 'commented'", nil)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var mrID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM merge_requests WHERE project_id = $1 AND number = $2`,
		p.ProjectID, p.Number).Scan(&mrID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("merge request")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	r := &model.MRReview{
		ID:    uuid.New(),
		MRID:  mrID,
		State: p.State,
		Body:  p.Body,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO mr_reviews (id, mr_id, author_id, state, body)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at
	`, r.ID, mrID, p.AuthorID, p.State, p.Body).Scan(&r.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "review")
	}

	// Bump the parent MR so listings re-sort on new activity.
	if _, err := tx.Exec(ctx,
		`UPDATE merge_requests SET updated_at = now() WHERE id = $1`, mrID); err != nil {
		return nil, apperr.Internal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}

	var a model.UserRef
	if err := s.pool.QueryRow(ctx,
		`SELECT id, username, display_name FROM users WHERE id = $1`, p.AuthorID).
		Scan(&a.ID, &a.Username, &a.DisplayName); err != nil {
		return nil, apperr.Internal(err)
	}
	r.Author = &a
	return r, nil
}

// ListMRReviews returns the reviews on an MR ordered oldest -> newest.
func (s *Store) ListMRReviews(ctx context.Context, projectID uuid.UUID, number int64) ([]model.MRReview, error) {
	var mrID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM merge_requests WHERE project_id = $1 AND number = $2`,
		projectID, number).Scan(&mrID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("merge request")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.mr_id, r.state, r.body, r.created_at,
		       a.id, a.username, a.display_name
		FROM mr_reviews r
		JOIN users a ON a.id = r.author_id
		WHERE r.mr_id = $1
		ORDER BY r.created_at ASC, r.id ASC
	`, mrID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.MRReview, 0)
	for rows.Next() {
		var r model.MRReview
		var a model.UserRef
		if err := rows.Scan(&r.ID, &r.MRID, &r.State, &r.Body, &r.CreatedAt,
			&a.ID, &a.Username, &a.DisplayName); err != nil {
			return nil, apperr.Internal(err)
		}
		r.Author = &a
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// mapInsertErr converts a pgconn unique-violation into a user-facing apperr.
// Mirrors issuestore.mapInsertErr so the two domains report errors uniformly.
func mapInsertErr(err error, kind string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return apperr.Conflict(fmt.Sprintf("%s already exists", kind))
		case "23503":
			return apperr.New(apperr.CodeBadRequest, fmt.Sprintf("invalid reference creating %s", kind))
		case "23514":
			return apperr.Validation(fmt.Sprintf("invalid value for %s", kind), nil)
		}
	}
	return apperr.Internal(err)
}
