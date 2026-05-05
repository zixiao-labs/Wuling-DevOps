// Package issuestore is the persistence layer for the Issues domain:
// issues, labels, comments, and assignees. Like userstore it never imports
// the HTTP layer and returns apperr-wrapped errors.
package issuestore

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

// Store is the data-access object for the Issues domain.
type Store struct{ pool *db.Pool }

// New returns a Store backed by pool.
func New(pool *db.Pool) *Store { return &Store{pool: pool} }

// ----------------------------------------------------------------------------
// Labels
// ----------------------------------------------------------------------------

// CreateLabelParams holds inputs to CreateLabel.
type CreateLabelParams struct {
	ProjectID   uuid.UUID
	Name        string
	Color       string
	Description string
}

// CreateLabel inserts a label scoped to a project.
func (s *Store) CreateLabel(ctx context.Context, p CreateLabelParams) (*model.Label, error) {
	l := &model.Label{
		ID:          uuid.New(),
		ProjectID:   p.ProjectID,
		Name:        strings.TrimSpace(p.Name),
		Color:       defaultIfEmpty(p.Color, "888888"),
		Description: p.Description,
	}
	if l.Name == "" {
		return nil, apperr.Validation("label name cannot be empty", nil)
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO labels (id, project_id, name, color, description)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at
	`, l.ID, l.ProjectID, l.Name, l.Color, l.Description).Scan(&l.CreatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "label")
	}
	return l, nil
}

// ListLabels returns all labels in a project ordered by name.
func (s *Store) ListLabels(ctx context.Context, projectID uuid.UUID) ([]model.Label, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, name, color, description, created_at
		FROM labels WHERE project_id = $1 ORDER BY LOWER(name) ASC
	`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.Label, 0)
	for rows.Next() {
		var l model.Label
		if err := rows.Scan(&l.ID, &l.ProjectID, &l.Name, &l.Color, &l.Description, &l.CreatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// GetLabelByName looks up a label by case-insensitive name within a project.
// Returns NotFound if no row matches.
func (s *Store) GetLabelByName(ctx context.Context, projectID uuid.UUID, name string) (*model.Label, error) {
	var l model.Label
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, name, color, description, created_at
		FROM labels WHERE project_id = $1 AND LOWER(name) = LOWER($2)
	`, projectID, name).Scan(&l.ID, &l.ProjectID, &l.Name, &l.Color, &l.Description, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("label")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &l, nil
}

// DeleteLabel removes a label by id within a project.
func (s *Store) DeleteLabel(ctx context.Context, projectID, labelID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM labels WHERE id = $1 AND project_id = $2`, labelID, projectID)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("label")
	}
	return nil
}

// ----------------------------------------------------------------------------
// Issues
// ----------------------------------------------------------------------------

// CreateIssueParams holds inputs to CreateIssue.
type CreateIssueParams struct {
	ProjectID   uuid.UUID
	AuthorID    uuid.UUID
	Title       string
	Body        string
	LabelIDs    []uuid.UUID
	AssigneeIDs []uuid.UUID
}

// CreateIssue inserts an issue, allocating the next per-project number
// atomically and recording labels/assignees in the same transaction so a
// crash never produces an issue with half its metadata.
func (s *Store) CreateIssue(ctx context.Context, p CreateIssueParams) (*model.Issue, error) {
	id := uuid.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Allocate the next issue number for this project. The UPSERT ensures
	// the row exists on first call; the row-level lock acquired by the
	// UPDATE serialises concurrent inserts so no two issues collide on
	// (project_id, number).
	var number int64
	err = tx.QueryRow(ctx, `
		INSERT INTO issue_number_seq (project_id, next_value)
		VALUES ($1, 2)
		ON CONFLICT (project_id) DO UPDATE
		   SET next_value = issue_number_seq.next_value + 1
		RETURNING next_value - 1
	`, p.ProjectID).Scan(&number)
	if err != nil {
		return nil, mapInsertErr(err, "issue number")
	}

	iss := &model.Issue{
		ID:        id,
		ProjectID: p.ProjectID,
		Number:    number,
		Title:     strings.TrimSpace(p.Title),
		Body:      p.Body,
		State:     "open",
		Labels:    []model.Label{},
		Assignees: []model.UserRef{},
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO issues (id, project_id, number, title, body, author_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at
	`, iss.ID, iss.ProjectID, iss.Number, iss.Title, iss.Body, p.AuthorID).
		Scan(&iss.CreatedAt, &iss.UpdatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "issue")
	}

	if err := attachLabelsTx(ctx, tx, iss.ID, p.ProjectID, p.LabelIDs); err != nil {
		return nil, err
	}
	if err := attachAssigneesTx(ctx, tx, iss.ID, p.ProjectID, p.AssigneeIDs); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}

	// Re-read so the response includes author/labels/assignees populated
	// from the same authoritative source as the GET path.
	full, err := s.GetIssueByNumber(ctx, p.ProjectID, number)
	if err != nil {
		return nil, err
	}
	return full, nil
}

// GetIssueByNumber returns an issue by its (project, number) identity.
func (s *Store) GetIssueByNumber(ctx context.Context, projectID uuid.UUID, number int64) (*model.Issue, error) {
	var iss model.Issue
	var author = model.UserRef{}
	var closedBy *uuid.UUID
	var closedByUsername, closedByDisplay *string
	err := s.pool.QueryRow(ctx, `
		SELECT i.id, i.project_id, i.number, i.title, i.body, i.state,
		       i.closed_at, i.created_at, i.updated_at,
		       a.id, a.username, a.display_name,
		       c.id, c.username, c.display_name,
		       (SELECT COUNT(*) FROM issue_comments ic WHERE ic.issue_id = i.id)
		FROM issues i
		JOIN users a ON a.id = i.author_id
		LEFT JOIN users c ON c.id = i.closed_by_id
		WHERE i.project_id = $1 AND i.number = $2
	`, projectID, number).Scan(
		&iss.ID, &iss.ProjectID, &iss.Number, &iss.Title, &iss.Body, &iss.State,
		&iss.ClosedAt, &iss.CreatedAt, &iss.UpdatedAt,
		&author.ID, &author.Username, &author.DisplayName,
		&closedBy, &closedByUsername, &closedByDisplay,
		&iss.CommentCnt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("issue")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	iss.Author = &author
	if closedBy != nil && closedByUsername != nil && closedByDisplay != nil {
		iss.ClosedBy = &model.UserRef{ID: *closedBy, Username: *closedByUsername, DisplayName: *closedByDisplay}
	}
	// Initialize the embedded collections so JSON serialization always emits
	// "labels":[] / "assignees":[] rather than null. Clients can then iterate
	// the arrays without nullity checks.
	iss.Labels = []model.Label{}
	iss.Assignees = []model.UserRef{}

	if err := s.populateRelations(ctx, []*model.Issue{&iss}); err != nil {
		return nil, err
	}
	return &iss, nil
}

// ListIssuesFilter narrows ListIssues. Zero values mean "no filter".
type ListIssuesFilter struct {
	State      string    // "open", "closed", or "" for both
	LabelName  string    // case-insensitive name; empty = no filter
	AssigneeID uuid.UUID // uuid.Nil = no filter
	AuthorID   uuid.UUID // uuid.Nil = no filter
	Search     string    // ILIKE on title+body; empty = no filter
	Limit      int       // capped to 100
}

// ListIssues returns issues in a project matching filter, newest first.
//
// The query path is:
//  1. Apply filters on the issues table (joining issue_labels / assignees
//     when the relevant filter is set).
//  2. Eagerly populate labels + assignees for the resulting set in two
//     batched follow-up queries — N+1 SELECTs per issue would be a
//     pathology we'd notice immediately under any load.
func (s *Store) ListIssues(ctx context.Context, projectID uuid.UUID, f ListIssuesFilter) ([]model.Issue, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	args := []any{projectID}
	conds := []string{"i.project_id = $1"}
	add := func(arg any, cond string) {
		args = append(args, arg)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}

	if f.State == "open" || f.State == "closed" {
		add(f.State, "i.state = $%d")
	}
	if f.AuthorID != uuid.Nil {
		add(f.AuthorID, "i.author_id = $%d")
	}
	if f.AssigneeID != uuid.Nil {
		add(f.AssigneeID,
			"EXISTS (SELECT 1 FROM issue_assignees a WHERE a.issue_id = i.id AND a.user_id = $%d)")
	}
	if name := strings.TrimSpace(f.LabelName); name != "" {
		add(name,
			"EXISTS (SELECT 1 FROM issue_labels il JOIN labels l ON l.id = il.label_id "+
				"WHERE il.issue_id = i.id AND l.project_id = i.project_id "+
				"AND LOWER(l.name) = LOWER($%d))")
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		args = append(args, "%"+q+"%")
		pos := len(args)
		conds = append(conds, fmt.Sprintf(
			"(i.title ILIKE $%d OR i.body ILIKE $%d)", pos, pos))
	}

	args = append(args, limit)

	sql := `
		SELECT i.id, i.project_id, i.number, i.title, i.body, i.state,
		       i.closed_at, i.created_at, i.updated_at,
		       a.id, a.username, a.display_name,
		       (SELECT COUNT(*) FROM issue_comments ic WHERE ic.issue_id = i.id)
		FROM issues i
		JOIN users a ON a.id = i.author_id
		WHERE ` + strings.Join(conds, " AND ") + `
		ORDER BY i.created_at DESC, i.number DESC
		LIMIT $` + fmt.Sprintf("%d", len(args))

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()

	out := make([]model.Issue, 0)
	ptrs := make([]*model.Issue, 0)
	for rows.Next() {
		var iss model.Issue
		var author model.UserRef
		if err := rows.Scan(
			&iss.ID, &iss.ProjectID, &iss.Number, &iss.Title, &iss.Body, &iss.State,
			&iss.ClosedAt, &iss.CreatedAt, &iss.UpdatedAt,
			&author.ID, &author.Username, &author.DisplayName,
			&iss.CommentCnt,
		); err != nil {
			return nil, apperr.Internal(err)
		}
		iss.Author = &author
		iss.Labels = []model.Label{}
		iss.Assignees = []model.UserRef{}
		out = append(out, iss)
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	for i := range out {
		ptrs = append(ptrs, &out[i])
	}

	if err := s.populateRelations(ctx, ptrs); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateIssueParams holds inputs to UpdateIssue. Pointer fields mean "leave
// alone if nil"; non-nil pointers replace the existing value, including
// empty slices for labels/assignees.
type UpdateIssueParams struct {
	Title       *string
	Body        *string
	State       *string
	LabelIDs    *[]uuid.UUID
	AssigneeIDs *[]uuid.UUID
	ActorID     uuid.UUID // user performing the change (for closed_by)
}

// UpdateIssue patches an issue. Returns the refreshed model.
func (s *Store) UpdateIssue(ctx context.Context, projectID uuid.UUID, number int64, p UpdateIssueParams) (*model.Issue, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the row so concurrent edits serialise.
	var issueID uuid.UUID
	var currentState string
	err = tx.QueryRow(ctx, `
		SELECT id, state FROM issues
		WHERE project_id = $1 AND number = $2
		FOR UPDATE
	`, projectID, number).Scan(&issueID, &currentState)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("issue")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	// Build an UPDATE statement only over the fields that were provided.
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
	if p.State != nil {
		st := *p.State
		if st != "open" && st != "closed" {
			return nil, apperr.Validation("state must be 'open' or 'closed'", nil)
		}
		if st != currentState {
			sets = append(sets, "state = "+nextArg(st))
			if st == "closed" {
				sets = append(sets, "closed_at = now()")
				sets = append(sets, "closed_by_id = "+nextArg(p.ActorID))
			} else {
				sets = append(sets, "closed_at = NULL")
				sets = append(sets, "closed_by_id = NULL")
			}
		}
	}

	// If labels or assignees are being modified, ensure updated_at is set even if no other fields changed
	if len(sets) > 0 || p.LabelIDs != nil || p.AssigneeIDs != nil {
		sets = append(sets, "updated_at = now()")
		idArg := nextArg(issueID)
		sql := "UPDATE issues SET " + strings.Join(sets, ", ") + " WHERE id = " + idArg
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			return nil, mapInsertErr(err, "issue")
		}
	}

	if p.LabelIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM issue_labels WHERE issue_id = $1`, issueID); err != nil {
			return nil, apperr.Internal(err)
		}
		if err := attachLabelsTx(ctx, tx, issueID, projectID, *p.LabelIDs); err != nil {
			return nil, err
		}
	}
	if p.AssigneeIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM issue_assignees WHERE issue_id = $1`, issueID); err != nil {
			return nil, apperr.Internal(err)
		}
		if err := attachAssigneesTx(ctx, tx, issueID, projectID, *p.AssigneeIDs); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}
	return s.GetIssueByNumber(ctx, projectID, number)
}

// DeleteIssue removes an issue and its dependent rows by cascade.
func (s *Store) DeleteIssue(ctx context.Context, projectID uuid.UUID, number int64) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM issues WHERE project_id = $1 AND number = $2`, projectID, number)
	if err != nil {
		return apperr.Internal(err)
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("issue")
	}
	return nil
}

