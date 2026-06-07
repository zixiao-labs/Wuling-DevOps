// Package model holds plain DTOs shared across domain packages and the HTTP
// layer. Keeping them in one place avoids cyclic imports between sibling
// domain packages (e.g. repo handlers needing User and Project shapes).
package model

import (
	"time"

	"github.com/google/uuid"
)

// Approval status values for User.ApprovalStatus.
const (
	UserApprovalPending  = "pending"
	UserApprovalApproved = "approved"
	UserApprovalRejected = "rejected"
)

// User is a public-facing user representation.
//
// AvatarURL is a derived field: empty when the user has not uploaded a custom
// avatar (frontends fall back to a deterministic initials tile), and
// "/api/v1/users/{username}/avatar?v=<unix>" when they have. The query-string
// version doubles as a cache-buster on every upload.
type User struct {
	ID             uuid.UUID  `json:"id"`
	Username       string     `json:"username"`
	Email          string     `json:"email,omitempty"`
	DisplayName    string     `json:"display_name"`
	AvatarURL      string     `json:"avatar_url,omitempty"`
	IsAdmin        bool       `json:"is_admin"`
	IsActive       bool       `json:"is_active"`
	ApprovalStatus string     `json:"approval_status"`
	ApprovalNote   string     `json:"approval_note,omitempty"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
	GithubLogin    string     `json:"github_login,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Org is the public org shape.
type Org struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	IsPersonal  bool      `json:"is_personal"`
	CreatedAt   time.Time `json:"created_at"`
}

// OrgMember is the public shape of one row in an org's member list. The user's
// avatar URL is embedded so a member listing can render without a second
// round-trip per row.
type OrgMember struct {
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email,omitempty"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joined_at"`
}

// OrgInvitation is the public shape of a magic-link invitation row. Token is
// only populated on the create-invitation response (the raw token is shown
// once); subsequent reads omit it.
type OrgInvitation struct {
	ID            uuid.UUID  `json:"id"`
	OrgID         uuid.UUID  `json:"org_id"`
	OrgSlug       string     `json:"org_slug,omitempty"`
	OrgDisplayName string    `json:"org_display_name,omitempty"`
	Inviter       *UserRef   `json:"inviter,omitempty"`
	InviteeUserID *uuid.UUID `json:"invitee_user_id,omitempty"`
	InviteeEmail  string     `json:"invitee_email,omitempty"`
	Role          string     `json:"role"`
	Status        string     `json:"status"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	AcceptedAt    *time.Time `json:"accepted_at,omitempty"`
	// Token is the raw, un-hashed invitation token. Non-empty ONLY on the
	// create-invitation response — every other endpoint returns the empty
	// string, since we only store the HMAC of the raw token.
	Token string `json:"token,omitempty"`
}

// Project is the public project shape.
type Project struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	Visibility  string    `json:"visibility"`
	CreatedAt   time.Time `json:"created_at"`
}

// Repo is the public repo shape.
type Repo struct {
	ID            uuid.UUID `json:"id"`
	ProjectID     uuid.UUID `json:"project_id"`
	Slug          string    `json:"slug"`
	DisplayName   string    `json:"display_name"`
	Description   string    `json:"description"`
	DefaultBranch string    `json:"default_branch"`
	Visibility    string    `json:"visibility"`
	IsEmpty       bool      `json:"is_empty"`
	SizeBytes     int64     `json:"size_bytes"`
	CreatedAt     time.Time `json:"created_at"`
}

// AccessTokenView is returned at creation; the raw token is only ever shown once.
type AccessTokenView struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// Token is non-empty only on the create response.
	Token string `json:"token,omitempty"`
}

