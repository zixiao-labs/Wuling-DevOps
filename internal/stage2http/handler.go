// Package stage2http exposes the Stage-2 project planning, test plan,
// repository policy, and artifact catalogue APIs.
package stage2http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/stage2store"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

type Handler struct {
	Users    *userstore.Store
	Stage2   *stage2store.Store
	Verifier *auth.Verifier
	OAT      auth.OATResolver
}

func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.MiddlewareBearer(auth.BearerResolver{JWT: h.Verifier, OAT: h.OAT}, false))
		base := "/orgs/{org_slug}/projects/{project_slug}"

		r.Get(base+"/dashboard", h.dashboard)
		r.Get(base+"/settings", h.getProjectSettings)
		r.Patch(base+"/settings", h.updateProjectSettings)

		r.Get(base+"/iterations", h.listIterations)
		r.Post(base+"/iterations", h.createIteration)
		r.Patch(base+"/iterations/{iteration_id}", h.updateIteration)

		r.Get(base+"/work-items", h.listWorkItems)
		r.Post(base+"/work-items", h.createWorkItem)
		r.Patch(base+"/work-items/{number}", h.updateWorkItem)

		r.Get(base+"/test-plans", h.listTestPlans)
		r.Post(base+"/test-plans", h.createTestPlan)
		r.Get(base+"/test-plans/{plan_id}/suites", h.listTestSuites)
		r.Post(base+"/test-plans/{plan_id}/suites", h.createTestSuite)
		r.Get(base+"/test-plans/{plan_id}/suites/{suite_id}/cases", h.listTestCases)
		r.Post(base+"/test-plans/{plan_id}/suites/{suite_id}/cases", h.createTestCase)
		r.Post(base+"/test-cases/{case_id}/runs", h.recordTestRun)

		r.Get(base+"/packages", h.listPackages)
		r.Post(base+"/packages", h.createPackage)
		r.Get(base+"/packages/{package_id}/versions", h.listVersions)
		r.Post(base+"/packages/{package_id}/versions", h.publishVersion)
		r.Get(base+"/releases", h.listReleases)
		r.Post(base+"/releases", h.createRelease)

		r.Get(base+"/repos/{repo_slug}/settings", h.getRepoSettings)
		r.Patch(base+"/repos/{repo_slug}/settings", h.updateRepoSettings)
	})
}

type projectContext struct {
	ProjectID uuid.UUID
	UserID    uuid.UUID
	Role      string
}

func (h *Handler) resolveProject(r *http.Request) (*projectContext, error) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		return nil, err
	}
	org, err := h.Users.GetOrgBySlug(r.Context(), chi.URLParam(r, "org_slug"))
	if err != nil {
		return nil, err
	}
	role, err := h.Users.MemberRole(r.Context(), org.ID, id.UserID)
	if err != nil {
		return nil, err
	}
	if !auth.CanReadOrg(role) {
		return nil, apperr.NotFound("project")
	}
	project, err := h.Users.GetProjectBySlug(r.Context(), org.ID, chi.URLParam(r, "project_slug"))
	if err != nil {
		return nil, err
	}
	return &projectContext{ProjectID: project.ID, UserID: id.UserID, Role: role}, nil
}

func requireWrite(pc *projectContext) error {
	if !auth.CanWriteRepo(pc.Role) {
		return apperr.Forbidden("developer role or above required")
	}
	return nil
}

func requireManage(pc *projectContext) error {
	if !auth.CanModerateContent(pc.Role) {
		return apperr.Forbidden("maintainer role or above required")
	}
	return nil
}

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, apperr.New(apperr.CodeBadRequest, "invalid "+name)
	}
	return id, nil
}

func parseWorkItemNumber(r *http.Request) (int64, error) {
	n, err := strconv.ParseInt(chi.URLParam(r, "number"), 10, 64)
	if err != nil || n < 1 {
		return 0, apperr.New(apperr.CodeBadRequest, "invalid work item number")
	}
	return n, nil
}

func renderError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	httpapi.RenderError(w, r, err)
	return true
}

