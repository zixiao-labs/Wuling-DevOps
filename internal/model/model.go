// Package model holds plain DTOs shared across domain packages and the HTTP
// layer. Keeping them in one place avoids cyclic imports between sibling
// domain packages (e.g. repo handlers needing User and Project shapes).
package model

import (
	"time"

	"github.com/google/uuid"
)

// User is a public-facing user representation.
type User struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name"`
	IsAdmin     bool      `json:"is_admin"`
	IsActive    bool      `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
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
