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
	Timestamp              time.Time      `json:"timestamp"`
}

type SessionSummaryListReq struct {
	ProjectID string `query:"project_id"`
	Limit     int    `query:"limit"`
}

type RecommendationListReq struct {
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

type ApplyHistoryReq struct {
	ProjectID string `query:"project_id"`
}

type PendingApplyReq struct {
	ProjectID string `query:"project_id"`
	UserID    string `query:"user_id"`
}

type ChangePlanListReq struct {
	ProjectID string `query:"project_id"`
	Status    string `query:"status"`
	UserID    string `query:"user_id"`
}

type ExperimentListReq struct {
	ProjectID string `query:"project_id"`
}

type ImpactSummaryReq struct {
	ProjectID string `query:"project_id"`
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

type HarnessCommandResultReq struct {
	Phase      string `json:"phase"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output,omitempty"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
}

type HarnessRunReq struct {
	ProjectID        string                    `json:"project_id" validate:"required"`
	RecommendationID string                    `json:"recommendation_id"`
	ApplyID          string                    `json:"apply_id"`
	SpecFile         string                    `json:"spec_file" validate:"required"`
	Name             string                    `json:"name" validate:"required"`
	Goal             string                    `json:"goal"`
	Passed           bool                      `json:"passed"`
	Reason           string                    `json:"reason"`
	RootDir          string                    `json:"root_dir"`
	DurationMS       int64                     `json:"duration_ms"`
	TriggeredBy      string                    `json:"triggered_by"`
	Commands         []HarnessCommandResultReq `json:"commands"`
	StartedAt        time.Time                 `json:"started_at"`
	CompletedAt      time.Time                 `json:"completed_at"`
}