// --- dashboard / settings -------------------------------------------------------

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	out, err := h.Stage2.Dashboard(r.Context(), pc.ProjectID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) getProjectSettings(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	out, err := h.Stage2.GetProjectSettings(r.Context(), pc.ProjectID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

type updateProjectSettingsRequest struct {
	ProcessTemplate     *string `json:"process_template,omitempty" validate:"omitempty,oneof=scrum kanban basic"`
	WorkItemPrefix      *string `json:"work_item_prefix,omitempty" validate:"omitempty,max=16,alphanum"`
	IterationLengthDays *int    `json:"iteration_length_days,omitempty" validate:"omitempty,min=1,max=90"`
	Archived            *bool   `json:"archived,omitempty"`
}

func (h *Handler) updateProjectSettings(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireManage(pc)) {
		return
	}
	var req updateProjectSettingsRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	if req.WorkItemPrefix != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.WorkItemPrefix))
		req.WorkItemPrefix = &v
	}
	out, err := h.Stage2.UpdateProjectSettings(r.Context(), pc.ProjectID, stage2store.UpdateProjectSettingsParams{
		ProcessTemplate: req.ProcessTemplate, WorkItemPrefix: req.WorkItemPrefix,
		IterationLengthDays: req.IterationLengthDays, Archived: req.Archived,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// --- iterations ----------------------------------------------------------------

type createIterationRequest struct {
	Name     string `json:"name" validate:"required,min=1,max=128"`
	Goal     string `json:"goal" validate:"max=1024"`
	State    string `json:"state" validate:"omitempty,oneof=planned current closed"`
	StartsAt string `json:"starts_at" validate:"required"`
	EndsAt   string `json:"ends_at" validate:"required"`
}

func (h *Handler) listIterations(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	items, err := h.Stage2.ListIterations(r.Context(), pc.ProjectID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"iterations": items})
}

func (h *Handler) createIteration(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireManage(pc)) {
		return
	}
	var req createIterationRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	starts, err := stage2store.ParseDate(req.StartsAt)
	if renderError(w, r, err) {
		return
	}
	ends, err := stage2store.ParseDate(req.EndsAt)
	if renderError(w, r, err) {
		return
	}
	out, err := h.Stage2.CreateIteration(r.Context(), stage2store.CreateIterationParams{
		ProjectID: pc.ProjectID, Name: req.Name, Goal: req.Goal,
		State: req.State, StartsAt: starts, EndsAt: ends,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

type updateIterationRequest struct {
	Name     *string `json:"name,omitempty" validate:"omitempty,min=1,max=128"`
	Goal     *string `json:"goal,omitempty" validate:"omitempty,max=1024"`
	State    *string `json:"state,omitempty" validate:"omitempty,oneof=planned current closed"`
	StartsAt *string `json:"starts_at,omitempty"`
	EndsAt   *string `json:"ends_at,omitempty"`
}

func (h *Handler) updateIteration(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireManage(pc)) {
		return
	}
	id, err := parseUUIDParam(r, "iteration_id")
	if renderError(w, r, err) {
		return
	}
	var req updateIterationRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	p := stage2store.UpdateIterationParams{Name: req.Name, Goal: req.Goal, State: req.State}
	if req.StartsAt != nil {
		value, err := stage2store.ParseDate(*req.StartsAt)
		if renderError(w, r, err) {
			return
		}
		p.StartsAt = &value
	}
	if req.EndsAt != nil {
		value, err := stage2store.ParseDate(*req.EndsAt)
		if renderError(w, r, err) {
			return
		}
		p.EndsAt = &value
	}
	out, err := h.Stage2.UpdateIteration(r.Context(), pc.ProjectID, id, p)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// --- work items ----------------------------------------------------------------

type createWorkItemRequest struct {
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	IterationID *uuid.UUID `json:"iteration_id,omitempty"`
	AssigneeID  *uuid.UUID `json:"assignee_id,omitempty"`
	Type        string     `json:"type" validate:"omitempty,oneof=epic feature user_story task bug"`
	Title       string     `json:"title" validate:"required,min=1,max=256"`
	Description string     `json:"description" validate:"max=65536"`
	Priority    *int       `json:"priority,omitempty" validate:"omitempty,min=0,max=4"`
	StoryPoints *float64   `json:"story_points,omitempty" validate:"omitempty,min=0"`
	AreaPath    string     `json:"area_path" validate:"max=256"`
}

func (h *Handler) listWorkItems(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	state := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("state")))
	if state != "" && state != "new" && state != "active" && state != "resolved" && state != "closed" {
		renderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid work item state"))
		return
	}
	var iterationID *uuid.UUID
	if raw := r.URL.Query().Get("iteration_id"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if renderError(w, r, func() error {
			if err != nil {
				return apperr.New(apperr.CodeBadRequest, "invalid iteration_id")
			}
			return nil
		}()) {
			return
		}
		iterationID = &parsed
	}
	items, err := h.Stage2.ListWorkItems(r.Context(), pc.ProjectID, state, iterationID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"work_items": items})
}

