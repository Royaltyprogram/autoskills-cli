package controller

import (
	"embed"
	"mime"
	"net/http"
	"path"
	"path/filepath"

	"github.com/labstack/echo/v5"
)

//go:embed assets/dashboard.html assets/landing.html assets/dashboard.css assets/dashboard.js
var uiFS embed.FS

type DashboardRoute struct {
	Options
}

func NewDashboardRoute(opt Options) *DashboardRoute {
	return &DashboardRoute{Options: opt}
}

func (r *DashboardRoute) RegisterRoute(router *echo.Group) {
	router.GET("/", r.landing)
	router.GET("/dashboard", r.dashboard)
	router.GET("/assets/:name", r.asset)
}

func (r *DashboardRoute) landing(c *echo.Context) error {
	page, err := uiFS.ReadFile("assets/landing.html")
	if err != nil {
		return err
	}
	return c.HTML(http.StatusOK, string(page))
}

func (r *DashboardRoute) dashboard(c *echo.Context) error {
	page, err := uiFS.ReadFile("assets/dashboard.html")
	if err != nil {
		return err
	}
	return c.HTML(http.StatusOK, string(page))
}

func (r *DashboardRoute) asset(c *echo.Context) error {
	name := c.Param("name")
	if name == "" || path.Base(name) != name {
		return echo.ErrNotFound
	}

	body, err := uiFS.ReadFile(path.Join("assets", name))
	if err != nil {
		return echo.ErrNotFound
	}

	contentType := mime.TypeByExtension(filepath.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}

	return c.Blob(http.StatusOK, contentType, body)
}
