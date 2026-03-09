package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"log/slog"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/configs"
	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/routes/controller"
	"github.com/liushuangls/go-server-template/service"
)

func TestHealthzAndReadyzEndpoints(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := NewEcho(conf, slog.Default(), store)
	require.NoError(t, err)

	healthSvc := service.NewHealthService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})
	engine := NewHttpEngine(Options{
		Router: echo,
		Conf:   conf,
		Health: controller.NewHealthRoute(controller.Options{HealthService: healthSvc}),
	})
	engine.RegisterRoute()

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		echo.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, path)
	}
}

func TestNewEchoAppliesConfiguredCORSHeaders(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")
	conf.HTTP.AllowedOrigins = []string{"https://beta.example.com"}

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := NewEcho(conf, slog.Default(), store)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})
	engine := NewHttpEngine(Options{
		Router: echo,
		Conf:   conf,
		Analytics: controller.NewAnalyticsRoute(controller.Options{
			AnalyticsService: analyticsSvc,
		}),
	})
	engine.RegisterRoute()

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "https://beta.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "https://beta.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestNewEchoRateLimitsAPIWhenConfigured(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")
	conf.HTTP.RateLimitPerMinute = 1

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := NewEcho(conf, slog.Default(), store)
	require.NoError(t, err)

	analyticsSvc := service.NewAnalyticsService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})
	engine := NewHttpEngine(Options{
		Router: echo,
		Conf:   conf,
		Analytics: controller.NewAnalyticsRoute(controller.Options{
			AnalyticsService: analyticsSvc,
		}),
	})
	engine.RegisterRoute()

	payload, err := json.Marshal(request.LoginReq{
		Email:    "demo@example.com",
		Password: "demo1234",
	})
	require.NoError(t, err)

	first := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(payload))
	first.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	echo.ServeHTTP(firstRec, first)
	require.Equal(t, http.StatusOK, firstRec.Code)

	second := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(payload))
	second.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	echo.ServeHTTP(secondRec, second)
	require.Equal(t, http.StatusTooManyRequests, secondRec.Code)
}

func TestNewEchoRejectsRequestsOutsideAllowedCIDRs(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")
	conf.HTTP.AllowedCIDRs = []string{"10.0.0.0/8"}

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := NewEcho(conf, slog.Default(), store)
	require.NoError(t, err)

	healthSvc := service.NewHealthService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})
	engine := NewHttpEngine(Options{
		Router: echo,
		Conf:   conf,
		Health: controller.NewHealthRoute(controller.Options{HealthService: healthSvc}),
	})
	engine.RegisterRoute()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestNewEchoAllowsRequestsWithinAllowedCIDRs(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")
	conf.HTTP.AllowedCIDRs = []string{"127.0.0.1/32"}

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := NewEcho(conf, slog.Default(), store)
	require.NoError(t, err)

	healthSvc := service.NewHealthService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})
	engine := NewHttpEngine(Options{
		Router: echo,
		Conf:   conf,
		Health: controller.NewHealthRoute(controller.Options{HealthService: healthSvc}),
	})
	engine.RegisterRoute()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestNewEchoUsesTrustedProxyCIDRsForAllowlist(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")
	conf.HTTP.AllowedCIDRs = []string{"203.0.113.0/24"}
	conf.HTTP.TrustedProxyCIDRs = []string{"127.0.0.1/32"}

	store, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)

	echo, err := NewEcho(conf, slog.Default(), store)
	require.NoError(t, err)

	healthSvc := service.NewHealthService(service.Options{
		Config:         conf,
		AnalyticsStore: store,
	})
	engine := NewHttpEngine(Options{
		Router: echo,
		Conf:   conf,
		Health: controller.NewHealthRoute(controller.Options{HealthService: healthSvc}),
	})
	engine.RegisterRoute()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	rec := httptest.NewRecorder()
	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
