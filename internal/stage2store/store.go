// Package stage2store owns Stage-2 project planning, test management,
// repository policy, and artifact catalogue persistence.
package stage2store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/model"
)

type Store struct{ pool *db.Pool }

func New(pool *db.Pool) *Store { return &Store{pool: pool} }

func mapDBError(err error, resource string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound(resource)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return apperr.New(apperr.CodeAlreadyExists, resource+" already exists")
		case "23503":
			return apperr.Validation("referenced resource does not exist", nil)
		case "23514", "22P02":
			return apperr.Validation("invalid "+resource, nil)
		}
	}
	return apperr.Internal(err)
}

func normalizedList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func validMergeStrategies(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		switch value {
		case "merge", "squash", "rebase":
		default:
			return false
		}
	}
	return true
}

// --- project dashboard and setup ------------------------------------------------

func (s *Store) GetProjectSettings(ctx context.Context, projectID uuid.UUID) (*model.ProjectSettings, error) {
	var out model.ProjectSettings
	err := s.pool.QueryRow(ctx, `
		SELECT id, process_template, work_item_prefix, iteration_length_days, archived
		FROM projects WHERE id = $1
	`, projectID).Scan(&out.ProjectID, &out.ProcessTemplate, &out.WorkItemPrefix,
		&out.IterationLengthDays, &out.Archived)
	if err != nil {
		return nil, mapDBError(err, "project")
	}
	return &out, nil
}

type UpdateProjectSettingsParams struct {
	ProcessTemplate     *string
	WorkItemPrefix      *string
	IterationLengthDays *int
	Archived            *bool
}

func (s *Store) UpdateProjectSettings(ctx context.Context, projectID uuid.UUID, p UpdateProjectSettingsParams) (*model.ProjectSettings, error) {
	if p.WorkItemPrefix != nil {
		value := strings.ToUpper(strings.TrimSpace(*p.WorkItemPrefix))
		p.WorkItemPrefix = &value
	}
	var out model.ProjectSettings
	err := s.pool.QueryRow(ctx, `
		UPDATE projects SET
			process_template = COALESCE($2, process_template),
			work_item_prefix = COALESCE($3, work_item_prefix),
			iteration_length_days = COALESCE($4, iteration_length_days),
			archived = COALESCE($5, archived),
			updated_at = now()
		WHERE id = $1
		RETURNING id, process_template, work_item_prefix, iteration_length_days, archived
	`, projectID, p.ProcessTemplate, p.WorkItemPrefix, p.IterationLengthDays, p.Archived).Scan(
		&out.ProjectID, &out.ProcessTemplate, &out.WorkItemPrefix,
		&out.IterationLengthDays, &out.Archived,
	)
	if err != nil {
		return nil, mapDBError(err, "project settings")
	}
	return &out, nil
}

func (s *Store) Dashboard(ctx context.Context, projectID uuid.UUID) (*model.DashboardCounts, error) {
	out := &model.DashboardCounts{
		BacklogByState: map[string]int64{"new": 0, "active": 0, "resolved": 0, "closed": 0},
		BacklogByType: map[string]int64{
			"epic": 0, "feature": 0, "user_story": 0, "task": 0, "bug": 0,
		},
	}
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM repos WHERE project_id = $1),
			(SELECT count(*) FROM issues WHERE project_id = $1 AND state = 'open'),
			(SELECT count(*) FROM work_items WHERE project_id = $1 AND state <> 'closed'),
			(SELECT count(*) FROM pipeline_runs WHERE project_id = $1),
			(SELECT count(*) FROM test_cases tc JOIN test_suites ts ON ts.id = tc.suite_id
			 JOIN test_plans tp ON tp.id = ts.plan_id WHERE tp.project_id = $1),
			(SELECT count(*) FROM artifact_packages WHERE project_id = $1),
			(SELECT count(*) FROM project_releases WHERE project_id = $1)
	`, projectID).Scan(&out.Repos, &out.OpenIssues, &out.OpenWorkItems,
		&out.PipelineRuns, &out.TestCases, &out.Packages, &out.Releases)
	if err != nil {
		return nil, mapDBError(err, "project dashboard")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT state, count(*) FROM work_items WHERE project_id = $1 GROUP BY state
	`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			rows.Close()
			return nil, apperr.Internal(err)
		}
		out.BacklogByState[key] = count
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `
		SELECT type, count(*) FROM work_items WHERE project_id = $1 GROUP BY type
	`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return nil, apperr.Internal(err)
		}
		out.BacklogByType[key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Internal(err)
	}
	return out, nil
}

// --- iterations -----------------------------------------------------------------

