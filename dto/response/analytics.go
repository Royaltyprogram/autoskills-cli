package response

import "time"

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
	SessionID               string    `json:"session_id"`
	ProjectID               string    `json:"project_id"`
	RecommendationCount     int       `json:"recommendation_count"`
	LatestRecommendationIDs []string  `json:"latest_recommendation_ids"`
	RecordedAt              time.Time `json:"recorded_at"`
}

type SessionSummaryItem struct {
	ID                       string             `json:"id"`
	ProjectID                string             `json:"project_id"`
	Tool                     string             `json:"tool"`
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
	TaskType                 string             `json:"task_type"`
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
	SettingsUpdates  map[string]any       `json:"settings_updates"`
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
	ApplyID    string    `json:"apply_id"`
	Status     string    `json:"status"`
	AppliedAt  time.Time `json:"applied_at"`
	RolledBack bool      `json:"rolled_back"`
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
	RecommendationID string             `json:"recommendation_id"`
	Status           string             `json:"status"`
	PolicyMode       string             `json:"policy_mode"`
	PolicyReason     string             `json:"policy_reason"`
	ApprovalStatus   string             `json:"approval_status"`
	Scope            string             `json:"scope"`
	RequestedBy      string             `json:"requested_by"`
	RequestedAt      time.Time          `json:"requested_at"`
	PatchPreview     []PatchPreviewItem `json:"patch_preview"`
}

type PendingApplyResp struct {
	Items []PendingApplyItem `json:"items"`
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
	TotalDevices            int             `json:"total_devices"`
	TotalProjects           int             `json:"total_projects"`
	TotalSessions           int             `json:"total_sessions"`
	ActiveRecommendations   int             `json:"active_recommendations"`
	PendingReviewCount      int             `json:"pending_review_count"`
	ApprovedQueueCount      int             `json:"approved_queue_count"`
	SuccessfulRolloutCount  int             `json:"successful_rollout_count"`
	FailedExecutionCount    int             `json:"failed_execution_count"`
	TotalEstimatedCost      float64         `json:"total_estimated_cost"`
	AvgTokensPerQuery       float64         `json:"avg_tokens_per_query"`
	AvgToolCallsPerQuery    float64         `json:"avg_tool_calls_per_query"`
	PermissionRejectRate    float64         `json:"permission_reject_rate"`
	RetryRate               float64         `json:"retry_rate"`
	RecommendationApplyRate float64         `json:"recommendation_apply_rate"`
	InferredAcceptRate      float64         `json:"inferred_accept_rate"`
	RollbackRate            float64         `json:"rollback_rate"`
	PrimaryTaskType         string          `json:"primary_task_type"`
	ActionSummary           string          `json:"action_summary"`
	OutcomeSummary          string          `json:"outcome_summary"`
	ResearchProvider        string          `json:"research_provider"`
	ResearchMode            string          `json:"research_mode"`
	TopTaskTypes            []TaskBreakdown `json:"top_task_types"`
	LastIngestedAt          *time.Time      `json:"last_ingested_at"`
}
