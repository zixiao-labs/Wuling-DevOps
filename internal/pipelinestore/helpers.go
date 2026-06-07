package pipelinestore

import (
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zixiao-labs/wuling-devops/internal/model"
)

// row is the common interface of pgx.Row and a single pgx.Rows iteration.
type scanner interface {
	Scan(dest ...any) error
}

// scanRun decodes one pipeline_runs row joined to its triggering user.
func scanRun(row scanner) (*model.PipelineRun, error) {
	var r model.PipelineRun
	var uID *uuid.UUID
	var uName, uDisplay *string
	if err := row.Scan(
		&r.ID, &r.OrgID, &r.ProjectID, &r.RepoID, &r.Number, &r.WorkflowPath, &r.WorkflowName,
		&r.Event, &r.GitRef, &r.CommitSHA, &r.CommitMessage, &r.Status,
		&r.CreatedAt, &r.StartedAt, &r.FinishedAt,
		&uID, &uName, &uDisplay,
	); err != nil {
		return nil, err
	}
	if uID != nil && uName != nil && uDisplay != nil {
		r.TriggeredBy = &model.UserRef{ID: *uID, Username: *uName, DisplayName: *uDisplay}
	}
	return &r, nil
}

func scanRuns(rows pgx.Rows) ([]model.PipelineRun, error) {
	out := make([]model.PipelineRun, 0)
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// normStrings coerces a nil slice to an empty one so a NOT NULL text[] column
// never receives NULL.
func normStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func itoa(i int) string { return strconv.Itoa(i) }
