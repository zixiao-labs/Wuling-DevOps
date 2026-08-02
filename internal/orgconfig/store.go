// Package orgconfig resolves an org's GitOps config repo ({org}/config/config
// by default) and reads/writes the files the control plane manages there —
// today just runner-config.yaml.
//
// It deals in RAW BYTES and deliberately does not import internal/autoscale:
// autoscale reads through this package, so parsing must stay on the caller's
// side or we get an import cycle.
package orgconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// RunnerConfigPath is the runner/autoscaler config blob, at the ROOT of the
// config repo's default branch. Mirrors autoscale.ConfigFileName; duplicated
// rather than imported (see package doc). The autoscaler only scans the root
// tree (reconcile.go:308-317), so a nested copy would be silently ignored.
const RunnerConfigPath = "runner-config.yaml"

// MaxFileBytes caps a single managed file. Well under httpapi.MaxJSONBodyBytes
// (1 MiB) so an oversized PUT fails here with a useful message rather than on
// the opaque body-size limit.
const MaxFileBytes = 256 << 10

// Store resolves and reads/writes managed files in each org's config repo.
type Store struct {
	Users  *userstore.Store
	Layout *repostore.Layout
	// ProjectSlug/RepoSlug locate the config repo. Wire from
	// config.RunnerConfig.ConfigProject / .ConfigRepo.
	ProjectSlug string
	RepoSlug    string

	// mu serialises the whole read-check-commit sequence. One global mutex,
	// not per-org: writes are human-paced (someone clicking Save) so
	// contention is irrelevant and a lock map is not worth the bookkeeping.
	// Cross-process races are caught by libgit2 — see Write.
	mu sync.Mutex
}

// New returns a Store wired to the given config project/repo slugs.
func New(users *userstore.Store, layout *repostore.Layout, projectSlug, repoSlug string) *Store {
	return &Store{
		Users:       users,
		Layout:      layout,
		ProjectSlug: projectSlug,
		RepoSlug:    repoSlug,
	}
}

// File is one managed file plus the git coordinates callers need for
// optimistic concurrency and for rendering "last changed by".
type File struct {
	Content   []byte
	BlobSHA   string // "" when the file does not exist
	CommitSHA string // tip of the config repo default branch; "" when no commits
	Branch    string
	UpdatedAt time.Time // committer time of the tip commit
	UpdatedBy string    // committer name of the tip commit
}

// Exists reports whether the managed file is present in the config repo.
func (f *File) Exists() bool { return f != nil && f.BlobSHA != "" }

// Locate resolves the config project + repo. Returns all-nil with a nil error
// when either does not exist yet — mirrors the "org hasn't opted in" contract
// of autoscale.Reconciler.loadOrgConfig (reconcile.go:282-295).
func (s *Store) Locate(ctx context.Context, orgID uuid.UUID) (*model.Project, *model.Repo, string, error) {
	project, err := s.Users.GetProjectBySlug(ctx, orgID, s.ProjectSlug)
	if err != nil {
		if isNotFound(err) {
			return nil, nil, "", nil
		}
		return nil, nil, "", err
	}
	repo, err := s.Users.GetRepoBySlug(ctx, project.ID, s.RepoSlug)
	if err != nil {
		if isNotFound(err) {
			return project, nil, "", nil
		}
		return nil, nil, "", err
	}
	return project, repo, s.Layout.Path(orgID, project.ID, repo.ID), nil
}

