package controller

import (
	"github.com/labstack/echo/v5"

	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/routes/common"
)

type AnalyticsRoute struct {
	Options
}

func NewAnalyticsRoute(opt Options) *AnalyticsRoute {
	return &AnalyticsRoute{Options: opt}
}

func (r *AnalyticsRoute) RegisterRoute(router *echo.Group) {
	api := router.Group("/api/v1")
	api.POST("/agents/register", r.registerAgent)
	api.POST("/projects/register", r.registerProject)
	api.POST("/config-snapshots", r.uploadConfigSnapshot)
	api.POST("/session-summaries", r.uploadSessionSummary)
	api.GET("/projects", r.listProjects)
	api.GET("/recommendations", r.listRecommendations)
	api.GET("/impact", r.impactSummary)
	api.GET("/audits", r.auditList)
	api.POST("/recommendations/apply", r.applyRecommendation)
	api.GET("/applies", r.applyHistory)
	api.POST("/applies/result", r.reportApplyResult)
	api.GET("/dashboard/overview", r.dashboardOverview)
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

func (r *AnalyticsRoute) uploadSessionSummary(c *echo.Context) error {
	var req request.SessionSummaryReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.UploadSessionSummary(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) listRecommendations(c *echo.Context) error {
	var req request.RecommendationListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ListRecommendations(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) listProjects(c *echo.Context) error {
	var req request.ProjectListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ListProjects(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) applyRecommendation(c *echo.Context) error {
	var req request.ApplyRecommendationReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.CreateApplyPlan(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) applyHistory(c *echo.Context) error {
	var req request.ApplyHistoryReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ApplyHistory(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) impactSummary(c *echo.Context) error {
	var req request.ImpactSummaryReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ImpactSummary(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) auditList(c *echo.Context) error {
	var req request.AuditListReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.AuditList(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) reportApplyResult(c *echo.Context) error {
	var req request.ApplyResultReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.ReportApplyResult(c.Request().Context(), &req))
}

func (r *AnalyticsRoute) dashboardOverview(c *echo.Context) error {
	var req request.DashboardOverviewReq
	if err := c.Bind(&req); err != nil {
		return err
	}
	return common.WrapResp(c)(r.AnalyticsService.DashboardOverview(c.Request().Context(), &req))
}
