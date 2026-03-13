package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"log/slog"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
	"github.com/Royaltyprogram/aiops/routes/controller"
	"github.com/Royaltyprogram/aiops/service"
)

type testEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

func TestRequireAPITokenProtectsAnalyticsAPI(t *testing.T) {
	conf := &configs.Config{}
	conf.App.APIToken = "secret-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := NewEcho(conf, slog.Default(), store)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
	})
	healthSvc := service.NewHealthService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})

	engine := NewHttpEngine(Options{
		Router: echo,
		Conf:   conf,
		Health: controller.NewHealthRoute(controller.Options{HealthService: healthSvc}),
		Analytics: controller.NewAnalyticsRoute(controller.Options{
			AnalyticsService: analyticsSvc,
		}),
	})
	engine.RegisterRoute()

	healthReq := httptest.NewRequest(http.MethodGet, "/health?message=ok", nil)
	healthRec := httptest.NewRecorder()
	echo.ServeHTTP(healthRec, healthReq)
	require.Equal(t, http.StatusOK, healthRec.Code)

	payload, err := json.Marshal(request.RegisterAgentReq{
		OrgID:      "org-auth",
		UserID:     "user-auth",
		DeviceName: "mbp",
	})
	require.NoError(t, err)

	apiReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(payload))
	apiReq = apiReq.WithContext(context.Background())
	apiReq.Header.Set("Content-Type", "application/json")
	apiRec := httptest.NewRecorder()
	echo.ServeHTTP(apiRec, apiReq)
	require.Equal(t, http.StatusUnauthorized, apiRec.Code)

	var env testEnvelope
	require.NoError(t, json.Unmarshal(apiRec.Body.Bytes(), &env))
	require.Equal(t, 1001, env.Code)

	apiReq = httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(payload))
	apiReq = apiReq.WithContext(context.Background())
	apiReq.Header.Set("Content-Type", "application/json")
	apiReq.Header.Set("X-Crux-Token", "secret-token")
	apiRec = httptest.NewRecorder()
	echo.ServeHTTP(apiRec, apiReq)
	require.Equal(t, http.StatusOK, apiRec.Code)

	require.NoError(t, json.Unmarshal(apiRec.Body.Bytes(), &env))
	require.Equal(t, 0, env.Code)

	var data response.AgentRegistrationResp
	require.NoError(t, json.Unmarshal(env.Data, &data))
	require.Equal(t, "registered", data.Status)

	loginPayload, err := json.Marshal(request.LoginReq{
		Email:    "demo@example.com",
		Password: "demo1234",
	})
	require.NoError(t, err)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	loginReq = loginReq.WithContext(context.Background())
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	echo.ServeHTTP(loginRec, loginReq)
	require.Equal(t, http.StatusOK, loginRec.Code)

	var sessionCookie *http.Cookie
	for _, cookie := range loginRec.Result().Cookies() {
		if cookie.Name == service.WebSessionCookieName {
			sessionCookie = cookie
			break
		}
	}
	require.NotNil(t, sessionCookie)

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq = meReq.WithContext(context.Background())
	meReq.AddCookie(sessionCookie)
	meRec := httptest.NewRecorder()
	echo.ServeHTTP(meRec, meReq)
	require.Equal(t, http.StatusOK, meRec.Code)
}

func TestRequireAPITokenDisablesStaticTokenByDefaultInProd(t *testing.T) {
	conf := &configs.Config{}
	conf.App.Mode = "prod"
	conf.App.APIToken = "secret-token"
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	conf.Auth.BootstrapUsers = []configs.BootstrapUser{{
		ID:       "beta-user",
		OrgID:    "beta-org",
		OrgName:  "Beta Org",
		Email:    "beta@example.com",
		Name:     "Beta User",
		Password: "beta-pass",
	}}

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := NewEcho(conf, slog.Default(), store)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 1,
	})

	engine := NewHttpEngine(Options{
		Router: echo,
		Conf:   conf,
		Analytics: controller.NewAnalyticsRoute(controller.Options{
			AnalyticsService: analyticsSvc,
		}),
	})
	engine.RegisterRoute()

	payload, err := json.Marshal(request.RegisterAgentReq{
		OrgID:      "beta-org",
		UserID:     "beta-user",
		DeviceName: "mbp",
	})
	require.NoError(t, err)

	apiReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(payload))
	apiReq = apiReq.WithContext(context.Background())
	apiReq.Header.Set("Content-Type", "application/json")
	apiReq.Header.Set("X-Crux-Token", "secret-token")
	apiRec := httptest.NewRecorder()
	echo.ServeHTTP(apiRec, apiReq)
	require.Equal(t, http.StatusUnauthorized, apiRec.Code)

	loginPayload, err := json.Marshal(request.LoginReq{
		Email:    "beta@example.com",
		Password: "beta-pass",
	})
	require.NoError(t, err)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	loginReq = loginReq.WithContext(context.Background())
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	echo.ServeHTTP(loginRec, loginReq)
	require.Equal(t, http.StatusOK, loginRec.Code)
}