// ----------------------------------------------------------------------------
// Comments
// ----------------------------------------------------------------------------

// CreateCommentParams holds inputs to CreateComment.
type CreateCommentParams struct {
	ProjectID uuid.UUID
	Number    int64
	AuthorID  uuid.UUID
	Body      string
}

// CreateComment posts a comment on an issue.
func (s *Store) CreateComment(ctx context.Context, p CreateCommentParams) (*model.IssueComment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var issueID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM issues WHERE project_id = $1 AND number = $2`,
		p.ProjectID, p.Number).Scan(&issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("issue")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	c := &model.IssueComment{
		ID:      uuid.New(),
		IssueID: issueID,
		Body:    p.Body,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO issue_comments (id, issue_id, author_id, body)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at, updated_at
	`, c.ID, issueID, p.AuthorID, p.Body).Scan(&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, mapInsertErr(err, "comment")
	}

	// Bump the parent issue's updated_at so listings re-sort correctly when
	// a stale issue gets new activity.
	if _, err := tx.Exec(ctx,
		`UPDATE issues SET updated_at = now() WHERE id = $1`, issueID); err != nil {
		return nil, apperr.Internal(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, apperr.Internal(err)
	}

	// Hydrate the author shape.
	var a model.UserRef
	if err := s.pool.QueryRow(ctx,
		`SELECT id, username, display_name FROM users WHERE id = $1`, p.AuthorID).
		Scan(&a.ID, &a.Username, &a.DisplayName); err != nil {
		return nil, apperr.Internal(err)
	}
	c.Author = &a
	return c, nil
}

