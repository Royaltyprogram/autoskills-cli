package response

import "time"

type AgentRegistrationResp struct {
	AgentID      string    `json:"agent_id"`
	OrgID        string    `json:"org_id"`
	UserID       string    `json:"user_id"`
	Status       string    `json:"status"`
	RegisteredAt time.Time `json:"registered_at"`
}

type ProjectRegistrationResp struct {
	ProjectID   string    `json:"project_id"`
	Status      string    `json:"status"`
	ConnectedAt time.Time `json:"connected_at"`
}

type ConfigSnapshotResp struct {
	SnapshotID string    `json:"snapshot_id"`
	ProjectID  string    `json:"project_id"`
	ProfileID  string    `json:"profile_id"`
	CapturedAt time.Time `json:"captured_at"`
}

type SessionIngestResp struct {
	SessionID               string    `json:"session_id"`
	ProjectID               string    `json:"project_id"`
	RecommendationCount     int       `json:"recommendation_count"`
	LatestRecommendationIDs []string  `json:"latest_recommendation_ids"`
	RecordedAt              time.Time `json:"recorded_at"`
}

type RecommendationResp struct {
	ID              string         `json:"id"`
	ProjectID       string         `json:"project_id"`
	Kind            string         `json:"kind"`
	Title           string         `json:"title"`
	Summary         string         `json:"summary"`
	Reason          string         `json:"reason"`
	ExpectedImpact  string         `json:"expected_impact"`
	Status          string         `json:"status"`
	Score           float64        `json:"score"`
	TargetTool      string         `json:"target_tool"`
	TargetFileHint  string         `json:"target_file_hint"`
	SettingsUpdates map[string]any `json:"settings_updates"`
	CreatedAt       time.Time      `json:"created_at"`
}

type RecommendationListResp struct {
	Items []RecommendationResp `json:"items"`
}

type ProjectResp struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	RepoHash       string             `json:"repo_hash"`
	RepoPath       string             `json:"repo_path"`
	DefaultTool    string             `json:"default_tool"`
	LastProfileID  string             `json:"last_profile_id"`
	LastIngestedAt *time.Time         `json:"last_ingested_at"`
	LanguageMix    map[string]float64 `json:"language_mix"`
}

type ProjectListResp struct {
	Items []ProjectResp `json:"items"`
}

type PatchPreviewItem struct {
	FilePath        string         `json:"file_path"`
	Summary         string         `json:"summary"`
	SettingsUpdates map[string]any `json:"settings_updates"`
}

type ApplyPlanResp struct {
	ApplyID        string             `json:"apply_id"`
	Recommendation RecommendationResp `json:"recommendation"`
	Status         string             `json:"status"`
	PatchPreview   []PatchPreviewItem `json:"patch_preview"`
	RequestedAt    time.Time          `json:"requested_at"`
}

type ApplyResultResp struct {
	ApplyID    string    `json:"apply_id"`
	Status     string    `json:"status"`
	AppliedAt  time.Time `json:"applied_at"`
	RolledBack bool      `json:"rolled_back"`
}

type ApplyHistoryItem struct {
	ApplyID          string             `json:"apply_id"`
	RecommendationID string             `json:"recommendation_id"`
	Status           string             `json:"status"`
	Scope            string             `json:"scope"`
	RequestedBy      string             `json:"requested_by"`
	RequestedAt      time.Time          `json:"requested_at"`
	AppliedAt        *time.Time         `json:"applied_at"`
	AppliedFile      string             `json:"applied_file"`
	AppliedSettings  map[string]any     `json:"applied_settings"`
	PatchPreview     []PatchPreviewItem `json:"patch_preview"`
	RolledBack       bool               `json:"rolled_back"`
}

type ApplyHistoryResp struct {
	Items []ApplyHistoryItem `json:"items"`
}

type ImpactSummaryItem struct {
	ApplyID             string     `json:"apply_id"`
	RecommendationID    string     `json:"recommendation_id"`
	Status              string     `json:"status"`
	AppliedAt           *time.Time `json:"applied_at"`
	SessionsBefore      int        `json:"sessions_before"`
	SessionsAfter       int        `json:"sessions_after"`
	AvgCostBefore       float64    `json:"avg_cost_before"`
	AvgCostAfter        float64    `json:"avg_cost_after"`
	AvgRetryRateBefore  float64    `json:"avg_retry_rate_before"`
	AvgRetryRateAfter   float64    `json:"avg_retry_rate_after"`
	AvgRejectRateBefore float64    `json:"avg_reject_rate_before"`
	AvgRejectRateAfter  float64    `json:"avg_reject_rate_after"`
	CostDelta           float64    `json:"cost_delta"`
	RetryDelta          float64    `json:"retry_delta"`
	RejectDelta         float64    `json:"reject_delta"`
	Interpretation      string     `json:"interpretation"`
}

type ImpactSummaryResp struct {
	Items []ImpactSummaryItem `json:"items"`
}

type AuditEventResp struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditListResp struct {
	Items []AuditEventResp `json:"items"`
}

type TaskBreakdown struct {
	TaskType string `json:"task_type"`
	Sessions int    `json:"sessions"`
}

type DashboardOverviewResp struct {
	OrgID                   string          `json:"org_id"`
	TotalProjects           int             `json:"total_projects"`
	TotalSessions           int             `json:"total_sessions"`
	ActiveRecommendations   int             `json:"active_recommendations"`
	TotalEstimatedCost      float64         `json:"total_estimated_cost"`
	AvgTokensPerQuery       float64         `json:"avg_tokens_per_query"`
	AvgToolCallsPerQuery    float64         `json:"avg_tool_calls_per_query"`
	PermissionRejectRate    float64         `json:"permission_reject_rate"`
	RetryRate               float64         `json:"retry_rate"`
	RecommendationApplyRate float64         `json:"recommendation_apply_rate"`
	RollbackRate            float64         `json:"rollback_rate"`
	TopTaskTypes            []TaskBreakdown `json:"top_task_types"`
	LastIngestedAt          *time.Time      `json:"last_ingested_at"`
}
