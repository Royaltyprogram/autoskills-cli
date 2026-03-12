package response

import "time"

type AuthUserResp struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
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
	TokenPrefix string     `json:"token_prefix"`
	Label       string     `json:"label"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
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
	AgentID       string    `json:"agent_id"`
	DeviceID      string    `json:"device_id"`
	OrgID         string    `json:"org_id"`
	OrgName       string    `json:"org_name"`
	UserID        string    `json:"user_id"`
	UserName      string    `json:"user_name"`
	UserEmail     string    `json:"user_email"`
	Status        string    `json:"status"`
	ConsentScopes []string  `json:"consent_scopes"`
	RegisteredAt  time.Time `json:"registered_at"`
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
	SessionID               string                            `json:"session_id"`
	ProjectID               string                            `json:"project_id"`
	RecommendationCount     int                               `json:"recommendation_count"`
	LatestRecommendationIDs []string                          `json:"latest_recommendation_ids"`
	RecordedAt              time.Time                         `json:"recorded_at"`
	ResearchStatus          *RecommendationResearchStatusResp `json:"research_status,omitempty"`
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
	Timestamp              time.Time      `json:"timestamp"`
}

type SessionSummaryListResp struct {
	Items []SessionSummaryItem `json:"items"`
}

type ChangePlanStepResp struct {
	Type            string         `json:"type"`
	Action          string         `json:"action"`
	TargetFile      string         `json:"target_file"`
	Summary         string         `json:"summary"`
	SettingsUpdates map[string]any `json:"settings_updates"`
	ContentPreview  string         `json:"content_preview"`
}

type HarnessAssertionResp struct {
	Kind        string `json:"kind"`
	Equals      int    `json:"equals,omitempty"`
	Contains    string `json:"contains,omitempty"`
	NotContains string `json:"not_contains,omitempty"`
}

type HarnessSpecResp struct {
	Version       int                    `json:"version"`
	Name          string                 `json:"name"`
	Goal          string                 `json:"goal,omitempty"`
	TargetPaths   []string               `json:"target_paths,omitempty"`
	SetupCommands []string               `json:"setup_commands,omitempty"`
	TestCommands  []string               `json:"test_commands"`
	Assertions    []HarnessAssertionResp `json:"assertions,omitempty"`
	AntiGoals     []string               `json:"anti_goals,omitempty"`
}

type RecommendationResp struct {
	ID               string               `json:"id"`
	ProjectID        string               `json:"project_id"`
	Kind             string               `json:"kind"`
	Title            string               `json:"title"`
	Summary          string               `json:"summary"`
	Reason           string               `json:"reason"`
	Explanation      string               `json:"explanation"`
	ExpectedBenefit  string               `json:"expected_benefit"`
	Risk             string               `json:"risk"`
	ExpectedImpact   string               `json:"expected_impact"`
	Status           string               `json:"status"`
	Score            float64              `json:"score"`
	TargetTool       string               `json:"target_tool"`
	TargetFileHint   string               `json:"target_file_hint"`
	ResearchProvider string               `json:"research_provider"`
	ResearchModel    string               `json:"research_model"`
	Evidence         []string             `json:"evidence"`
	ChangePlan       []ChangePlanStepResp `json:"change_plan"`
	HarnessSpec      *HarnessSpecResp     `json:"harness_spec,omitempty"`
	SettingsUpdates  map[string]any       `json:"settings_updates"`
	RawSuggestion    string               `json:"raw_suggestion"`
	CreatedAt        time.Time            `json:"created_at"`
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
	Operation       string         `json:"operation"`
	Summary         string         `json:"summary"`
	SettingsUpdates map[string]any `json:"settings_updates"`
	ContentPreview  string         `json:"content_preview"`
}

type ApplyPlanResp struct {
	ApplyID        string             `json:"apply_id"`
	ExperimentID   string             `json:"experiment_id"`
	Recommendation RecommendationResp `json:"recommendation"`
	Status         string             `json:"status"`
	PolicyMode     string             `json:"policy_mode"`
	PolicyReason   string             `json:"policy_reason"`
	ApprovalStatus string             `json:"approval_status"`
	Decision       string             `json:"decision"`
	ReviewedBy     string             `json:"reviewed_by"`
	ReviewNote     string             `json:"review_note"`
	ReviewedAt     *time.Time         `json:"reviewed_at"`
	PatchPreview   []PatchPreviewItem `json:"patch_preview"`
	RequestedAt    time.Time          `json:"requested_at"`
}

type ApplyResultResp struct {
	ApplyID      string    `json:"apply_id"`
	ExperimentID string    `json:"experiment_id"`
	Status       string    `json:"status"`
	AppliedAt    time.Time `json:"applied_at"`
	RolledBack   bool      `json:"rolled_back"`
}

type HarnessCommandResultResp struct {
	Phase      string `json:"phase"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output,omitempty"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
}

