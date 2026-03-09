package controller_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/configs"
	"github.com/liushuangls/go-server-template/routes"
	"github.com/liushuangls/go-server-template/routes/controller"
)

func TestDashboardRouteServesUserFacingDashboard(t *testing.T) {
	conf := &configs.Config{}

	echo, err := routes.NewEcho(conf, nil)
	require.NoError(t, err)

	route := controller.NewDashboardRoute(controller.Options{})
	route.RegisterRoute(echo.Group(""))

	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()

	echo.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Approve with confidence. Measure what changed.")
	require.Contains(t, rec.Body.String(), "Recommended changes for this workspace")
	require.Contains(t, rec.Body.String(), "Avg Tokens / Query")
	require.Contains(t, rec.Body.String(), "What improved after rollout")
}