// ListComments returns the comments on an issue ordered oldest -> newest.
func (s *Store) ListComments(ctx context.Context, projectID uuid.UUID, number int64) ([]model.IssueComment, error) {
	var issueID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM issues WHERE project_id = $1 AND number = $2`,
		projectID, number).Scan(&issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("issue")
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.issue_id, c.body, c.created_at, c.updated_at,
		       a.id, a.username, a.display_name
		FROM issue_comments c
		JOIN users a ON a.id = c.author_id
		WHERE c.issue_id = $1
		ORDER BY c.created_at ASC, c.id ASC
	`, issueID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.IssueComment, 0)
	for rows.Next() {
		var c model.IssueComment
		var a model.UserRef
		if err := rows.Scan(&c.ID, &c.IssueID, &c.Body, &c.CreatedAt, &c.UpdatedAt,
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
// helpers
// ----------------------------------------------------------------------------

// populateRelations bulk-loads labels and assignees for a slice of issues.
// Two queries regardless of slice size; safe to call with an empty slice.
func (s *Store) populateRelations(ctx context.Context, issues []*model.Issue) error {
	if len(issues) == 0 {
		return nil
	}
	idx := make(map[uuid.UUID]*model.Issue, len(issues))
	ids := make([]uuid.UUID, 0, len(issues))
	for _, iss := range issues {
		idx[iss.ID] = iss
		ids = append(ids, iss.ID)
	}

	// Labels.
	rows, err := s.pool.Query(ctx, `
		SELECT il.issue_id, l.id, l.project_id, l.name, l.color, l.description, l.created_at
		FROM issue_labels il
		JOIN labels l ON l.id = il.label_id
		WHERE il.issue_id = ANY($1)
		ORDER BY LOWER(l.name) ASC
	`, ids)
	if err != nil {
		return apperr.Internal(err)
	}
	for rows.Next() {
		var issueID uuid.UUID
		var l model.Label
		if err := rows.Scan(&issueID, &l.ID, &l.ProjectID, &l.Name, &l.Color, &l.Description, &l.CreatedAt); err != nil {
			rows.Close()
			return apperr.Internal(err)
		}
		if iss, ok := idx[issueID]; ok {
			iss.Labels = append(iss.Labels, l)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return apperr.Internal(err)
	}

	// Assignees.
	rows, err = s.pool.Query(ctx, `
		SELECT ia.issue_id, u.id, u.username, u.display_name
		FROM issue_assignees ia
		JOIN users u ON u.id = ia.user_id
		WHERE ia.issue_id = ANY($1)
		ORDER BY LOWER(u.username) ASC
	`, ids)
	if err != nil {
		return apperr.Internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var issueID uuid.UUID
		var u model.UserRef
		if err := rows.Scan(&issueID, &u.ID, &u.Username, &u.DisplayName); err != nil {
			return apperr.Internal(err)
		}
		if iss, ok := idx[issueID]; ok {
			iss.Assignees = append(iss.Assignees, u)
		}
	}
	if err := rows.Err(); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// attachLabelsTx links a set of labels to an issue. It validates that every
// label belongs to the same project (otherwise a caller could attach a
// label from another project they don't have access to).
func attachLabelsTx(ctx context.Context, tx pgx.Tx, issueID, projectID uuid.UUID, labelIDs []uuid.UUID) error {
	if len(labelIDs) == 0 {
		return nil
	}
	// De-duplicate while preserving submitted order.
	seen := make(map[uuid.UUID]struct{}, len(labelIDs))
	uniq := labelIDs[:0:0]
	for _, id := range labelIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}

	// Verify all labels exist and belong to projectID.
	var found int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM labels WHERE id = ANY($1) AND project_id = $2`,
		uniq, projectID).Scan(&found); err != nil {
		return apperr.Internal(err)
	}
	if found != len(uniq) {
		return apperr.Validation("one or more labels do not belong to this project", nil)
	}

	for _, id := range uniq {
		if _, err := tx.Exec(ctx, `
			INSERT INTO issue_labels (issue_id, label_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, issueID, id); err != nil {
			return apperr.Internal(err)
		}
	}
	return nil
}

// attachAssigneesTx links users to an issue. It validates that every
// assignee is a member of the org owning the project — non-members can't
// be put on the hook for issues they can't even read.
func attachAssigneesTx(ctx context.Context, tx pgx.Tx, issueID, projectID uuid.UUID, userIDs []uuid.UUID) error {
	if len(userIDs) == 0 {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(userIDs))
	uniq := userIDs[:0:0]
	for _, id := range userIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}

	var found int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM org_members om
		JOIN projects p ON p.org_id = om.org_id
		WHERE p.id = $1 AND om.user_id = ANY($2)
	`, projectID, uniq).Scan(&found); err != nil {
		return apperr.Internal(err)
	}
	if found != len(uniq) {
		return apperr.Validation("one or more assignees are not members of this org", nil)
	}

	for _, id := range uniq {
		if _, err := tx.Exec(ctx, `
			INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, issueID, id); err != nil {
			return apperr.Internal(err)
		}
	}
	return nil
}

func defaultIfEmpty(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// mapInsertErr converts a pgconn unique-violation into a user-facing apperr.
// Mirrors userstore.mapInsertErr without taking a dependency on it.
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
