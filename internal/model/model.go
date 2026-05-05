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
