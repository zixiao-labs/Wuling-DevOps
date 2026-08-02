package githubwebhook

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/zixiao-labs/wuling-devops/internal/githubapp"
	"github.com/zixiao-labs/wuling-devops/internal/githttp"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinetrigger"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
)

// Processor handles verified webhook events after the MVP accept path.
type Processor struct {
	AppID   int64
	App     *githubapp.Client
	Links   *LinkStore
	Layout  *repostore.Layout
	Trigger *pipelinetrigger.Service
	// PublicBaseURL is used as check-run details_url prefix when non-empty.
	PublicBaseURL string
}

// Handle dispatches by X-GitHub-Event.
func (p *Processor) Handle(ec EventContext) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch ec.Event {
	case "installation":
		return p.onInstallation(ctx, ec)
	case "installation_repositories":
		return p.onInstallationRepos(ctx, ec)
	case "push":
		return p.onPush(ctx, ec)
	case "pull_request":
		return p.onPullRequest(ctx, ec)
	case "check_suite":
		return p.onCheckSuite(ctx, ec)
	case "check_run":
		return p.onCheckRun(ctx, ec)
	case "repository":
		return p.onRepository(ctx, ec)
	default:
		ec.Log.Info("github-webhook: unhandled event")
		return nil
	}
}

func (p *Processor) onInstallation(ctx context.Context, ec EventContext) error {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repositories []struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			FullName string `json:"full_name"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(ec.Body, &payload); err != nil {
		return err
	}
	switch payload.Action {
	case "deleted", "suspend":
		return p.Links.DeactivateByInstallation(ctx, payload.Installation.ID)
	case "created", "unsuspend", "new_permissions_accepted":
		for _, r := range payload.Repositories {
			owner, name := r.Owner.Login, r.Name
			if owner == "" || name == "" {
				owner, name = splitFullName(r.FullName)
			}
			if owner == "" {
				continue
			}
			link, err := p.Links.GetByFullName(ctx, owner, name)
			if err != nil || link == nil {
				continue
			}
			_, _ = p.Links.Upsert(ctx, RepoLink{
				ID: link.ID, InstallationID: payload.Installation.ID,
				Owner: owner, Name: name,
				OrgID: link.OrgID, ProjectID: link.ProjectID, RepoID: link.RepoID,
			})
		}
	}
	return nil
}

func (p *Processor) onInstallationRepos(ctx context.Context, ec EventContext) error {
	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		RepositoriesRemoved []struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repositories_removed"`
		RepositoriesAdded []struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repositories_added"`
	}
	if err := json.Unmarshal(ec.Body, &payload); err != nil {
		return err
	}
	for _, r := range payload.RepositoriesRemoved {
		owner, name := r.Owner.Login, r.Name
		if owner == "" {
			owner, name = splitFullName(r.FullName)
		}
		_ = p.Links.DeactivateRepo(ctx, owner, name)
	}
	for _, r := range payload.RepositoriesAdded {
		owner, name := r.Owner.Login, r.Name
		if owner == "" {
			owner, name = splitFullName(r.FullName)
		}
		link, err := p.Links.GetByFullName(ctx, owner, name)
		if err != nil || link == nil {
			ec.Log.Info("github-webhook: added repo not linked in Wuling", "repo", owner+"/"+name)
			continue
		}
		_, _ = p.Links.Upsert(ctx, RepoLink{
			ID: link.ID, InstallationID: payload.Installation.ID,
			Owner: owner, Name: name,
			OrgID: link.OrgID, ProjectID: link.ProjectID, RepoID: link.RepoID,
		})
	}
	return nil
}

