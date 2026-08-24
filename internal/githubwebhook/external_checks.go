package githubwebhook

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zixiao-labs/wuling-devops/internal/notification"
)

func (p *Processor) onWorkflowRun(ctx context.Context, ec EventContext) error {
	var payload struct {
		Action      string `json:"action"`
		WorkflowRun struct {
			ID          int64      `json:"id"`
			Name        string     `json:"name"`
			DisplayName string     `json:"display_title"`
			HeadSHA     string     `json:"head_sha"`
			Status      string     `json:"status"`
			Conclusion  string     `json:"conclusion"`
			HTMLURL     string     `json:"html_url"`
			RunAttempt  int        `json:"run_attempt"`
			UpdatedAt   *time.Time `json:"updated_at"`
		} `json:"workflow_run"`
		Repository struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(ec.Body, &payload); err != nil {
		return err
	}
	if payload.Action != "completed" || payload.WorkflowRun.Status != "completed" ||
		payload.WorkflowRun.ID == 0 || payload.WorkflowRun.Conclusion == "" {
		return nil
	}
	name := payload.WorkflowRun.Name
	if name == "" {
		name = payload.WorkflowRun.DisplayName
	}
	return p.recordCheckCompletion(ctx, ec, payload.Repository.Owner.Login,
		payload.Repository.Name, payload.Repository.FullName, CheckCompletion{
			Source:      "workflow_run",
			ExternalID:  strconv.FormatInt(payload.WorkflowRun.ID, 10),
			Name:        name,
			Provider:    "github-actions",
			HeadSHA:     payload.WorkflowRun.HeadSHA,
			Conclusion:  payload.WorkflowRun.Conclusion,
			Attempt:     payload.WorkflowRun.RunAttempt,
			DetailsURL:  payload.WorkflowRun.HTMLURL,
			CompletedAt: timeOrNow(payload.WorkflowRun.UpdatedAt),
		})
}

func (p *Processor) recordCheckCompletion(
	ctx context.Context,
	ec EventContext,
	owner, name, fullName string,
	check CheckCompletion,
) error {
	if p.Checks == nil || p.Links == nil {
		return nil
	}
	if owner == "" || name == "" {
		owner, name = splitFullName(fullName)
	}
	link, err := p.Links.GetByFullName(ctx, owner, name)
	if err != nil {
		return err
	}
	if link == nil {
		ec.Log.Info("github-webhook: completed check for unlinked repo",
			"repo", owner+"/"+name, "source", check.Source)
		return nil
	}
	check.RepoID = link.RepoID
	check.Color = NormalizeCheckColor(check.Conclusion)
	saved, err := p.Checks.Upsert(ctx, check)
	if err != nil {
		return err
	}
	if p.Notifications == nil {
		return nil
	}
	dedupeKey := strings.Join([]string{
		"github.check.completed",
		link.RepoID.String(),
		saved.Source,
		saved.ExternalID,
		strconv.Itoa(saved.Attempt),
		saved.Conclusion,
		saved.CompletedAt.UTC().Format(time.RFC3339Nano),
	}, ":")
	if err := p.Notifications.Publish(ctx, notification.Event{
		DedupeKey: dedupeKey,
		Type:      "github.check.completed",
		OrgID:     link.OrgID,
		RepoID:    link.RepoID,
		Payload: map[string]any{
			"check_id":     saved.ID,
			"source":       saved.Source,
			"external_id":  saved.ExternalID,
			"name":         saved.Name,
			"provider":     saved.Provider,
			"head_sha":     saved.HeadSHA,
			"status":       "completed",
			"conclusion":   saved.Conclusion,
			"color":        saved.Color,
			"attempt":      saved.Attempt,
			"details_url":  saved.DetailsURL,
			"completed_at": saved.CompletedAt,
		},
	}); err != nil {
		return fmt.Errorf("enqueue github check notification: %w", err)
	}
	ec.Log.Info("github-webhook: completed check recorded",
		"repo", owner+"/"+name,
		"source", saved.Source,
		"check", saved.Name,
		"conclusion", saved.Conclusion,
		"color", saved.Color)
	return nil
}

func timeOrNow(value *time.Time) time.Time {
	if value == nil || value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}
