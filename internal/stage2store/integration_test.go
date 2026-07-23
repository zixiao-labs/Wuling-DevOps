package stage2store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/testutil/dbtest"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

func TestStage2ProjectLifecycle(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.Reset(t, pool)
	ctx := context.Background()
	users := userstore.New(pool)
	store := New(pool)

	user, org, err := users.CreateUser(ctx, userstore.CreateUserParams{
		Username: "stage2-user", Email: "stage2@example.com", DisplayName: "Stage 2",
	})
	require.NoError(t, err)
	project, err := users.CreateProject(ctx, userstore.CreateProjectParams{
		OrgID: org.ID, Slug: "stage2", DisplayName: "Stage 2",
	})
	require.NoError(t, err)

	settings, err := store.GetProjectSettings(ctx, project.ID)
	require.NoError(t, err)
	require.Equal(t, "scrum", settings.ProcessTemplate)
	require.Equal(t, 14, settings.IterationLengthDays)

	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	iteration, err := store.CreateIteration(ctx, CreateIterationParams{
		ProjectID: project.ID, Name: "Sprint 1", State: "current",
		StartsAt: start, EndsAt: start.AddDate(0, 0, 13),
	})
	require.NoError(t, err)
	priority := 0
	points := 5.0
	workItem, err := store.CreateWorkItem(ctx, CreateWorkItemParams{
		ProjectID: project.ID, IterationID: &iteration.ID, AuthorID: user.ID,
		Type: "user_story", Title: "Ship Stage 2", Priority: &priority, StoryPoints: &points,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), workItem.Number)
	require.Equal(t, 0, workItem.Priority)
	active := "active"
	workItem, err = store.UpdateWorkItem(ctx, project.ID, workItem.Number, UpdateWorkItemParams{State: &active})
	require.NoError(t, err)
	require.Equal(t, "active", workItem.State)

	plan, err := store.CreateTestPlan(ctx, CreateTestPlanParams{
		ProjectID: project.ID, IterationID: &iteration.ID, Name: "Release acceptance", CreatedBy: user.ID,
	})
	require.NoError(t, err)
	suite, err := store.CreateTestSuite(ctx, CreateTestSuiteParams{
		ProjectID: project.ID, PlanID: plan.ID, Name: "Dashboard",
	})
	require.NoError(t, err)
	testCase, err := store.CreateTestCase(ctx, project.ID, CreateTestCaseParams{
		SuiteID: suite.ID, Title: "renders counters", Steps: json.RawMessage(`[]`),
		Automation: "lightning", AutomationRef: "planning.test.ts", CreatedBy: user.ID,
	})
	require.NoError(t, err)
	_, err = store.RecordTestRun(ctx, project.ID, RecordTestRunParams{
		TestCaseID: testCase.ID, Status: "passed", RunBy: user.ID,
	})
	require.NoError(t, err)

	pkg, err := store.CreatePackage(ctx, CreatePackageParams{
		ProjectID: project.ID, Kind: "npm", Name: "@wuling/stage2",
	})
	require.NoError(t, err)
	_, err = store.PublishVersion(ctx, PublishVersionParams{
		ProjectID: project.ID, PackageID: pkg.ID, Version: "2.0.0", PublishedBy: user.ID,
	})
	require.NoError(t, err)
	_, err = store.CreateRelease(ctx, CreateReleaseParams{
		ProjectID: project.ID, TagName: "v2.0.0", Name: "Stage 2", CreatedBy: user.ID, Publish: true,
	})
	require.NoError(t, err)

	dashboard, err := store.Dashboard(ctx, project.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), dashboard.OpenWorkItems)
	require.Equal(t, int64(1), dashboard.TestCases)
	require.Equal(t, int64(1), dashboard.Packages)
	require.Equal(t, int64(1), dashboard.Releases)
}