func (p *Processor) onRepository(ctx context.Context, ec EventContext) error {
	var payload struct {
		Action     string `json:"action"`
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
	if payload.Action == "renamed" || payload.Action == "transferred" || payload.Action == "deleted" {
		owner, name := payload.Repository.Owner.Login, payload.Repository.Name
		if owner == "" {
			owner, name = splitFullName(payload.Repository.FullName)
		}
		if payload.Action == "deleted" {
			return p.Links.DeactivateRepo(ctx, owner, name)
		}
		ec.Log.Info("github-webhook: repository renamed/transferred — re-bind the Wuling link manually",
			"repo", owner+"/"+name, "action", payload.Action)
	}
	return nil
}

func (p *Processor) onPush(ctx context.Context, ec EventContext) error {
	var payload struct {
		Ref        string `json:"ref"`
		Before     string `json:"before"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repository"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(ec.Body, &payload); err != nil {
		return err
	}
	if payload.Deleted || !strings.HasPrefix(payload.Ref, "refs/heads/") {
		return nil
	}
	owner, name := payload.Repository.Owner.Login, payload.Repository.Name
	if owner == "" {
		owner, name = splitFullName(payload.Repository.FullName)
	}
	link, err := p.Links.GetByFullName(ctx, owner, name)
	if err != nil {
		return err
	}
	if link == nil {
		ec.Log.Info("github-webhook: push for unlinked repo", "repo", owner+"/"+name)
		return nil
	}
	instID := payload.Installation.ID
	if instID == 0 {
		instID = link.InstallationID
	}
	// Match pull_request / check_suite: without an App client we cannot fetch.
	// Returning an error would 500 + ReleaseClaim and cause infinite redelivery.
	if p.App == nil {
		ec.Log.Info("github-webhook: push skipped — app client not configured", "repo", owner+"/"+name)
		return nil
	}
	token, err := p.App.InstallationToken(instID)
	if err != nil {
		return err
	}
	repoPath := p.Layout.Path(link.OrgID, link.ProjectID, link.RepoID)
	remote := githubapp.CloneURL(token, owner, name)
	if err := FetchMirror(repoPath, remote); err != nil {
		return err
	}
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")
	before := payload.Before
	if before == "0000000000000000000000000000000000000000" {
		before = ""
	}
	if p.Trigger != nil {
		p.Trigger.OnPush(link.RepoID, link.ProjectID, link.OrgID, repoPath, []githttp.RefUpdate{{
			Branch: branch, OldOID: before, NewOID: payload.After,
		}})
	}
	return nil
}

func (p *Processor) onPullRequest(ctx context.Context, ec EventContext) error {
	var payload struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Head struct {
				SHA string `json:"sha"`
				Ref string `json:"ref"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
			Title string `json:"title"`
		} `json:"pull_request"`
		Repository struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"repository"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(ec.Body, &payload); err != nil {
		return err
	}
	switch payload.Action {
	case "opened", "synchronize", "reopened":
	default:
		return nil
	}
	owner, name := payload.Repository.Owner.Login, payload.Repository.Name
	if owner == "" {
		owner, name = splitFullName(payload.Repository.FullName)
	}
	link, err := p.Links.GetByFullName(ctx, owner, name)
	if err != nil || link == nil {
		if link == nil {
			ec.Log.Info("github-webhook: PR for unlinked repo", "repo", owner+"/"+name)
		}
		return err
	}
	instID := payload.Installation.ID
	if instID == 0 {
		instID = link.InstallationID
	}
	if p.App == nil || p.Trigger == nil {
		return nil
	}
	token, err := p.App.InstallationToken(instID)
	if err != nil {
		return err
	}
	repoPath := p.Layout.Path(link.OrgID, link.ProjectID, link.RepoID)
	remote := githubapp.CloneURL(token, owner, name)
	if err := FetchPRHead(repoPath, remote, payload.Number, payload.PullRequest.Head.SHA); err != nil {
		return err
	}
	p.Trigger.OnPullRequest(link.RepoID, link.ProjectID, link.OrgID, repoPath, pipelinetrigger.PullRequestEvent{
		Number:        payload.Number,
		HeadSHA:       payload.PullRequest.Head.SHA,
		BaseBranch:    payload.PullRequest.Base.Ref,
		HeadBranch:    payload.PullRequest.Head.Ref,
		CommitMessage: payload.PullRequest.Title,
	})
	return nil
}

func (p *Processor) onCheckSuite(ctx context.Context, ec EventContext) error {
	var payload struct {
		Action     string `json:"action"`
		CheckSuite struct {
			HeadSHA string `json:"head_sha"`
		} `json:"check_suite"`
		Repository struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			FullName string `json:"full_name"`
		} `json:"repository"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(ec.Body, &payload); err != nil {
		return err
	}
	if payload.Action != "requested" && payload.Action != "rerequested" {
		return nil
	}
	owner, name := payload.Repository.Owner.Login, payload.Repository.Name
	if owner == "" {
		owner, name = splitFullName(payload.Repository.FullName)
	}
	link, err := p.Links.GetByFullName(ctx, owner, name)
	if err != nil || link == nil || p.App == nil {
		return err
	}
	instID := payload.Installation.ID
	if instID == 0 {
		instID = link.InstallationID
	}
	token, err := p.App.InstallationToken(instID)
	if err != nil {
		return err
	}
	details := strings.TrimRight(p.PublicBaseURL, "/")
	_, err = p.App.CreateCheckRun(token, owner, name, githubapp.CreateCheckRunRequest{
		Name:       "武陵 CI",
		HeadSHA:    payload.CheckSuite.HeadSHA,
		Status:     "queued",
		DetailsURL: details,
		Output: &githubapp.CheckOutput{
			Title:   "武陵 CI",
			Summary: "Queued — waiting for Wuling pipeline runs.",
		},
	})
	return err
}

func (p *Processor) onCheckRun(ctx context.Context, ec EventContext) error {
	var payload struct {
		Action   string `json:"action"`
		CheckRun struct {
			ID         int64  `json:"id"`
			HeadSHA    string `json:"head_sha"`
			ExternalID string `json:"external_id"`
			App        struct {
				ID int64 `json:"id"`
			} `json:"app"`
			Name string `json:"name"`
		} `json:"check_run"`
		RequestedAction *struct {
			Identifier string `json:"identifier"`
		} `json:"requested_action"`
		Repository struct {
			Name  string `json:"name"`
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			FullName string `json:"full_name"`
		} `json:"repository"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(ec.Body, &payload); err != nil {
		return err
	}
	// Broadcast filter — only handle our App's check runs.
	if p.AppID != 0 && payload.CheckRun.App.ID != p.AppID {
		ec.Log.Info("github-webhook: ignoring check_run from other app",
			"app_id", payload.CheckRun.App.ID)
		return nil
	}
	switch payload.Action {
	case "rerequested", "requested_action":
		ec.Log.Info("github-webhook: check_run re-request",
			"action", payload.Action,
			"identifier", func() string {
				if payload.RequestedAction != nil {
					return payload.RequestedAction.Identifier
				}
				return ""
			}(),
			"head_sha", payload.CheckRun.HeadSHA,
		)
		// Re-queue a queued check run so the operator sees activity; full
		// pipeline re-run wiring lands with external_id ↔ run_id bookkeeping.
		owner, name := payload.Repository.Owner.Login, payload.Repository.Name
		if owner == "" {
			owner, name = splitFullName(payload.Repository.FullName)
		}
		link, err := p.Links.GetByFullName(ctx, owner, name)
		if err != nil || link == nil || p.App == nil {
			return err
		}
		instID := payload.Installation.ID
		if instID == 0 {
			instID = link.InstallationID
		}
		token, err := p.App.InstallationToken(instID)
		if err != nil {
			return err
		}
		return p.App.UpdateCheckRun(token, owner, name, payload.CheckRun.ID, githubapp.UpdateCheckRunRequest{
			Status: "queued",
			Output: &githubapp.CheckOutput{
				Title:   "武陵 CI",
				Summary: "Re-queued from GitHub Checks UI.",
			},
		})
	default:
		return nil
	}
}

func splitFullName(full string) (owner, name string) {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// ReleaseClaim deletes a delivery row so GitHub redelivery can retry after a
// process failure.
func (s *Store) ReleaseClaim(ctx context.Context, deliveryID string) error {
	if deliveryID == "" || s == nil {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM github_webhook_deliveries WHERE delivery_id = $1`, deliveryID)
	return err
}
