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

//go:embed assets/admin.html assets/admin.js assets/dashboard.html assets/docs.html assets/landing.html assets/login.html assets/dashboard.css assets/dashboard.js assets/logo.ico assets/logo.png assets/logo.svg
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
	router.GET("/docs", r.docs)
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

func (r *DashboardRoute) docs(c *echo.Context) error {
	page, err := uiFS.ReadFile("assets/docs.html")
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
	html := string(page)
	if r.AnalyticsService != nil && r.AnalyticsService.IsDevMode() {
		devButton := `<form method="POST" action="/api/v1/auth/dev/login" style="margin-top:12px">` +
			`<button type="submit" class="provider-btn" style="background:var(--support-light);border-color:var(--support);">` +
			`<svg class="provider-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M16 18l2-2-2-2"/><path d="M8 18l-2-2 2-2"/><path d="M14.5 4l-5 16"/></svg>` +
			`Dev Login (no auth)</button></form>`
		html = strings.Replace(html, `</div><!--dev-login-slot-->`, devButton+`</div>`, 1)
		// Fallback: inject after the Google button's closing </a> inside login-providers
		if !strings.Contains(html, devButton) {
			html = strings.Replace(html, `</div>

      <div class="login-divider">`, devButton+`</div>

      <div class="login-divider">`, 1)
		}
	}
	return c.HTML(http.StatusOK, html)
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