type HarnessRunResp struct {
	ID               string                     `json:"id"`
	ProjectID        string                     `json:"project_id"`
	RecommendationID string                     `json:"recommendation_id,omitempty"`
	ApplyID          string                     `json:"apply_id,omitempty"`
	SpecFile         string                     `json:"spec_file"`
	Name             string                     `json:"name"`
	Goal             string                     `json:"goal,omitempty"`
	Status           string                     `json:"status"`
	Passed           bool                       `json:"passed"`
	Reason           string                     `json:"reason,omitempty"`
	RootDir          string                     `json:"root_dir,omitempty"`
	DurationMS       int64                      `json:"duration_ms"`
	TriggeredBy      string                     `json:"triggered_by,omitempty"`
	Commands         []HarnessCommandResultResp `json:"commands"`
	StartedAt        time.Time                  `json:"started_at"`
	CompletedAt      time.Time                  `json:"completed_at"`
	CreatedAt        time.Time                  `json:"created_at"`
}

type ChangePlanReviewResp struct {
	ApplyID        string     `json:"apply_id"`
	Status         string     `json:"status"`
	PolicyMode     string     `json:"policy_mode"`
	PolicyReason   string     `json:"policy_reason"`
	ApprovalStatus string     `json:"approval_status"`
	Decision       string     `json:"decision"`
	ReviewedBy     string     `json:"reviewed_by"`
	ReviewNote     string     `json:"review_note"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
}

type ApplyHistoryItem struct {
	ApplyID          string             `json:"apply_id"`
	ExperimentID     string             `json:"experiment_id"`
	RecommendationID string             `json:"recommendation_id"`
	Status           string             `json:"status"`
	PolicyMode       string             `json:"policy_mode"`
	PolicyReason     string             `json:"policy_reason"`
	ApprovalStatus   string             `json:"approval_status"`
	Decision         string             `json:"decision"`
	Scope            string             `json:"scope"`
	RequestedBy      string             `json:"requested_by"`
	ReviewedBy       string             `json:"reviewed_by"`
	ReviewNote       string             `json:"review_note"`
	RequestedAt      time.Time          `json:"requested_at"`
	ReviewedAt       *time.Time         `json:"reviewed_at"`
	AppliedAt        *time.Time         `json:"applied_at"`
	LastReportedAt   *time.Time         `json:"last_reported_at"`
	RolledBackAt     *time.Time         `json:"rolled_back_at"`
	AppliedFile      string             `json:"applied_file"`
	AppliedSettings  map[string]any     `json:"applied_settings"`
	AppliedText      string             `json:"applied_text"`
	PatchPreview     []PatchPreviewItem `json:"patch_preview"`
	RolledBack       bool               `json:"rolled_back"`
}

type ApplyHistoryResp struct {
	Items []ApplyHistoryItem `json:"items"`
}

type PendingApplyItem struct {
	ApplyID          string             `json:"apply_id"`
	ExperimentID     string             `json:"experiment_id"`
	RecommendationID string             `json:"recommendation_id"`
	Action           string             `json:"action"`
	Status           string             `json:"status"`
	PolicyMode       string             `json:"policy_mode"`
	PolicyReason     string             `json:"policy_reason"`
	ApprovalStatus   string             `json:"approval_status"`
	Scope            string             `json:"scope"`
	RequestedBy      string             `json:"requested_by"`
	RequestedAt      time.Time          `json:"requested_at"`
	Note             string             `json:"note"`
	PatchPreview     []PatchPreviewItem `json:"patch_preview"`
}

type ExperimentSummaryResp struct {
	ExperimentID         string     `json:"experiment_id"`
	ProjectID            string     `json:"project_id"`
	RecommendationID     string     `json:"recommendation_id"`
	ApplyID              string     `json:"apply_id"`
	Status               string     `json:"status"`
	Decision             string     `json:"decision"`
	DecisionReason       string     `json:"decision_reason"`
	EvaluationMode       string     `json:"evaluation_mode"`
	EvaluationModel      string     `json:"evaluation_model"`
	EvaluationDecision   string     `json:"evaluation_decision"`
	EvaluationConfidence string     `json:"evaluation_confidence"`
	EvaluationSummary    string     `json:"evaluation_summary"`
	TargetMetric         string     `json:"target_metric"`
	RequestedBy          string     `json:"requested_by"`
	Scope                string     `json:"scope"`
	BaselineSessions     int        `json:"baseline_sessions"`
	BaselineQueries      int        `json:"baseline_queries"`
	PostApplySessions    int        `json:"post_apply_sessions"`
	PostApplyQueries     int        `json:"post_apply_queries"`
	CreatedAt            time.Time  `json:"created_at"`
	ApprovedAt           *time.Time `json:"approved_at"`
	AppliedAt            *time.Time `json:"applied_at"`
	EvaluatedAt          *time.Time `json:"evaluated_at"`
	LastObservedAt       *time.Time `json:"last_observed_at"`
	ResolvedAt           *time.Time `json:"resolved_at"`
}

type ExperimentListResp struct {
	Items []ExperimentSummaryResp `json:"items"`
}

type PendingApplyResp struct {
	Items []PendingApplyItem `json:"items"`
}

type ImpactSummaryItem struct {
	ApplyID                         string     `json:"apply_id"`
	ExperimentID                    string     `json:"experiment_id"`
	RecommendationID                string     `json:"recommendation_id"`
	Status                          string     `json:"status"`
	EvaluationMode                  string     `json:"evaluation_mode"`
	EvaluationModel                 string     `json:"evaluation_model"`
	EvaluationDecision              string     `json:"evaluation_decision"`
	EvaluationConfidence            string     `json:"evaluation_confidence"`
	EvaluationSummary               string     `json:"evaluation_summary"`
	AppliedAt                       *time.Time `json:"applied_at"`
	SessionsBefore                  int        `json:"sessions_before"`
	SessionsAfter                   int        `json:"sessions_after"`
	QueriesBefore                   int        `json:"queries_before"`
	QueriesAfter                    int        `json:"queries_after"`
	AvgInputTokensPerQueryBefore    float64    `json:"avg_input_tokens_per_query_before"`
	AvgInputTokensPerQueryAfter     float64    `json:"avg_input_tokens_per_query_after"`
	AvgOutputTokensPerQueryBefore   float64    `json:"avg_output_tokens_per_query_before"`
	AvgOutputTokensPerQueryAfter    float64    `json:"avg_output_tokens_per_query_after"`
	AvgTokensPerQueryBefore         float64    `json:"avg_tokens_per_query_before"`
	AvgTokensPerQueryAfter          float64    `json:"avg_tokens_per_query_after"`
	AvgInputTokensPerSessionBefore  float64    `json:"avg_input_tokens_per_session_before"`
	AvgInputTokensPerSessionAfter   float64    `json:"avg_input_tokens_per_session_after"`
	AvgOutputTokensPerSessionBefore float64    `json:"avg_output_tokens_per_session_before"`
	AvgOutputTokensPerSessionAfter  float64    `json:"avg_output_tokens_per_session_after"`
	AvgTokensPerSessionBefore       float64    `json:"avg_tokens_per_session_before"`
	AvgTokensPerSessionAfter        float64    `json:"avg_tokens_per_session_after"`
	InputTokensPerQueryDelta        float64    `json:"input_tokens_per_query_delta"`
	OutputTokensPerQueryDelta       float64    `json:"output_tokens_per_query_delta"`
	TokensPerQueryDelta             float64    `json:"tokens_per_query_delta"`
	InputTokensPerSessionDelta      float64    `json:"input_tokens_per_session_delta"`
	OutputTokensPerSessionDelta     float64    `json:"output_tokens_per_session_delta"`
	TokensPerSessionDelta           float64    `json:"tokens_per_session_delta"`
	Interpretation                  string     `json:"interpretation"`
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

type RecommendationResearchStatusResp struct {
	State               string     `json:"state"`
	Summary             string     `json:"summary"`
	Provider            string     `json:"provider"`
	Model               string     `json:"model"`
	MinimumSessions     int        `json:"minimum_sessions"`
	SessionCount        int        `json:"session_count"`
	RawQueryCount       int        `json:"raw_query_count"`
	RecommendationCount int        `json:"recommendation_count"`
	TriggerSessionID    string     `json:"trigger_session_id"`
	LastError           string     `json:"last_error"`
	TriggeredAt         *time.Time `json:"triggered_at"`
	StartedAt           *time.Time `json:"started_at"`
	CompletedAt         *time.Time `json:"completed_at"`
	LastSuccessfulAt    *time.Time `json:"last_successful_at"`
	LastDurationMS      int        `json:"last_duration_ms"`
}

type DashboardOverviewResp struct {
	OrgID                     string                            `json:"org_id"`
	TotalDevices              int                               `json:"total_devices"`
	TotalProjects             int                               `json:"total_projects"`
	TotalSessions             int                               `json:"total_sessions"`
	ActiveRecommendations     int                               `json:"active_recommendations"`
	ActiveExperimentCount     int                               `json:"active_experiment_count"`
	PendingReviewCount        int                               `json:"pending_review_count"`
	ApprovedQueueCount        int                               `json:"approved_queue_count"`
	SuccessfulRolloutCount    int                               `json:"successful_rollout_count"`
	FailedExecutionCount      int                               `json:"failed_execution_count"`
	TotalInputTokens          int                               `json:"total_input_tokens"`
	TotalOutputTokens         int                               `json:"total_output_tokens"`
	TotalTokens               int                               `json:"total_tokens"`
	AvgInputTokensPerQuery    float64                           `json:"avg_input_tokens_per_query"`
	AvgOutputTokensPerQuery   float64                           `json:"avg_output_tokens_per_query"`
	AvgTokensPerQuery         float64                           `json:"avg_tokens_per_query"`
	AvgInputTokensPerSession  float64                           `json:"avg_input_tokens_per_session"`
	AvgOutputTokensPerSession float64                           `json:"avg_output_tokens_per_session"`
	AvgTokensPerSession       float64                           `json:"avg_tokens_per_session"`
	AvgQueriesPerSession      float64                           `json:"avg_queries_per_session"`
	RecommendationApplyRate   float64                           `json:"recommendation_apply_rate"`
	RollbackRate              float64                           `json:"rollback_rate"`
	HarnessRunCount           int                               `json:"harness_run_count"`
	HarnessFailureCount       int                               `json:"harness_failure_count"`
	HarnessPassRate           float64                           `json:"harness_pass_rate"`
	LatestHarnessStatus       string                            `json:"latest_harness_status,omitempty"`
	LatestHarnessName         string                            `json:"latest_harness_name,omitempty"`
	LastFailingHarnessName    string                            `json:"last_failing_harness_name,omitempty"`
	LatestHarnessAt           *time.Time                        `json:"latest_harness_at,omitempty"`
	ActionSummary             string                            `json:"action_summary"`
	OutcomeSummary            string                            `json:"outcome_summary"`
	ResearchProvider          string                            `json:"research_provider"`
	ResearchMode              string                            `json:"research_mode"`
	LastIngestedAt            *time.Time                        `json:"last_ingested_at"`
	ResearchStatus            *RecommendationResearchStatusResp `json:"research_status,omitempty"`
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
	ApprovalCount             int    `json:"approval_count"`
	AppliedCount              int    `json:"applied_count"`
	RollbackCount             int    `json:"rollback_count"`
	HarnessPassCount          int    `json:"harness_pass_count"`
	HarnessFailCount          int    `json:"harness_fail_count"`
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
	HarnessRunCount            int                                   `json:"harness_run_count"`
	HarnessPassCount           int                                   `json:"harness_pass_count"`
	HarnessFailCount           int                                   `json:"harness_fail_count"`
	HarnessPassRate            float64                               `json:"harness_pass_rate"`
	LatestHarnessStatus        string                                `json:"latest_harness_status,omitempty"`
	LatestHarnessName          string                                `json:"latest_harness_name,omitempty"`
	LastFailingHarnessName     string                                `json:"last_failing_harness_name,omitempty"`
	LatestHarnessAt            *time.Time                            `json:"latest_harness_at,omitempty"`
	ResearchStatus             *RecommendationResearchStatusResp     `json:"research_status,omitempty"`
}
