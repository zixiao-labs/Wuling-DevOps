package runnerhttp_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/model"
	"github.com/zixiao-labs/wuling-devops/internal/orgconfig"
	"github.com/zixiao-labs/wuling-devops/internal/runnercheck"
	"github.com/zixiao-labs/wuling-devops/internal/runnerhttp"
)

const adminSelfCheckConfig = `version: 1
tiers:
  small: { cpu: 1 }
pools:
  - name: aws-check
    provider: aws
    tier: small
    aws:
      region: us-east-1
      ami: ami-1234
      instance_type: t3.small
      subnet_id: subnet-1234
      security_group_ids: [sg-1234]
      credentials_secret: AWS_CREDENTIALS
`

type selfCheckOrgLookup struct {
	org *model.Org
}

func (l selfCheckOrgLookup) GetOrgBySlug(_ context.Context, slug string) (*model.Org, error) {
	if l.org != nil && slug == l.org.Slug {
		return l.org, nil
	}
	return nil, nil
}

type selfCheckConfigReader struct {
	file *orgconfig.File
}

func (r selfCheckConfigReader) Read(_ context.Context, _ uuid.UUID, _ string) (*orgconfig.File, error) {
	return r.file, nil
}

type selfCheckSecretLister struct{}

func (selfCheckSecretLister) ListOrg(_ context.Context, _ uuid.UUID) ([]model.Secret, error) {
	return []model.Secret{{Name: "AWS_CREDENTIALS"}}, nil
}

func TestAdminRunnerSelfCheckHandlerRefusesPreflightOnlyService(t *testing.T) {
	org := &model.Org{ID: uuid.New(), Slug: "platform"}
	service := runnercheck.NewService(
		selfCheckConfigReader{file: &orgconfig.File{BlobSHA: "configured", Content: []byte(adminSelfCheckConfig)}},
		selfCheckSecretLister{},
		runnercheck.NewMemoryHistory(5),
	)
	handler := &runnerhttp.AdminRunnerSelfCheckHandler{
		Orgs:   selfCheckOrgLookup{org: org},
		Checks: service,
	}
	router := chi.NewRouter()
	router.Use(withSelfCheckIdentity(uuid.New()))
	handler.MountInner(router)

	postBody := bytes.NewBufferString(`{"org_slug":"platform","pool_names":["aws-check"]}`)
	postReq := httptest.NewRequest(http.MethodPost, "/runner-self-checks", postBody)
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	router.ServeHTTP(postRec, postReq)

	require.Equal(t, http.StatusServiceUnavailable, postRec.Code)

	getReq := httptest.NewRequest(http.MethodGet, "/runner-self-checks?org_slug=platform", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	require.Equal(t, http.StatusServiceUnavailable, getRec.Code)
}

func TestAdminRunnerSelfCheckHandlerRequiresIdentity(t *testing.T) {
	org := &model.Org{ID: uuid.New(), Slug: "platform"}
	handler := &runnerhttp.AdminRunnerSelfCheckHandler{
		Orgs: selfCheckOrgLookup{org: org},
		Checks: runnercheck.NewService(
			selfCheckConfigReader{file: &orgconfig.File{BlobSHA: "configured", Content: []byte(adminSelfCheckConfig)}},
			selfCheckSecretLister{},
			runnercheck.NewMemoryHistory(5),
		),
	}
	router := chi.NewRouter()
	handler.MountInner(router)

	req := httptest.NewRequest(http.MethodGet, "/runner-self-checks?org_slug=platform", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func withSelfCheckIdentity(userID uuid.UUID) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := &auth.Identity{UserID: userID, Username: "admin", Source: auth.IdentitySourceJWT}
			next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), identity)))
		})
	}
}
