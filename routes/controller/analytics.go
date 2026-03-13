package controller

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/routes/common"
	"github.com/Royaltyprogram/aiops/service"
)

type AnalyticsRoute struct {
	Options
}

func NewAnalyticsRoute(opt Options) *AnalyticsRoute {
	return &AnalyticsRoute{Options: opt}
}

func (r *AnalyticsRoute) RegisterRoute(router *echo.Group) {
	api := router.Group("/api/v1")
	api.POST("/auth/login", r.login)
	api.GET("/auth/me", r.currentSession)
	api.POST("/auth/logout", r.logout)
	api.POST("/auth/cli-tokens", r.issueCLIToken)
	api.GET("/auth/cli-tokens", r.listCLITokens)
	api.POST("/auth/cli-tokens/revoke", r.revokeCLIToken)
	api.POST("/auth/cli/login", r.authenticateCLI)
	api.POST("/agents/register", r.registerAgent)
	api.POST("/projects/register", r.registerProject)
	api.POST("/config-snapshots", r.uploadConfigSnapshot)
	api.GET("/config-snapshots", r.listConfigSnapshots)
	api.POST("/session-summaries", r.uploadSessionSummary)
	api.GET("/session-summaries", r.listSessionSummaries)
	api.GET("/projects", r.listProjects)
	api.GET("/reports", r.listReports)
	api.GET("/audits", r.auditList)
	api.GET("/admin/users", r.listAdminUsers)
	api.POST("/admin/users", r.createAdminUser)
	api.POST("/admin/users/reset-password", r.resetAdminUserPassword)
	api.POST("/admin/users/deactivate", r.deactivateAdminUser)
	api.POST("/admin/users/delete", r.deleteAdminUser)
	api.GET("/dashboard/overview", r.dashboardOverview)
	api.GET("/dashboard/project-insights", r.dashboardProjectInsights)
}

func (r *AnalyticsRoute) login(c *echo.Context) error {
	var req request.LoginReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	resp, err := r.AnalyticsService.Login(c.Request().Context(), &req)
	if err != nil {
		return err
	}
	if resp.SessionToken != "" {
		c.SetCookie(buildSessionCookie(c, resp.SessionToken, resp.SessionExpiresAt))
		resp.SessionToken = ""
	}
	return common.WrapResp(c)(resp, nil)
}

func (r *AnalyticsRoute) currentSession(c *echo.Context) error {
	return common.WrapResp(c)(r.AnalyticsService.CurrentSession(c.Request().Context()))
}

func (r *AnalyticsRoute) logout(c *echo.Context) error {
	resp, err := r.AnalyticsService.Logout(c.Request().Context())
	if err != nil {
		return err
	}
	c.SetCookie(expiredSessionCookie(c))
	return common.WrapResp(c)(resp, nil)
}

func (r *AnalyticsRoute) issueCLIToken(c *echo.Context) error {
	var req request.IssueCLITokenReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.IssueCLIToken(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) listCLITokens(c *echo.Context) error {
	return common.WrapResp(c)(r.AnalyticsService.ListCLITokens(c.Request().Context()))
}

func (r *AnalyticsRoute) revokeCLIToken(c *echo.Context) error {
	var req request.RevokeCLITokenReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.RevokeCLIToken(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) authenticateCLI(c *echo.Context) error {
	var req request.CLILoginReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.AuthenticateCLI(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) registerAgent(c *echo.Context) error {
	var req request.RegisterAgentReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.RegisterAgent(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) registerProject(c *echo.Context) error {
	var req request.RegisterProjectReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.RegisterProject(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) uploadConfigSnapshot(c *echo.Context) error {
	var req request.ConfigSnapshotReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.UploadConfigSnapshot(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) listConfigSnapshots(c *echo.Context) error {
	var req request.ConfigSnapshotListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ListConfigSnapshots(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) uploadSessionSummary(c *echo.Context) error {
	var req request.SessionSummaryReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.UploadSessionSummary(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) listSessionSummaries(c *echo.Context) error {
	var req request.SessionSummaryListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ListSessionSummaries(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) listReports(c *echo.Context) error {
	var req request.ReportListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ListReports(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) listProjects(c *echo.Context) error {
	var req request.ProjectListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ListProjects(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) auditList(c *echo.Context) error {
	var req request.AuditListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.AuditList(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) listAdminUsers(c *echo.Context) error {
	var req request.AdminUserListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ListAdminUsers(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) createAdminUser(c *echo.Context) error {
	var req request.AdminUserCreateReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.CreateAdminUser(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) resetAdminUserPassword(c *echo.Context) error {
	var req request.AdminUserPasswordResetReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ResetAdminUserPassword(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) deactivateAdminUser(c *echo.Context) error {
	var req request.AdminUserDeactivateReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.DeactivateAdminUser(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) deleteAdminUser(c *echo.Context) error {
	var req request.AdminUserDeleteReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.DeleteAdminUser(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) dashboardOverview(c *echo.Context) error {
	var req request.DashboardOverviewReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.DashboardOverview(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) dashboardProjectInsights(c *echo.Context) error {
	var req request.DashboardProjectInsightsReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.DashboardProjectInsights(c.Request().Context(), &req))
}

func buildSessionCookie(c *echo.Context, token string, expiresAt *time.Time) *http.Cookie {
	cookie := &http.Cookie{
		Name:     service.WebSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(c),
	}
	if expiresAt != nil {
		cookie.Expires = expiresAt.UTC()
		cookie.MaxAge = int(time.Until(cookie.Expires).Seconds())
	}
	return cookie
}

func expiredSessionCookie(c *echo.Context) *http.Cookie {
	return &http.Cookie{
		Name:     service.WebSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(c),
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
	}
}

func requestIsHTTPS(c *echo.Context) bool {
	req := c.Request()
	if req == nil {
		return false
	}
	if req.TLS != nil {
		return true
	}
	return strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
}