func (h *Handler) createWorkItem(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	var req createWorkItemRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.CreateWorkItem(r.Context(), stage2store.CreateWorkItemParams{
		ProjectID: pc.ProjectID, ParentID: req.ParentID, IterationID: req.IterationID,
		AssigneeID: req.AssigneeID, AuthorID: pc.UserID, Type: req.Type,
		Title: req.Title, Description: req.Description, Priority: req.Priority,
		StoryPoints: req.StoryPoints, AreaPath: req.AreaPath,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

type updateWorkItemRequest struct {
	ParentID     *uuid.UUID `json:"parent_id,omitempty"`
	IterationID  *uuid.UUID `json:"iteration_id,omitempty"`
	AssigneeID   *uuid.UUID `json:"assignee_id,omitempty"`
	Type         *string    `json:"type,omitempty" validate:"omitempty,oneof=epic feature user_story task bug"`
	Title        *string    `json:"title,omitempty" validate:"omitempty,min=1,max=256"`
	Description  *string    `json:"description,omitempty" validate:"omitempty,max=65536"`
	State        *string    `json:"state,omitempty" validate:"omitempty,oneof=new active resolved closed"`
	Priority     *int       `json:"priority,omitempty" validate:"omitempty,min=0,max=4"`
	StoryPoints  *float64   `json:"story_points,omitempty" validate:"omitempty,min=0"`
	AreaPath     *string    `json:"area_path,omitempty" validate:"omitempty,max=256"`
	BacklogOrder *float64   `json:"backlog_order,omitempty"`
}

func (h *Handler) updateWorkItem(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	number, err := parseWorkItemNumber(r)
	if renderError(w, r, err) {
		return
	}
	var req updateWorkItemRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.UpdateWorkItem(r.Context(), pc.ProjectID, number, stage2store.UpdateWorkItemParams{
		ParentID: req.ParentID, IterationID: req.IterationID, AssigneeID: req.AssigneeID,
		Type: req.Type, Title: req.Title, Description: req.Description, State: req.State,
		Priority: req.Priority, StoryPoints: req.StoryPoints, AreaPath: req.AreaPath,
		BacklogOrder: req.BacklogOrder,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// --- test plans ----------------------------------------------------------------

type createTestPlanRequest struct {
	IterationID *uuid.UUID `json:"iteration_id,omitempty"`
	Name        string     `json:"name" validate:"required,min=1,max=128"`
	Description string     `json:"description" validate:"max=4096"`
}

func (h *Handler) listTestPlans(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	items, err := h.Stage2.ListTestPlans(r.Context(), pc.ProjectID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"test_plans": items})
}

func (h *Handler) createTestPlan(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	var req createTestPlanRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.CreateTestPlan(r.Context(), stage2store.CreateTestPlanParams{
		ProjectID: pc.ProjectID, IterationID: req.IterationID, Name: req.Name,
		Description: req.Description, CreatedBy: pc.UserID,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

type createTestSuiteRequest struct {
	ParentID    *uuid.UUID `json:"parent_id,omitempty"`
	Name        string     `json:"name" validate:"required,min=1,max=128"`
	Description string     `json:"description" validate:"max=4096"`
}

func (h *Handler) listTestSuites(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	planID, err := parseUUIDParam(r, "plan_id")
	if renderError(w, r, err) {
		return
	}
	items, err := h.Stage2.ListTestSuites(r.Context(), pc.ProjectID, planID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"test_suites": items})
}

func (h *Handler) createTestSuite(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	planID, err := parseUUIDParam(r, "plan_id")
	if renderError(w, r, err) {
		return
	}
	var req createTestSuiteRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.CreateTestSuite(r.Context(), stage2store.CreateTestSuiteParams{
		ProjectID: pc.ProjectID, PlanID: planID, ParentID: req.ParentID,
		Name: req.Name, Description: req.Description,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

type createTestCaseRequest struct {
	Title         string          `json:"title" validate:"required,min=1,max=256"`
	Steps         json.RawMessage `json:"steps"`
	Expected      string          `json:"expected" validate:"max=65536"`
	Automation    string          `json:"automation" validate:"omitempty,oneof=manual lightning"`
	AutomationRef string          `json:"automation_ref" validate:"max=512"`
	Priority      *int            `json:"priority,omitempty" validate:"omitempty,min=0,max=4"`
}

func (h *Handler) listTestCases(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	suiteID, err := parseUUIDParam(r, "suite_id")
	if renderError(w, r, err) {
		return
	}
	items, err := h.Stage2.ListTestCases(r.Context(), pc.ProjectID, suiteID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"test_cases": items})
}

func (h *Handler) createTestCase(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	suiteID, err := parseUUIDParam(r, "suite_id")
	if renderError(w, r, err) {
		return
	}
	var req createTestCaseRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.CreateTestCase(r.Context(), pc.ProjectID, stage2store.CreateTestCaseParams{
		SuiteID: suiteID, Title: req.Title, Steps: req.Steps, Expected: req.Expected,
		Automation: req.Automation, AutomationRef: req.AutomationRef,
		Priority: req.Priority, CreatedBy: pc.UserID,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

type recordTestRunRequest struct {
	Status     string `json:"status" validate:"required,oneof=passed failed blocked skipped"`
	DurationMS *int64 `json:"duration_ms,omitempty" validate:"omitempty,min=0"`
	Notes      string `json:"notes" validate:"max=65536"`
}

func (h *Handler) recordTestRun(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	caseID, err := parseUUIDParam(r, "case_id")
	if renderError(w, r, err) {
		return
	}
	var req recordTestRunRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.RecordTestRun(r.Context(), pc.ProjectID, stage2store.RecordTestRunParams{
		TestCaseID: caseID, Status: req.Status, DurationMS: req.DurationMS,
		Notes: req.Notes, RunBy: pc.UserID,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

// --- artifacts / releases ------------------------------------------------------

type createPackageRequest struct {
	Kind        string `json:"kind" validate:"required,oneof=npm pypi cargo docker logos"`
	Name        string `json:"name" validate:"required,min=1,max=256"`
	Description string `json:"description" validate:"max=4096"`
}

func (h *Handler) listPackages(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	items, err := h.Stage2.ListPackages(r.Context(), pc.ProjectID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"packages": items})
}

func (h *Handler) createPackage(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	var req createPackageRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.CreatePackage(r.Context(), stage2store.CreatePackageParams{
		ProjectID: pc.ProjectID, Kind: req.Kind, Name: req.Name, Description: req.Description,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) listVersions(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	packageID, err := parseUUIDParam(r, "package_id")
	if renderError(w, r, err) {
		return
	}
	items, err := h.Stage2.ListVersions(r.Context(), pc.ProjectID, packageID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"versions": items})
}

type publishVersionRequest struct {
	Version     string          `json:"version" validate:"required,min=1,max=128"`
	SizeBytes   int64           `json:"size_bytes" validate:"min=0"`
	SHA256      string          `json:"sha256" validate:"omitempty,hexadecimal,len=64"`
	ContentType string          `json:"content_type" validate:"max=256"`
	Metadata    json.RawMessage `json:"metadata"`
}

func (h *Handler) publishVersion(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	packageID, err := parseUUIDParam(r, "package_id")
	if renderError(w, r, err) {
		return
	}
	var req publishVersionRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.PublishVersion(r.Context(), stage2store.PublishVersionParams{
		ProjectID: pc.ProjectID, PackageID: packageID, Version: req.Version,
		SizeBytes: req.SizeBytes, SHA256: req.SHA256, ContentType: req.ContentType,
		Metadata: req.Metadata, PublishedBy: pc.UserID,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) listReleases(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	items, err := h.Stage2.ListReleases(r.Context(), pc.ProjectID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"releases": items})
}

type createReleaseRequest struct {
	TagName    string `json:"tag_name" validate:"required,min=1,max=128"`
	Name       string `json:"name" validate:"required,min=1,max=256"`
	Notes      string `json:"notes" validate:"max=65536"`
	Prerelease bool   `json:"prerelease"`
	Publish    bool   `json:"publish"`
}

func (h *Handler) createRelease(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireWrite(pc)) {
		return
	}
	var req createReleaseRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.CreateRelease(r.Context(), stage2store.CreateReleaseParams{
		ProjectID: pc.ProjectID, TagName: req.TagName, Name: req.Name, Notes: req.Notes,
		Prerelease: req.Prerelease, CreatedBy: pc.UserID, Publish: req.Publish,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, out)
}

// --- repository setup ----------------------------------------------------------

func (h *Handler) resolveRepoID(r *http.Request, pc *projectContext) (uuid.UUID, error) {
	repo, projectID, _, err := h.Users.ResolveRepoPath(r.Context(), chi.URLParam(r, "org_slug"),
		chi.URLParam(r, "project_slug"), chi.URLParam(r, "repo_slug"))
	if err != nil {
		return uuid.Nil, err
	}
	if projectID != pc.ProjectID {
		return uuid.Nil, apperr.NotFound("repository")
	}
	return repo.ID, nil
}

func (h *Handler) getRepoSettings(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) {
		return
	}
	repoID, err := h.resolveRepoID(r, pc)
	if renderError(w, r, err) {
		return
	}
	out, err := h.Stage2.GetRepoSettings(r.Context(), repoID)
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

type updateRepoSettingsRequest struct {
	DefaultBranch       *string   `json:"default_branch,omitempty" validate:"omitempty,min=1,max=255"`
	Topics              *[]string `json:"topics,omitempty" validate:"omitempty,max=20,dive,min=1,max=50"`
	IssuesEnabled       *bool     `json:"issues_enabled,omitempty"`
	WikiEnabled         *bool     `json:"wiki_enabled,omitempty"`
	MergeStrategies     *[]string `json:"merge_strategies,omitempty" validate:"omitempty,min=1,max=3,dive,oneof=merge squash rebase"`
	DeleteBranchOnMerge *bool     `json:"delete_branch_on_merge,omitempty"`
}

func (h *Handler) updateRepoSettings(w http.ResponseWriter, r *http.Request) {
	pc, err := h.resolveProject(r)
	if renderError(w, r, err) || renderError(w, r, requireManage(pc)) {
		return
	}
	repoID, err := h.resolveRepoID(r, pc)
	if renderError(w, r, err) {
		return
	}
	var req updateRepoSettingsRequest
	if renderError(w, r, httpapi.DecodeJSON(w, r, &req)) {
		return
	}
	out, err := h.Stage2.UpdateRepoSettings(r.Context(), repoID, stage2store.UpdateRepoSettingsParams{
		DefaultBranch: req.DefaultBranch, Topics: req.Topics, IssuesEnabled: req.IssuesEnabled,
		WikiEnabled: req.WikiEnabled, MergeStrategies: req.MergeStrategies,
		DeleteBranchOnMerge: req.DeleteBranchOnMerge,
	})
	if renderError(w, r, err) {
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}
