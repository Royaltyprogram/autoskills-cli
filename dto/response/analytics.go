package response

import "time"

type AuthUserResp struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Status string `json:"status"`
}

type AuthOrganizationResp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type LoginResp struct {
	SessionToken     string               `json:"session_token"`
	SessionExpiresAt *time.Time           `json:"session_expires_at"`
	User             AuthUserResp         `json:"user"`
	Organization     AuthOrganizationResp `json:"organization"`
}

type AuthSessionResp struct {
	User         AuthUserResp         `json:"user"`
	Organization AuthOrganizationResp `json:"organization"`
}

type LogoutResp struct {
	Status    string    `json:"status"`
	RevokedAt time.Time `json:"revoked_at"`
}

type CLITokenIssueResp struct {
	TokenID     string     `json:"token_id"`
	Token       string     `json:"token"`
	TokenPrefix string     `json:"token_prefix"`
	Label       string     `json:"label"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

type CLITokenItemResp struct {
	TokenID     string     `json:"token_id"`
	Kind        string     `json:"kind"`
	TokenPrefix string     `json:"token_prefix"`
	Label       string     `json:"label"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	LastSeenAt  *time.Time `json:"last_seen_at"`
	ConsumedAt  *time.Time `json:"consumed_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

type CLITokenListResp struct {
	Items []CLITokenItemResp `json:"items"`
}

type CLITokenRevokeResp struct {
	TokenID   string    `json:"token_id"`
	Status    string    `json:"status"`
	RevokedAt time.Time `json:"revoked_at"`
}

type CLILoginResp struct {
	AccessToken      string     `json:"access_token"`
	AccessExpiresAt  *time.Time `json:"access_expires_at"`
	RefreshToken     string     `json:"refresh_token"`
	RefreshExpiresAt *time.Time `json:"refresh_expires_at"`
	TokenType        string     `json:"token_type"`
	AgentID          string     `json:"agent_id"`
	DeviceID         string     `json:"device_id"`
	OrgID            string     `json:"org_id"`
	OrgName          string     `json:"org_name"`
	UserID           string     `json:"user_id"`
	UserName         string     `json:"user_name"`
	UserEmail        string     `json:"user_email"`
	UserRole         string     `json:"user_role"`
	UserStatus       string     `json:"user_status"`
	Status           string     `json:"status"`
	ConsentScopes    []string   `json:"consent_scopes"`
	RegisteredAt     time.Time  `json:"registered_at"`
}

type CLIRefreshResp struct {
	AccessToken      string     `json:"access_token"`
	AccessExpiresAt  *time.Time `json:"access_expires_at"`
	RefreshToken     string     `json:"refresh_token"`
	RefreshExpiresAt *time.Time `json:"refresh_expires_at"`
	TokenType        string     `json:"token_type"`
	AgentID          string     `json:"agent_id"`
}

type AgentRegistrationResp struct {
	AgentID       string    `json:"agent_id"`
	DeviceID      string    `json:"device_id"`
	OrgID         string    `json:"org_id"`
	UserID        string    `json:"user_id"`
	Status        string    `json:"status"`
	ConsentScopes []string  `json:"consent_scopes"`
	RegisteredAt  time.Time `json:"registered_at"`
}

type ProjectRegistrationResp struct {
	ProjectID   string    `json:"project_id"`
	Status      string    `json:"status"`
	ConnectedAt time.Time `json:"connected_at"`
}

type ConfigSnapshotResp struct {
	SnapshotID        string    `json:"snapshot_id"`
	ProjectID         string    `json:"project_id"`
	ProfileID         string    `json:"profile_id"`
	ConfigFingerprint string    `json:"config_fingerprint"`
	CapturedAt        time.Time `json:"captured_at"`
}

type ConfigSnapshotItem struct {
	ID                  string         `json:"id"`
	ProjectID           string         `json:"project_id"`
	Tool                string         `json:"tool"`
	ProfileID           string         `json:"profile_id"`
	Settings            map[string]any `json:"settings"`
	EnabledMCPCount     int            `json:"enabled_mcp_count"`
	HooksEnabled        bool           `json:"hooks_enabled"`
	InstructionFiles    []string       `json:"instruction_files"`
	ConfigFingerprint   string         `json:"config_fingerprint"`
	RecentConfigChanges []string       `json:"recent_config_changes"`
	CapturedAt          time.Time      `json:"captured_at"`
}

type ConfigSnapshotListResp struct {
	Items []ConfigSnapshotItem `json:"items"`
}

type SessionIngestResp struct {
	SchemaVersion   string                    `json:"schema_version"`
	SessionID       string                    `json:"session_id"`
	ProjectID       string                    `json:"project_id"`
	ReportCount     int                       `json:"report_count"`
	LatestReportIDs []string                  `json:"latest_report_ids"`
	RecordedAt      time.Time                 `json:"recorded_at"`
	ResearchStatus  *ReportResearchStatusResp `json:"research_status,omitempty"`
}

type SessionSummaryItem struct {
	ID                     string         `json:"id"`
	ProjectID              string         `json:"project_id"`
	Tool                   string         `json:"tool"`
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

type SessionSummaryListResp struct {
	Items []SessionSummaryItem `json:"items"`
}

type ReportResp struct {
	ID                  string    `json:"id"`
	ProjectID           string    `json:"project_id"`
	Kind                string    `json:"kind"`
	Title               string    `json:"title"`
	Summary             string    `json:"summary"`
	UserIntent          string    `json:"user_intent"`
	ModelInterpretation string    `json:"model_interpretation"`
	Reason              string    `json:"reason"`
	Explanation         string    `json:"explanation"`
	ExpectedBenefit     string    `json:"expected_benefit"`
	Risk                string    `json:"risk"`
	ExpectedImpact      string    `json:"expected_impact"`
	Confidence          string    `json:"confidence"`
	Strengths           []string  `json:"strengths"`
	Frictions           []string  `json:"frictions"`
	NextSteps           []string  `json:"next_steps"`
	Status              string    `json:"status"`
	Score               float64   `json:"score"`
	TargetTool          string    `json:"target_tool"`
	ResearchProvider    string    `json:"research_provider"`
	ResearchModel       string    `json:"research_model"`
	Evidence            []string  `json:"evidence"`
	RawSuggestion       string    `json:"raw_suggestion"`
	CreatedAt           time.Time `json:"created_at"`
}

type ReportListResp struct {
	SchemaVersion string       `json:"schema_version"`
	Items         []ReportResp `json:"items"`
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

type AuditEventResp struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	ProjectID    string    `json:"project_id"`
	Type         string    `json:"type"`
	Message      string    `json:"message"`
	ActorUserID  string    `json:"actor_user_id"`
	ActorRole    string    `json:"actor_role"`
	TargetUserID string    `json:"target_user_id"`
	SourceIP     string    `json:"source_ip"`
	UserAgent    string    `json:"user_agent"`
	Result       string    `json:"result"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"created_at"`
}

type AuditListResp struct {
	Items []AuditEventResp `json:"items"`
}

type AdminUserResp struct {
	ID          string     `json:"id"`
	OrgID       string     `json:"org_id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Source      string     `json:"source"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
	DisabledAt  *time.Time `json:"disabled_at"`
	DeletedAt   *time.Time `json:"deleted_at"`
}

