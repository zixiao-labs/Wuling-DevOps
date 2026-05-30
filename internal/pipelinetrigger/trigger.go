// Package pipelinetrigger turns a git push into pipeline runs. It implements
// githttp.PushTrigger: for each branch a push moved, it discovers
// .wuling/workflows/*.yml at the new tip and starts a run for every workflow
// whose `on.push` (with optional branch filter) matches.
//
// It deliberately lives outside githttp so the git transport stays free of the
// pipeline store dependency; server.go wires the two together.
package pipelinetrigger

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/githttp"
	"github.com/zixiao-labs/wuling-devops/internal/pipeline"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
)

// Service implements githttp.PushTrigger.
type Service struct {
	Pipelines   *pipelinestore.Store
	Log         *slog.Logger
	DefaultTier string
}

// OnPush handles branch updates asynchronously so the push response is never
// blocked on workflow discovery / run creation.
func (s *Service) OnPush(repoID, projectID, orgID uuid.UUID, repoPath string, updates []githttp.RefUpdate) {
	go s.handle(repoID, projectID, orgID, repoPath, updates)
}

func (s *Service) handle(repoID, projectID, orgID uuid.UUID, repoPath string, updates []githttp.RefUpdate) {
	// Detached from the request: discovery + inserts shouldn't be cut short by
	// the push connection closing.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, u := range updates {
		discovered, err := pipeline.Discover(repoPath, u.NewOID)
		if err != nil {
			s.log().Warn("ci-trigger: discover failed", "repo_id", repoID, "branch", u.Branch, "err", err)
			continue
		}
		for _, dw := range discovered {
			if dw.ParseErr != nil {
				// A broken workflow shouldn't fail the push; surface it in logs.
				// (A future stage can record a "failed to parse" run so the
				// author sees it in the UI.)
				s.log().Warn("ci-trigger: workflow parse error",
					"repo_id", repoID, "path", dw.Path, "err", dw.ParseErr)
				continue
			}
			if !dw.Workflow.MatchEvent("push", u.Branch) {
				continue
			}
			_, err := s.Pipelines.CreateRun(ctx, pipelinestore.CreateRunParams{
				OrgID:         orgID,
				ProjectID:     projectID,
				RepoID:        repoID,
				WorkflowPath:  dw.Path,
				Event:         "push",
				GitRef:        "refs/heads/" + u.Branch,
				CommitSHA:     u.NewOID,
				CommitMessage: firstLine(commitMessage(repoPath, u.NewOID)),
				Workflow:      dw.Workflow,
				DefaultTier:   s.DefaultTier,
			})
			if err != nil {
				s.log().Error("ci-trigger: create run failed",
					"repo_id", repoID, "path", dw.Path, "err", err)
			}
		}
	}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func commitMessage(repoPath, sha string) string {
	commits, err := git.Log(repoPath, sha, 1)
	if err != nil || len(commits) == 0 {
		return ""
	}
	return commits[0].Message
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