type CreateIterationParams struct {
	ProjectID uuid.UUID
	Name      string
	Goal      string
	State     string
	StartsAt  time.Time
	EndsAt    time.Time
}

func (s *Store) CreateIteration(ctx context.Context, p CreateIterationParams) (*model.ProjectIteration, error) {
	if p.EndsAt.Before(p.StartsAt) {
		return nil, apperr.Validation("iteration end date must not precede start date", nil)
	}
	out := &model.ProjectIteration{ID: uuid.New(), ProjectID: p.ProjectID,
		Name: strings.TrimSpace(p.Name), Goal: p.Goal, State: p.State,
		StartsAt: p.StartsAt, EndsAt: p.EndsAt}
	if out.State == "" {
		out.State = "planned"
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO project_iterations (id, project_id, name, goal, state, starts_at, ends_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING created_at, updated_at
	`, out.ID, out.ProjectID, out.Name, out.Goal, out.State, out.StartsAt, out.EndsAt).
		Scan(&out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, mapDBError(err, "iteration")
	}
	return out, nil
}

func (s *Store) ListIterations(ctx context.Context, projectID uuid.UUID) ([]model.ProjectIteration, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, name, goal, state, starts_at, ends_at, created_at, updated_at
		FROM project_iterations WHERE project_id = $1 ORDER BY starts_at DESC, name
	`, projectID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.ProjectIteration, 0)
	for rows.Next() {
		var item model.ProjectIteration
		if err := rows.Scan(&item.ID, &item.ProjectID, &item.Name, &item.Goal, &item.State,
			&item.StartsAt, &item.EndsAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, item)
	}
	return out, mapDBError(rows.Err(), "iterations")
}

type UpdateIterationParams struct {
	Name     *string
	Goal     *string
	State    *string
	StartsAt *time.Time
	EndsAt   *time.Time
}

func (s *Store) UpdateIteration(ctx context.Context, projectID, id uuid.UUID, p UpdateIterationParams) (*model.ProjectIteration, error) {
	var out model.ProjectIteration
	err := s.pool.QueryRow(ctx, `
		UPDATE project_iterations SET
			name = COALESCE($3, name), goal = COALESCE($4, goal),
			state = COALESCE($5, state), starts_at = COALESCE($6, starts_at),
			ends_at = COALESCE($7, ends_at), updated_at = now()
		WHERE id = $2 AND project_id = $1
		RETURNING id, project_id, name, goal, state, starts_at, ends_at, created_at, updated_at
	`, projectID, id, p.Name, p.Goal, p.State, p.StartsAt, p.EndsAt).Scan(
		&out.ID, &out.ProjectID, &out.Name, &out.Goal, &out.State,
		&out.StartsAt, &out.EndsAt, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, mapDBError(err, "iteration")
	}
	return &out, nil
}

// --- work items -----------------------------------------------------------------

type CreateWorkItemParams struct {
	ProjectID    uuid.UUID
	ParentID     *uuid.UUID
	IterationID  *uuid.UUID
	AssigneeID   *uuid.UUID
	AuthorID     uuid.UUID
	Type         string
	Title        string
	Description  string
	Priority     *int
	StoryPoints  *float64
	AreaPath     string
	BacklogOrder float64
}

func (s *Store) CreateWorkItem(ctx context.Context, p CreateWorkItemParams) (*model.WorkItem, error) {
	var out *model.WorkItem
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var number int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO work_item_number_seq (project_id, next_number) VALUES ($1, 1)
			ON CONFLICT (project_id) DO UPDATE
			SET next_number = work_item_number_seq.next_number + 1
			RETURNING next_number
		`, p.ProjectID).Scan(&number); err != nil {
			return err
		}
		order := p.BacklogOrder
		if order == 0 {
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(max(backlog_order), 0) + 1024 FROM work_items WHERE project_id = $1
			`, p.ProjectID).Scan(&order); err != nil {
				return err
			}
		}
		item := &model.WorkItem{
			ID: uuid.New(), ProjectID: p.ProjectID, Number: number,
			ParentID: p.ParentID, IterationID: p.IterationID,
			AssigneeID: p.AssigneeID, AuthorID: &p.AuthorID, Type: p.Type,
			Title: strings.TrimSpace(p.Title), Description: p.Description,
			State: "new", Priority: 2, StoryPoints: p.StoryPoints,
			AreaPath: p.AreaPath, BacklogOrder: order,
		}
		if item.Type == "" {
			item.Type = "task"
		}
		if p.Priority != nil {
			item.Priority = *p.Priority
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO work_items
				(id, project_id, number, parent_id, iteration_id, assignee_id, author_id,
				 type, title, description, state, priority, story_points, area_path, backlog_order)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			RETURNING created_at, updated_at, closed_at
		`, item.ID, item.ProjectID, item.Number, item.ParentID, item.IterationID,
			item.AssigneeID, item.AuthorID, item.Type, item.Title, item.Description,
			item.State, item.Priority, item.StoryPoints, item.AreaPath, item.BacklogOrder).
			Scan(&item.CreatedAt, &item.UpdatedAt, &item.ClosedAt)
		if err != nil {
			return err
		}
		out = item
		return nil
	})
	if err != nil {
		return nil, mapDBError(err, "work item")
	}
	return out, nil
}

func scanWorkItem(row pgx.Row) (*model.WorkItem, error) {
	var item model.WorkItem
	err := row.Scan(&item.ID, &item.ProjectID, &item.Number, &item.ParentID,
		&item.IterationID, &item.AssigneeID, &item.AuthorID, &item.Type,
		&item.Title, &item.Description, &item.State, &item.Priority,
		&item.StoryPoints, &item.AreaPath, &item.BacklogOrder,
		&item.CreatedAt, &item.UpdatedAt, &item.ClosedAt)
	return &item, err
}

const workItemColumns = `id, project_id, number, parent_id, iteration_id,
	assignee_id, author_id, type, title, description, state, priority,
	story_points, area_path, backlog_order, created_at, updated_at, closed_at`

func (s *Store) ListWorkItems(ctx context.Context, projectID uuid.UUID, state string, iterationID *uuid.UUID) ([]model.WorkItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+workItemColumns+`
		FROM work_items
		WHERE project_id = $1
		  AND ($2 = '' OR state = $2)
		  AND ($3::uuid IS NULL OR iteration_id = $3)
		ORDER BY backlog_order, number`, projectID, state, iterationID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	defer rows.Close()
	out := make([]model.WorkItem, 0)
	for rows.Next() {
		item, err := scanWorkItem(rows)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		out = append(out, *item)
	}
	return out, mapDBError(rows.Err(), "work items")
}

type UpdateWorkItemParams struct {
	ParentID     *uuid.UUID
	IterationID  *uuid.UUID
	AssigneeID   *uuid.UUID
	Type         *string
	Title        *string
	Description  *string
	State        *string
	Priority     *int
	StoryPoints  *float64
	AreaPath     *string
	BacklogOrder *float64
}

func (s *Store) UpdateWorkItem(ctx context.Context, projectID uuid.UUID, number int64, p UpdateWorkItemParams) (*model.WorkItem, error) {
	row := s.pool.QueryRow(ctx, `UPDATE work_items SET
		parent_id = COALESCE($3, parent_id), iteration_id = COALESCE($4, iteration_id),
		assignee_id = COALESCE($5, assignee_id), type = COALESCE($6, type),
		title = COALESCE($7, title), description = COALESCE($8, description),
		state = COALESCE($9, state), priority = COALESCE($10, priority),
		story_points = COALESCE($11, story_points), area_path = COALESCE($12, area_path),
		backlog_order = COALESCE($13, backlog_order), updated_at = now(),
		closed_at = CASE WHEN COALESCE($9, state) = 'closed' THEN COALESCE(closed_at, now()) ELSE NULL END
		WHERE project_id = $1 AND number = $2 RETURNING `+workItemColumns,
		projectID, number, p.ParentID, p.IterationID, p.AssigneeID, p.Type,
		p.Title, p.Description, p.State, p.Priority, p.StoryPoints,
		p.AreaPath, p.BacklogOrder)
	out, err := scanWorkItem(row)
	if err != nil {
		return nil, mapDBError(err, "work item")
	}
	return out, nil
}

func parseDate(value string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, apperr.Validation("date must use YYYY-MM-DD", map[string]any{"value": value})
	}
	return t, nil
}

// ParseDate is shared by the HTTP layer and kept here so its behavior is easy
// to unit test without constructing requests.
func ParseDate(value string) (time.Time, error) { return parseDate(value) }

func formatBlobKey(projectID, packageID uuid.UUID, version string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_', r == '+', r == '@':
			return r
		default:
			return '-'
		}
	}, strings.TrimSpace(version))
	clean = strings.ReplaceAll(clean, "..", "-")
	for strings.Contains(clean, "--") {
		clean = strings.ReplaceAll(clean, "--", "-")
	}
	clean = strings.Trim(clean, "-.")
	if clean == "" {
		clean = "version"
	}
	return fmt.Sprintf("projects/%s/packages/%s/%s", projectID, packageID, clean)
}
