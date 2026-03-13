package request

import "time"

type LoginReq struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

type IssueCLITokenReq struct {
	Label string `json:"label"`
}

type RevokeCLITokenReq struct {
	TokenID string `json:"token_id" validate:"required"`
}

type CLILoginReq struct {
	DeviceName    string   `json:"device_name" validate:"required"`
	Hostname      string   `json:"hostname"`
	Platform      string   `json:"platform"`
	CLIVersion    string   `json:"cli_version"`
	Tools         []string `json:"tools"`
	ConsentScopes []string `json:"consent_scopes"`
	AgentID       string   `json:"agent_id"`
	DeviceID      string   `json:"device_id"`
}

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
	ProjectID           string         `json:"project_id"`
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
	ProjectID string `query:"project_id"`
}

type SessionSummaryReq struct {
	ProjectID              string         `json:"project_id"`
	SessionID              string         `json:"session_id"`
	Tool                   string         `json:"tool" validate:"required"`
	TokenIn                int            `json:"token_in"`
	TokenOut               int            `json:"token_out"`
	CachedInputTokens      int            `json:"cached_input_tokens"`
	ReasoningOutputTokens  int            `json:"reasoning_output_tokens"`
	FunctionCallCount      int            `json:"function_call_count"`
	ToolErrorCount         int            `json:"tool_error_count"`
	SessionDurationMS      int            `json:"session_duration_ms"`
	ToolWallTimeMS         int            `json:"tool_wall_time_ms"`
	ToolCalls              map[string]int `json:"tool_calls"`
	ToolErrors             map[string]int `json:"tool_errors"`
	ToolWallTimesMS        map[string]int `json:"tool_wall_times_ms"`
	RawQueries             []string       `json:"raw_queries"`
	Models                 []string       `json:"models"`
	ModelProvider          string         `json:"model_provider"`
	FirstResponseLatencyMS int            `json:"first_response_latency_ms"`
	AssistantResponses     []string       `json:"assistant_responses"`
	ReasoningSummaries     []string       `json:"reasoning_summaries"`
	Timestamp              time.Time      `json:"timestamp"`
}

type SessionSummaryListReq struct {
	ProjectID string `query:"project_id"`
	Limit     int    `query:"limit"`
}

type ReportListReq struct {
	ProjectID string `query:"project_id"`
}

type DashboardOverviewReq struct {
	OrgID string `query:"org_id" validate:"required"`
}

type DashboardProjectInsightsReq struct {
	ProjectID string `query:"project_id" validate:"required"`
}

type ProjectListReq struct {
	OrgID string `query:"org_id" validate:"required"`
}

type AuditListReq struct {
	OrgID        string `query:"org_id" validate:"required"`
	ProjectID    string `query:"project_id"`
	Type         string `query:"type"`
	ActorUserID  string `query:"actor_user_id"`
	TargetUserID string `query:"target_user_id"`
	Limit        int    `query:"limit"`
}

type AdminUserListReq struct {
	Search         string `query:"search"`
	Role           string `query:"role"`
	Status         string `query:"status"`
	IncludeDeleted bool   `query:"include_deleted"`
}

type AdminUserCreateReq struct {
	Email    string `json:"email" validate:"required,email"`
	Name     string `json:"name" validate:"required"`
	Role     string `json:"role"`
	Password string `json:"password" validate:"required"`
}

type AdminUserPasswordResetReq struct {
	UserID   string `json:"user_id" validate:"required"`
	Password string `json:"password" validate:"required"`
}

type AdminUserDeactivateReq struct {
	UserID string `json:"user_id" validate:"required"`
}

type AdminUserDeleteReq struct {
	UserID string `json:"user_id" validate:"required"`
}
