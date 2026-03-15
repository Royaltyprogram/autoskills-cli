package controller

import (
	"embed"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/Royaltyprogram/aiops/service"
)

//go:embed assets/admin.html assets/admin.js assets/dashboard.html assets/landing.html assets/login.html assets/dashboard.css assets/dashboard.js assets/logo.ico assets/logo.png assets/logo.svg
var uiFS embed.FS

type DashboardRoute struct {
	Options
}

func NewDashboardRoute(opt Options) *DashboardRoute {
	return &DashboardRoute{Options: opt}
}

func (r *DashboardRoute) RegisterRoute(router *echo.Group) {
	router.GET("/", r.landing)
	router.GET("/login", r.login)
	router.GET("/dashboard", r.dashboard)
	router.GET("/admin", r.admin)
	router.GET("/favicon.ico", r.favicon)
	router.GET("/assets/:name", r.asset)
}

func (r *DashboardRoute) landing(c *echo.Context) error {
	page, err := uiFS.ReadFile("assets/landing.html")
	if err != nil {
		return err
	}
	return c.HTML(http.StatusOK, string(page))
}

func (r *DashboardRoute) login(c *echo.Context) error {
	page, err := uiFS.ReadFile("assets/login.html")
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

func (r *DashboardRoute) admin(c *echo.Context) error {
	identity, ok := r.webSessionIdentity(c)
	if !ok {
		return c.Redirect(http.StatusSeeOther, "/")
	}
	if strings.ToLower(strings.TrimSpace(identity.UserRole)) != "admin" {
		return c.Redirect(http.StatusSeeOther, "/dashboard")
	}

	page, err := uiFS.ReadFile("assets/admin.html")
	if err != nil {
		return err
	}
	return c.HTML(http.StatusOK, string(page))
}

func (r *DashboardRoute) favicon(c *echo.Context) error {
	return r.serveAsset(c, "logo.ico")
}

func (r *DashboardRoute) asset(c *echo.Context) error {
	name := c.Param("name")
	if name == "" || path.Base(name) != name {
		return echo.ErrNotFound
	}

	return r.serveAsset(c, name)
}

func (r *DashboardRoute) serveAsset(c *echo.Context, name string) error {
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

func (r *DashboardRoute) webSessionIdentity(c *echo.Context) (service.AuthIdentity, bool) {
	if r.AnalyticsService == nil || r.AnalyticsService.AnalyticsStore == nil {
		return service.AuthIdentity{}, false
	}
	cookie, err := c.Cookie(service.WebSessionCookieName)
	if err != nil || cookie == nil {
		return service.AuthIdentity{}, false
	}
	identity, ok := r.AnalyticsService.AnalyticsStore.ValidateAccessToken(strings.TrimSpace(cookie.Value))
	if !ok || identity == nil {
		return service.AuthIdentity{}, false
	}
	if identity.TokenKind != service.TokenKindWebSession {
		return service.AuthIdentity{}, false
	}
	r.AnalyticsService.AnalyticsStore.MarkAccessTokenSeenAsync(identity.TokenID, time.Now().UTC())
	return *identity, true
}