// Label is the public shape of a project-scoped issue label.
type Label struct {
	ID          uuid.UUID `json:"id"`
	ProjectID   uuid.UUID `json:"project_id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// UserRef is a thin reference to a user, used inside issue/comment payloads
// so the API can return author/assignee identity without a second round-trip.
// Email is intentionally omitted from this shape.
type UserRef struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
}

// Issue is the public shape of an issue. Labels and assignees are eagerly
// embedded so the most common views don't need follow-up calls.
type Issue struct {
	ID         uuid.UUID  `json:"id"`
	ProjectID  uuid.UUID  `json:"project_id"`
	Number     int64      `json:"number"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	State      string     `json:"state"`
	Author     *UserRef   `json:"author,omitempty"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`
	ClosedBy   *UserRef   `json:"closed_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Labels     []Label    `json:"labels"`
	Assignees  []UserRef  `json:"assignees"`
	CommentCnt int64      `json:"comment_count"`
}

// IssueComment is the public shape of a comment on an issue.
type IssueComment struct {
	ID        uuid.UUID `json:"id"`
	IssueID   uuid.UUID `json:"issue_id"`
	Author    *UserRef  `json:"author,omitempty"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MergeRequest is the public shape of a merge request. State transitions are
// open -> merged | closed; reopen flips closed back to open. The merge_*
// fields are only populated once state is "merged".
type MergeRequest struct {
	ID               uuid.UUID  `json:"id"`
	RepoID           uuid.UUID  `json:"repo_id"`
	ProjectID        uuid.UUID  `json:"project_id"`
	Number           int64      `json:"number"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	State            string     `json:"state"`
	SourceRef        string     `json:"source_ref"`
	TargetRef        string     `json:"target_ref"`
	SourceOIDAtOpen  string     `json:"source_oid_at_open"`
	TargetOIDAtOpen  string     `json:"target_oid_at_open"`
	MergeStrategy    *string    `json:"merge_strategy,omitempty"`
	MergeCommitOID   *string    `json:"merge_commit_oid,omitempty"`
	Author           *UserRef   `json:"author,omitempty"`
	MergedBy         *UserRef   `json:"merged_by,omitempty"`
	ClosedBy         *UserRef   `json:"closed_by,omitempty"`
	MergedAt         *time.Time `json:"merged_at,omitempty"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	CommentCnt       int64      `json:"comment_count"`
	ReviewCnt        int64      `json:"review_count"`
}

// MRComment is the public shape of a comment on a merge request.
type MRComment struct {
	ID        uuid.UUID `json:"id"`
	MRID      uuid.UUID `json:"mr_id"`
	Author    *UserRef  `json:"author,omitempty"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MRReview is the public shape of a review event on a merge request. State
// is one of "approved", "changes_requested", or "commented".
type MRReview struct {
	ID        uuid.UUID `json:"id"`
	MRID      uuid.UUID `json:"mr_id"`
	Author    *UserRef  `json:"author,omitempty"`
	State     string    `json:"state"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// MRDiffEntry is one file's worth of diff between two commits, used by the
// /merge-requests/{n}/diff endpoint. Patch is empty unless the caller asked
// for ?include=patch — keeping it off by default keeps responses small.
type MRDiffEntry struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"` // added | modified | deleted | renamed | copied | typechange
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
}

// WikiPage is the listing shape for a single Markdown page in the wiki tree.
// Path is forward-slash separated and ends in ".md". UpdatedAt is the time
// of the most recent commit that touched the file (when available).
type WikiPage struct {
	Path      string     `json:"path"`
	Size      int64      `json:"size"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// WikiPageContent is the response for fetching a single page.
type WikiPageContent struct {
	Path      string `json:"path"`
	Raw       string `json:"raw"`
	HTML      string `json:"html"`
	CommitOID string `json:"commit_oid"`
}

// SSHKey is the public shape of a stored SSH public key.
type SSHKey struct {
	ID          uuid.UUID  `json:"id"`
	Title       string     `json:"title"`
	Fingerprint string     `json:"fingerprint"`
	PublicKey   string     `json:"public_key"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

// ActivityDay is one row in the per-day activity rollup. Date is YYYY-MM-DD.
type ActivityDay struct {
	Date          string `json:"date"`
	IssuesOpened  int64  `json:"issues_opened"`
	IssuesClosed  int64  `json:"issues_closed"`
	MRsOpened     int64  `json:"mrs_opened"`
	MRsMerged     int64  `json:"mrs_merged"`
	Commits       int64  `json:"commits"`
}

// ContributorStat is a per-author commit count for the contributors endpoint.
type ContributorStat struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Commits int64  `json:"commits"`
}

// LanguageStats summarises the byte and file counts per detected language
// across the latest tree of a repo. Truncated is true when the walker hit
// MaxLanguageBlobs before reaching every blob — the counts in that case are
// a lower bound, not an exact total.
type LanguageStats struct {
	Bytes     map[string]int64 `json:"bytes"`
	Files     map[string]int64 `json:"files"`
	Truncated bool             `json:"truncated,omitempty"`
}

// ----------------------------------------------------------------------------
// Pipelines + Secrets + Runners (Stage 1). See docs/pipelines.md.
// ----------------------------------------------------------------------------

// Resource tiers — the abstract machine size a job needs and a runner offers.
// Mapped to concrete CPU/memory/storage per provider in runner-config.yaml.
const (
	TierLow    = "low"
	TierMedium = "medium"
	TierHigh   = "high"
)

// Runner operating systems. The OS drives the runner's execution backend
// (Linux/Windows containers vs a host shell) and the autoscaler's bootstrap
// (cloud-init vs PowerShell). macOS is manual-registration only — never
// autoscaled (Apple licensing requires Apple hardware).
const (
	OSLinux   = "linux"
	OSWindows = "windows"
	OSMacOS   = "macos"
)

// Secret is the metadata-only shape of a stored secret. The value is NEVER
// serialized to JSON — only the name and scope are exposed. ProjectID is nil
// for org-scoped secrets.
type Secret struct {
	ID        uuid.UUID  `json:"id"`
	OrgID     uuid.UUID  `json:"org_id"`
	ProjectID *uuid.UUID `json:"project_id,omitempty"`
	Scope     string     `json:"scope"` // "org" | "project"
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Runner is the public shape of a registered execution agent. Token is
// populated ONLY on the register/create response (shown once, like a PAT).
type Runner struct {
	ID           uuid.UUID  `json:"id"`
	OrgID        uuid.UUID  `json:"org_id"`
	Name         string     `json:"name"`
	Labels       []string   `json:"labels"`
	ResourceTier string     `json:"resource_tier"`
	OS           string     `json:"os"`       // linux|windows|macos
	Provider     string     `json:"provider"` // static|aliyun|aws|proxmox|vcenter
	PoolName     string     `json:"pool_name,omitempty"`
	Ephemeral    bool       `json:"ephemeral"`
	Status       string     `json:"status"` // offline|idle|busy
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	LastJobAt    *time.Time `json:"last_job_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	Token        string     `json:"token,omitempty"`
}

// PipelineRun is one execution of one workflow file for a commit/event. Jobs
// is populated on the detail endpoint, omitted from list responses.
type PipelineRun struct {
	ID            uuid.UUID     `json:"id"`
	OrgID         uuid.UUID     `json:"org_id"`
	ProjectID     uuid.UUID     `json:"project_id"`
	RepoID        uuid.UUID     `json:"repo_id"`
	Number        int64         `json:"number"`
	WorkflowPath  string        `json:"workflow_path"`
	WorkflowName  string        `json:"workflow_name"`
	Event         string        `json:"event"` // push|pull_request|manual
	GitRef        string        `json:"git_ref"`
	CommitSHA     string        `json:"commit_sha"`
	CommitMessage string        `json:"commit_message"`
	Status        string        `json:"status"` // queued|running|success|failed|canceled
	TriggeredBy   *UserRef      `json:"triggered_by,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	StartedAt     *time.Time    `json:"started_at,omitempty"`
	FinishedAt    *time.Time    `json:"finished_at,omitempty"`
	Jobs          []PipelineJob `json:"jobs,omitempty"`
}

// PipelineJob is a job within a run. Steps is populated on the detail endpoint.
type PipelineJob struct {
	ID           uuid.UUID      `json:"id"`
	RunID        uuid.UUID      `json:"run_id"`
	Name         string         `json:"name"`
	RunsOn       []string       `json:"runs_on"`
	ResourceTier string         `json:"resource_tier"`
	Needs        []string       `json:"needs"`
	Status       string         `json:"status"`
	RunnerID     *uuid.UUID     `json:"runner_id,omitempty"`
	Attempt      int            `json:"attempt"`
	LogSize      int64          `json:"log_size"`
	QueuedAt     time.Time      `json:"queued_at"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	FinishedAt   *time.Time     `json:"finished_at,omitempty"`
	Steps        []PipelineStep `json:"steps,omitempty"`
}

// PipelineStep is one ordered step within a job. Logs live on disk, not here.
type PipelineStep struct {
	ID         uuid.UUID  `json:"id"`
	JobID      uuid.UUID  `json:"job_id"`
	Number     int        `json:"number"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
