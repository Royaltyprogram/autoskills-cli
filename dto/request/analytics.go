package request

import "time"

type RegisterAgentReq struct {
	OrgID         string   `json:"org_id" validate:"required"`
	OrgName       string   `json:"org_name"`
	UserID        string   `json:"user_id" validate:"required"`
	UserEmail     string   `json:"user_email"`
	DeviceName    string   `json:"device_name" validate:"required"`
	Hostname      string   `json:"hostname"`
	Platform      string   `json:"platform"`
	CLIVersion    string   `json:"cli_version"`
	Tools         []string `json:"tools"`
	ConsentScopes []string `json:"consent_scopes"`
	AgentID       string   `json:"agent_id"`
	DeviceID      string   `json:"device_id"`
}

type RegisterProjectReq struct {
	OrgID       string             `json:"org_id" validate:"required"`
	AgentID     string             `json:"agent_id" validate:"required"`
	ProjectID   string             `json:"project_id"`
	Name        string             `json:"name" validate:"required"`
	RepoHash    string             `json:"repo_hash" validate:"required"`
	RepoPath    string             `json:"repo_path"`
	LanguageMix map[string]float64 `json:"language_mix"`
	DefaultTool string             `json:"default_tool"`
}

type ConfigSnapshotReq struct {
	ProjectID           string         `json:"project_id" validate:"required"`
	Tool                string         `json:"tool" validate:"required"`
	ProfileID           string         `json:"profile_id"`
	Settings            map[string]any `json:"settings"`
	EnabledMCPCount     int            `json:"enabled_mcp_count"`
	HooksEnabled        bool           `json:"hooks_enabled"`
	InstructionFiles    []string       `json:"instruction_files"`
	ConfigFingerprint   string         `json:"config_fingerprint"`
	RecentConfigChanges []string       `json:"recent_config_changes"`
	CapturedAt          time.Time      `json:"captured_at"`
}

type ConfigSnapshotListReq struct {
	ProjectID string `query:"project_id" validate:"required"`
}

type SessionSummaryReq struct {
	ProjectID                string             `json:"project_id" validate:"required"`
	SessionID                string             `json:"session_id"`
	Tool                     string             `json:"tool" validate:"required"`
	ProjectHash              string             `json:"project_hash"`
	LanguageMix              map[string]float64 `json:"language_mix"`
	TotalPromptsCount        int                `json:"total_prompts_count"`
	TotalToolCalls           int                `json:"total_tool_calls"`
	BashCallsCount           int                `json:"bash_calls_count"`
	ReadOps                  int                `json:"read_ops"`
	EditOps                  int                `json:"edit_ops"`
	WriteOps                 int                `json:"write_ops"`
	MCPUsageCount            int                `json:"mcp_usage_count"`
	PermissionRejectCount    int                `json:"permission_reject_count"`
	RetryCount               int                `json:"retry_count"`
	TokenIn                  int                `json:"token_in"`
	TokenOut                 int                `json:"token_out"`
	EstimatedCost            float64            `json:"estimated_cost"`
	TaskType                 string             `json:"task_type" validate:"required"`
	RepoSizeBucket           string             `json:"repo_size_bucket"`
	ConfigProfileID          string             `json:"config_profile_id"`
	TaskTypeDistribution     map[string]float64 `json:"task_type_distribution"`
	RepoExplorationIntensity float64            `json:"repo_exploration_intensity"`
	ShellHeavy               bool               `json:"shell_heavy"`
	WorkloadTags             []string           `json:"workload_tags"`
	AcceptanceProxy          float64            `json:"acceptance_proxy"`
	EventSummaries           []string           `json:"event_summaries"`
	Timestamp                time.Time          `json:"timestamp"`
}

type SessionSummaryListReq struct {
	ProjectID string `query:"project_id" validate:"required"`
	Limit     int    `query:"limit"`
}

type RecommendationListReq struct {
	ProjectID string `query:"project_id" validate:"required"`
}

type DashboardOverviewReq struct {
	OrgID string `query:"org_id" validate:"required"`
}

type ProjectListReq struct {
	OrgID string `query:"org_id" validate:"required"`
}

type ApplyHistoryReq struct {
	ProjectID string `query:"project_id" validate:"required"`
}

type PendingApplyReq struct {
	ProjectID string `query:"project_id" validate:"required"`
	UserID    string `query:"user_id"`
}

type ChangePlanListReq struct {
	ProjectID string `query:"project_id" validate:"required"`
	Status    string `query:"status"`
	UserID    string `query:"user_id"`
}

type ImpactSummaryReq struct {
	ProjectID string `query:"project_id" validate:"required"`
}

type AuditListReq struct {
	OrgID     string `query:"org_id" validate:"required"`
	ProjectID string `query:"project_id"`
}

type ApplyRecommendationReq struct {
	RecommendationID string `json:"recommendation_id" validate:"required"`
	RequestedBy      string `json:"requested_by" validate:"required"`
	Scope            string `json:"scope"`
}

type ReviewChangePlanReq struct {
	ApplyID    string `json:"apply_id" validate:"required"`
	Decision   string `json:"decision" validate:"required"`
	ReviewedBy string `json:"reviewed_by" validate:"required"`
	ReviewNote string `json:"review_note"`
}

type ApplyResultReq struct {
	ApplyID         string         `json:"apply_id" validate:"required"`
	Success         bool           `json:"success"`
	Note            string         `json:"note"`
	AppliedFile     string         `json:"applied_file"`
	AppliedSettings map[string]any `json:"applied_settings"`
	AppliedText     string         `json:"applied_text"`
	RolledBack      bool           `json:"rolled_back"`
}