// Read returns name from the tip of the config repo's default branch. A
// missing project, repo, commit, or file all yield a non-nil *File with
// Exists()==false, so callers never distinguish four flavours of absence.
func (s *Store) Read(ctx context.Context, orgID uuid.UUID, name string) (*File, error) {
	project, repo, repoPath, err := s.Locate(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if project == nil || repo == nil {
		return &File{}, nil
	}
	return s.readAt(repoPath, repo.DefaultBranch, name)
}

// WriteParams holds inputs to Write.
type WriteParams struct {
	OrgID   uuid.UUID
	Name    string
	Content []byte
	Message string
	Author  git.Author
	// BaseBlobSHA is the optimistic-concurrency precondition. nil disables the
	// check (the HTTP layer never passes nil). "" asserts the file is absent.
	BaseBlobSHA *string
	// EnsureRepo creates the config project/repo when missing. Caller must
	// have already verified the actor may create projects.
	EnsureRepo bool
}

// WriteResult is the outcome of a successful Write.
type WriteResult struct {
	File           *File
	CreatedProject bool
	CreatedRepo    bool
	Unchanged      bool
	ProjectID      uuid.UUID
	RepoID         uuid.UUID
}

// Write commits name to the config repo's default branch.
func (s *Store) Write(ctx context.Context, p WriteParams) (*WriteResult, error) {
	if len(p.Content) > MaxFileBytes {
		return nil, apperr.Validation(fmt.Sprintf("%s exceeds %d bytes", p.Name, MaxFileBytes), nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	project, repo, _, err := s.Locate(ctx, p.OrgID)
	if err != nil {
		return nil, err
	}

	res := &WriteResult{}
	if project == nil {
		if !p.EnsureRepo {
			return nil, apperr.NotFound("config project")
		}
		project, err = s.ensureProject(ctx, p.OrgID)
		if err != nil {
			return nil, err
		}
		res.CreatedProject = true
	}
	if repo == nil {
		if !p.EnsureRepo {
			return nil, apperr.NotFound("config repo")
		}
		repo, err = s.ensureRepo(ctx, p.OrgID, project.ID)
		if err != nil {
			return nil, err
		}
		res.CreatedRepo = true
	}
	res.ProjectID, res.RepoID = project.ID, repo.ID
	repoPath := s.Layout.Path(p.OrgID, project.ID, repo.ID)

	if !git.Exists(repoPath) {
		if gerr := git.InitBare(repoPath, repo.DefaultBranch); gerr != nil {
			return nil, apperr.Wrap(apperr.CodeInternal, "init config repo", gerr)
		}
	}

	cur, err := s.readAt(repoPath, repo.DefaultBranch, p.Name)
	if err != nil {
		return nil, err
	}

	if p.BaseBlobSHA != nil && !strings.EqualFold(*p.BaseBlobSHA, cur.BlobSHA) {
		return nil, apperr.WithDetails(
			apperr.Conflict(p.Name+" changed since it was read; reload and re-apply your edit"),
			map[string]any{"current_blob_sha": cur.BlobSHA, "base_blob_sha": *p.BaseBlobSHA})
	}
	if cur.Exists() && bytes.Equal(cur.Content, p.Content) {
		res.File, res.Unchanged = cur, true
		return res, nil
	}

	ref := "refs/heads/" + repo.DefaultBranch
	if _, gerr := git.CommitFile(repoPath, ref, p.Name, p.Content, p.Author, p.Message); gerr != nil {
		if git.IsStaleTip(gerr) {
			return nil, apperr.Conflict("the config repo advanced during the write; retry")
		}
		if errors.Is(gerr, git.ErrCGOUnsupported) {
			return nil, apperr.New(apperr.CodeUnavailable, "this build has no libgit2 backend; GitOps writes are unavailable")
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "commit config", gerr)
	}

	_ = s.Users.MarkRepoNotEmpty(ctx, repo.ID)
	if size, serr := repostore.DirSize(repoPath); serr == nil {
		_ = s.Users.UpdateRepoSize(ctx, repo.ID, size)
	}

	res.File, err = s.readAt(repoPath, repo.DefaultBranch, p.Name)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Invalidate drops any cached copy of an org's config. There is NO cache today
// (autoscale.Reconciler re-reads from git every tick), so this is a documented
// no-op: the write path calls the hook now so adding a cache later stays local
// to this package.
func (s *Store) Invalidate(orgID uuid.UUID) {}

func (s *Store) readAt(repoPath, branch, name string) (*File, error) {
	f := &File{Branch: branch}
	commitSHA, gerr := git.Resolve(repoPath, branch)
	if gerr != nil {
		if git.IsNotFound(gerr) {
			return f, nil
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "resolve config head", gerr)
	}
	f.CommitSHA = commitSHA
	if commits, cerr := git.Log(repoPath, commitSHA, 1); cerr == nil && len(commits) == 1 {
		f.UpdatedAt, f.UpdatedBy = commits[0].Committer.When, commits[0].Committer.Name
	}
	entries, gerr := git.ReadTree(repoPath, commitSHA)
	if gerr != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "read config tree", gerr)
	}
	for _, e := range entries {
		if e.Kind == "blob" && e.Name == name {
			f.BlobSHA = e.OID
			break
		}
	}
	if f.BlobSHA == "" {
		return f, nil
	}
	blob, gerr := git.ReadBlob(repoPath, f.BlobSHA)
	if gerr != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "read config blob", gerr)
	}
	f.Content = blob.Data
	return f, nil
}

func (s *Store) ensureProject(ctx context.Context, orgID uuid.UUID) (*model.Project, error) {
	p, err := s.Users.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID:       orgID,
		Slug:        s.ProjectSlug,
		DisplayName: s.ProjectSlug,
		Description: "GitOps configuration for this organization (runner-config.yaml).",
		Visibility:  "private",
	})
	if err == nil {
		return p, nil
	}
	if ae := apperr.As(err); ae != nil && ae.Code == apperr.CodeConflict {
		return s.Users.GetProjectBySlug(ctx, orgID, s.ProjectSlug)
	}
	return nil, err
}

func (s *Store) ensureRepo(ctx context.Context, orgID, projectID uuid.UUID) (*model.Repo, error) {
	r, err := s.Users.CreateRepo(ctx, userstore.CreateRepoParams{
		ProjectID:     projectID,
		Slug:          s.RepoSlug,
		DisplayName:   s.RepoSlug,
		Description:   "Runner / autoscaler configuration.",
		DefaultBranch: "main",
		Visibility:    "private",
	})
	if err != nil {
		if ae := apperr.As(err); ae != nil && ae.Code == apperr.CodeConflict {
			return s.Users.GetRepoBySlug(ctx, projectID, s.RepoSlug)
		}
		return nil, err
	}
	if gerr := git.InitBare(s.Layout.Path(orgID, projectID, r.ID), r.DefaultBranch); gerr != nil {
		if derr := s.Users.DeleteRepo(ctx, r.ID); derr != nil {
			return nil, apperr.Wrap(apperr.CodeInternal, "init config repo (and rollback failed)", gerr)
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "init config repo", gerr)
	}
	return r, nil
}

func isNotFound(err error) bool {
	if ae := apperr.As(err); ae != nil {
		return ae.Code == apperr.CodeNotFound
	}
	return false
}
