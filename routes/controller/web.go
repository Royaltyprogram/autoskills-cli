package controller

import (
	"embed"
	"net/http"

	"github.com/labstack/echo/v5"
)

//go:embed assets/dashboard.html
var dashboardFS embed.FS

type DashboardRoute struct {
	Options
}

func NewDashboardRoute(opt Options) *DashboardRoute {
	return &DashboardRoute{Options: opt}
}

func (r *DashboardRoute) RegisterRoute(router *echo.Group) {
	router.GET("/", r.redirectDashboard)
	router.GET("/dashboard", r.dashboard)
}

func (r *DashboardRoute) redirectDashboard(c *echo.Context) error {
	return c.Redirect(http.StatusTemporaryRedirect, "/dashboard")
}

func (r *DashboardRoute) dashboard(c *echo.Context) error {
	page, err := dashboardFS.ReadFile("assets/dashboard.html")
	if err != nil {
		return err
	}
	return c.HTML(http.StatusOK, string(page))
}
