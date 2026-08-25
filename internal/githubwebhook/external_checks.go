package githubwebhook

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zixiao-labs/wuling-devops/internal/githubapp"
	"github.com/zixiao-labs/wuling-devops/internal/notification"
)

const (
	feedbackExternalIDPrefix = "wuling-monitor:"
	feedbackCheckNameLimit   = 120
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
		}, true)
}

func (p *Processor) recordCheckCompletion(
	ctx context.Context,
	ec EventContext,
	owner, name, fullName string,
	check CheckCompletion,
	feedback bool,
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
	saved, err := p.persistCheckCompletion(ctx, link, check)
	if err != nil {
		return err
	}
	ec.Log.Info("github-webhook: completed check recorded",
		"repo", owner+"/"+name,
		"source", saved.Source,
		"check", saved.Name,
		"conclusion", saved.Conclusion,
		"color", saved.Color)
	if feedback {
		if err := p.echoCheckCompletion(ctx, link, saved); err != nil {
			return fmt.Errorf("echo completed github check: %w", err)
		}
	}
	return nil
}

// echoCheckCompletion mirrors an observed terminal result into a Wuling-owned
// Check Run. The stable name is what GitHub exposes as a selectable required
// check in branch protection and rulesets. We update a previously-created run
// on replay/rerun instead of producing duplicate contexts.
func (p *Processor) echoCheckCompletion(
	ctx context.Context,
	link *RepoLink,
	check *CheckCompletion,
) error {
	if check == nil {
		return fmt.Errorf("echo github check completion: check is nil")
	}
	if check.HeadSHA == "" {
		return fmt.Errorf("echo github check completion: head SHA is empty")
	}
	if p.App == nil {
		return githubAppUnavailable("echo github check completion")
	}
	token, err := p.App.InstallationToken(link.InstallationID)
	if err != nil {
		return err
	}
	checkName := feedbackCheckName(check)
	externalID := feedbackExternalIDPrefix + check.Source + ":" + check.ExternalID
	output := &githubapp.CheckOutput{
		Title: checkName,
		Summary: fmt.Sprintf(
			"Observed %s result %q from %s: %s (attempt %d).",
			check.Source, check.Name, feedbackProvider(check), check.Conclusion, check.Attempt,
		),
	}
	conclusion := feedbackConclusion(check.Conclusion)
	completedAt := check.CompletedAt.UTC().Format(time.RFC3339)
	if check.FeedbackCheckRunID > 0 {
		return p.App.UpdateCheckRun(token, link.Owner, link.Name, check.FeedbackCheckRunID,
			githubapp.UpdateCheckRunRequest{
				Name:        checkName,
				Status:      "completed",
				Conclusion:  conclusion,
				CompletedAt: completedAt,
				DetailsURL:  check.DetailsURL,
				ExternalID:  externalID,
				Output:      output,
			})
	}
	checkRunID, err := p.App.CreateCheckRun(token, link.Owner, link.Name,
		githubapp.CreateCheckRunRequest{
			Name:        checkName,
			HeadSHA:     check.HeadSHA,
			Status:      "completed",
			Conclusion:  conclusion,
			CompletedAt: completedAt,
			DetailsURL:  check.DetailsURL,
			ExternalID:  externalID,
			Output:      output,
		})
	if err != nil {
		return err
	}
	if checkRunID <= 0 {
		return fmt.Errorf("github returned an empty feedback check run id")
	}
	if err := p.Checks.SetFeedbackCheckRunID(ctx, check.ID, checkRunID); err != nil {
		return err
	}
	check.FeedbackCheckRunID = checkRunID
	return nil
}

func feedbackCheckName(check *CheckCompletion) string {
	parts := []string{"武陵监听", feedbackProvider(check)}
	if name := compactCheckLabel(check.Name); name != "" {
		parts = append(parts, name)
	}
	value := strings.Join(parts, " / ")
	runes := []rune(value)
	if len(runes) <= feedbackCheckNameLimit {
		return value
	}
	return string(runes[:feedbackCheckNameLimit-1]) + "…"
}

func feedbackProvider(check *CheckCompletion) string {
	if check.Source == "workflow_run" || strings.EqualFold(check.Provider, "github-actions") {
		return "GitHub Actions"
	}
	if provider := compactCheckLabel(check.Provider); provider != "" {
		return provider
	}
	return "External Checks"
}

func compactCheckLabel(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func feedbackConclusion(conclusion string) string {
	switch conclusion {
	case "action_required", "cancelled", "failure", "neutral", "success", "skipped", "stale", "timed_out":
		return conclusion
	default:
		// Unknown and provider-specific terminal states fail closed so a required
		// branch-protection check can never become green accidentally.
		return "failure"
	}
}

func (p *Processor) persistCheckCompletion(
	ctx context.Context,
	link *RepoLink,
	check CheckCompletion,
) (*CheckCompletion, error) {
	if p.Checks == nil || p.Checks.Pool == nil {
		return nil, fmt.Errorf("github check store is not configured")
	}
	if p.Notifications == nil {
		return p.Checks.Upsert(ctx, check)
	}
	tx, err := p.Checks.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin github check completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	saved, err := p.Checks.UpsertTx(ctx, tx, check)
	if err != nil {
		return nil, err
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
	if err := p.Notifications.PublishTx(ctx, tx, notification.Event{
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
		return nil, fmt.Errorf("enqueue github check notification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit github check completion: %w", err)
	}
	return saved, nil
}

func timeOrNow(value *time.Time) time.Time {
	if value == nil || value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}