type AdminUserListResp struct {
	Items []AdminUserResp `json:"items"`
}

type AdminUserDeactivateResp struct {
	Status     string    `json:"status"`
	UserID     string    `json:"user_id"`
	DisabledAt time.Time `json:"disabled_at"`
}

type AdminUserDeleteResp struct {
	Status    string    `json:"status"`
	UserID    string    `json:"user_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type ReportResearchStatusResp struct {
	SchemaVersion    string     `json:"schema_version"`
	State            string     `json:"state"`
	Summary          string     `json:"summary"`
	Provider         string     `json:"provider"`
	Model            string     `json:"model"`
	MinimumSessions  int        `json:"minimum_sessions"`
	SessionCount     int        `json:"session_count"`
	RawQueryCount    int        `json:"raw_query_count"`
	ReportCount      int        `json:"report_count"`
	TriggerSessionID string     `json:"trigger_session_id"`
	LastError        string     `json:"last_error"`
	TriggeredAt      *time.Time `json:"triggered_at"`
	StartedAt        *time.Time `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at"`
	LastSuccessfulAt *time.Time `json:"last_successful_at"`
	LastDurationMS   int        `json:"last_duration_ms"`
}

type DashboardOverviewResp struct {
	SchemaVersion             string                    `json:"schema_version"`
	OrgID                     string                    `json:"org_id"`
	TotalDevices              int                       `json:"total_devices"`
	TotalProjects             int                       `json:"total_projects"`
	TotalSessions             int                       `json:"total_sessions"`
	ActiveReports             int                       `json:"active_reports"`
	TotalInputTokens          int                       `json:"total_input_tokens"`
	TotalOutputTokens         int                       `json:"total_output_tokens"`
	TotalTokens               int                       `json:"total_tokens"`
	AvgInputTokensPerQuery    float64                   `json:"avg_input_tokens_per_query"`
	AvgOutputTokensPerQuery   float64                   `json:"avg_output_tokens_per_query"`
	AvgTokensPerQuery         float64                   `json:"avg_tokens_per_query"`
	AvgInputTokensPerSession  float64                   `json:"avg_input_tokens_per_session"`
	AvgOutputTokensPerSession float64                   `json:"avg_output_tokens_per_session"`
	AvgTokensPerSession       float64                   `json:"avg_tokens_per_session"`
	AvgQueriesPerSession      float64                   `json:"avg_queries_per_session"`
	ActionSummary             string                    `json:"action_summary"`
	OutcomeSummary            string                    `json:"outcome_summary"`
	ResearchProvider          string                    `json:"research_provider"`
	ResearchMode              string                    `json:"research_mode"`
	LastIngestedAt            *time.Time                `json:"last_ingested_at"`
	ResearchStatus            *ReportResearchStatusResp `json:"research_status,omitempty"`
}

