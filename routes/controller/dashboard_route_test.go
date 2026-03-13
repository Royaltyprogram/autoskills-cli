package controller_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/routes"
	"github.com/Royaltyprogram/aiops/routes/controller"
	"github.com/Royaltyprogram/aiops/service"
)

func TestLandingRouteServesLandingPage(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	route := controller.NewDashboardRoute(controller.Options{})
	route.RegisterRoute(echo.Group(""))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "actually understood")
	require.Contains(t, rec.Body.String(), "Sign in to dashboard")
	require.Contains(t, rec.Body.String(), `id="loginForm"`)
	require.Contains(t, rec.Body.String(), "Stop guessing what your Codex agent did wrong")
	require.Contains(t, rec.Body.String(), "Closed Beta")
	require.NotContains(t, rec.Body.String(), "demo@example.com")
}

func TestDashboardRouteServesWorkspaceDashboard(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	route := controller.NewDashboardRoute(controller.Options{})
	route.RegisterRoute(echo.Group(""))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()

	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Trace analysis reports")
	require.Contains(t, rec.Body.String(), `<link rel="stylesheet" href="/assets/dashboard.css">`)
	require.Contains(t, rec.Body.String(), `<script src="/assets/dashboard.js"></script>`)
	require.Contains(t, rec.Body.String(), "Analysis reports")
	require.Contains(t, rec.Body.String(), "Collected session traces")
	require.Contains(t, rec.Body.String(), "Usage Analytics")
	require.Contains(t, rec.Body.String(), `data-action="copy-command"`)
	require.Contains(t, rec.Body.String(), "Issued CLI tokens")
	require.Contains(t, rec.Body.String(), "Create CLI token")
	require.Contains(t, rec.Body.String(), "curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/aiops/main/scripts/install.sh | sh")
	require.Contains(t, rec.Body.String(), "crux login --server")
	require.Contains(t, rec.Body.String(), `data-action="issue-cli-token"`)
	require.Contains(t, rec.Body.String(), "Latest trace analysis")
	require.Contains(t, rec.Body.String(), "Usage Analytics")
	require.Contains(t, rec.Body.String(), "Config snapshots & recent events")
	require.Contains(t, rec.Body.String(), "Settings")
	require.Contains(t, rec.Body.String(), `data-action="refresh-dashboard"`)
	require.NotContains(t, rec.Body.String(), "Approve with confidence. Measure what changed.")
	require.NotContains(t, rec.Body.String(), `id="loginForm"`)
	require.Contains(t, rec.Body.String(), `id="adminLink"`)
}

func TestDashboardAssetRoutesServeSplitAssets(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	route := controller.NewDashboardRoute(controller.Options{})
	route.RegisterRoute(echo.Group(""))

	cases := []struct {
		path        string
		contentType string
		bodySnippet string
	}{
		{
			path:        "/assets/dashboard.css",
			contentType: "text/css",
			bodySnippet: ".wizard {",
		},
		{
			path:        "/assets/dashboard.js",
			contentType: "javascript",
			bodySnippet: `window.location.replace("/")`,
		},
		{
			path:        "/assets/admin.js",
			contentType: "javascript",
			bodySnippet: `/api/v1/admin/users`,
		},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()

		echo.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, tc.path)
		require.Contains(t, rec.Header().Get("Content-Type"), tc.contentType, tc.path)
		require.Contains(t, rec.Body.String(), tc.bodySnippet, tc.path)
	}
}

func TestAdminRouteRedirectsWithoutWebSession(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
	})

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	route := controller.NewDashboardRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	})
	route.RegisterRoute(echo.Group(""))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()

	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/", rec.Header().Get("Location"))
}

func TestAdminRouteServesPageForAdminSession(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
	})

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	controller.NewAnalyticsRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	}).RegisterRoute(echo.Group(""))
	controller.NewDashboardRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	}).RegisterRoute(echo.Group(""))

	loginRec := postJSONRecorder(t, echo, "", http.MethodPost, "/api/v1/auth/login", request.LoginReq{
		Email:    "demo@example.com",
		Password: "demo1234",
	})
	sessionCookie := requireCookie(t, loginRec, service.WebSessionCookieName)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(sessionCookie)
	rec := httptest.NewRecorder()

	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "User Management")
	require.Contains(t, rec.Body.String(), `<script src="/assets/admin.js"></script>`)
}

func TestAdminRouteRedirectsNonAdminToDashboard(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	conf.Auth.BootstrapUsers = []configs.BootstrapUser{{
		ID:       "member-1",
		OrgID:    "member-org",
		OrgName:  "Member Org",
		Email:    "member@example.com",
		Name:     "Member User",
		Role:     "member",
		Password: "member-pass",
	}}

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
	})

	echo, err := routes.NewEcho(conf, nil, store)
	require.NoError(t, err)

	controller.NewAnalyticsRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	}).RegisterRoute(echo.Group(""))
	controller.NewDashboardRoute(controller.Options{
		AnalyticsService: analyticsSvc,
	}).RegisterRoute(echo.Group(""))

	loginRec := postJSONRecorder(t, echo, "", http.MethodPost, "/api/v1/auth/login", request.LoginReq{
		Email:    "member@example.com",
		Password: "member-pass",
	})
	sessionCookie := requireCookie(t, loginRec, service.WebSessionCookieName)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(sessionCookie)
	rec := httptest.NewRecorder()

	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/dashboard", rec.Header().Get("Location"))
}