type DashboardProjectInsightDayResp struct {
	Day                       string `json:"day"`
	SessionCount              int    `json:"session_count"`
	QueryCount                int    `json:"query_count"`
	InputTokens               int    `json:"input_tokens"`
	OutputTokens              int    `json:"output_tokens"`
	TotalTokens               int    `json:"total_tokens"`
	CachedInputTokens         int    `json:"cached_input_tokens"`
	ReasoningOutputTokens     int    `json:"reasoning_output_tokens"`
	FunctionCallCount         int    `json:"function_call_count"`
	ToolErrorCount            int    `json:"tool_error_count"`
	ToolWallTimeMS            int    `json:"tool_wall_time_ms"`
	ReportCount               int    `json:"report_count"`
	SnapshotCount             int    `json:"snapshot_count"`
	LatencySessionCount       int    `json:"latency_session_count"`
	AvgFirstResponseLatencyMS int    `json:"avg_first_response_latency_ms"`
	DurationSessionCount      int    `json:"duration_session_count"`
	AvgSessionDurationMS      int    `json:"avg_session_duration_ms"`
}

type DashboardProjectInsightModelResp struct {
	Model        string  `json:"model"`
	SessionCount int     `json:"session_count"`
	Share        float64 `json:"share"`
}

type DashboardProjectInsightProviderResp struct {
	Provider     string  `json:"provider"`
	SessionCount int     `json:"session_count"`
	Share        float64 `json:"share"`
}

type DashboardProjectInsightToolResp struct {
	Tool          string  `json:"tool"`
	CallCount     int     `json:"call_count"`
	ErrorCount    int     `json:"error_count"`
	ErrorRate     float64 `json:"error_rate"`
	WallTimeMS    int     `json:"wall_time_ms"`
	AvgWallTimeMS int     `json:"avg_wall_time_ms"`
	SessionCount  int     `json:"session_count"`
	Share         float64 `json:"share"`
}

type DashboardProjectInsightsResp struct {
	SchemaVersion              string                                `json:"schema_version"`
	ProjectID                  string                                `json:"project_id"`
	Days                       []DashboardProjectInsightDayResp      `json:"days"`
	Models                     []DashboardProjectInsightModelResp    `json:"models"`
	Providers                  []DashboardProjectInsightProviderResp `json:"providers"`
	Tools                      []DashboardProjectInsightToolResp     `json:"tools"`
	KnownModelSessions         int                                   `json:"known_model_sessions"`
	UnknownModelSessions       int                                   `json:"unknown_model_sessions"`
	KnownProviderSessions      int                                   `json:"known_provider_sessions"`
	UnknownProviderSessions    int                                   `json:"unknown_provider_sessions"`
	KnownLatencySessions       int                                   `json:"known_latency_sessions"`
	UnknownLatencySessions     int                                   `json:"unknown_latency_sessions"`
	KnownDurationSessions      int                                   `json:"known_duration_sessions"`
	UnknownDurationSessions    int                                   `json:"unknown_duration_sessions"`
	AvgFirstResponseLatencyMS  int                                   `json:"avg_first_response_latency_ms"`
	AvgSessionDurationMS       int                                   `json:"avg_session_duration_ms"`
	TotalCachedInputTokens     int                                   `json:"total_cached_input_tokens"`
	TotalReasoningOutputTokens int                                   `json:"total_reasoning_output_tokens"`
	TotalFunctionCalls         int                                   `json:"total_function_calls"`
	TotalToolErrors            int                                   `json:"total_tool_errors"`
	TotalToolWallTimeMS        int                                   `json:"total_tool_wall_time_ms"`
	AvgToolWallTimeMS          int                                   `json:"avg_tool_wall_time_ms"`
	SessionsWithFunctionCalls  int                                   `json:"sessions_with_function_calls"`
	SessionsWithToolErrors     int                                   `json:"sessions_with_tool_errors"`
	ResearchStatus             *ReportResearchStatusResp             `json:"research_status,omitempty"`
}
