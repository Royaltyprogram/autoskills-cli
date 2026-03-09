package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/dto/response"
)

type state struct {
	ServerURL  string `json:"server_url"`
	APIToken   string `json:"api_token"`
	OrgID      string `json:"org_id"`
	UserID     string `json:"user_id"`
	AgentID    string `json:"agent_id"`
	DeviceName string `json:"device_name"`
	Hostname   string `json:"hostname"`
	ProjectID  string `json:"project_id"`
}

const sharedWorkspaceName = "Shared workspace"

type applyBackup struct {
	ApplyID        string            `json:"apply_id"`
	ProjectID      string            `json:"project_id"`
	Files          []applyFileBackup `json:"files"`
	FilePath       string            `json:"file_path"`
	FileKind       string            `json:"file_kind"`
	OriginalExists bool              `json:"original_exists"`
	OriginalJSON   map[string]any    `json:"original_json"`
	OriginalText   string            `json:"original_text"`
}

type applyFileBackup struct {
	FilePath       string         `json:"file_path"`
	FileKind       string         `json:"file_kind"`
	OriginalExists bool           `json:"original_exists"`
	OriginalJSON   map[string]any `json:"original_json"`
	OriginalText   string         `json:"original_text"`
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

type localApplyResult struct {
	FilePath        string
	FilePaths       []string
	AppliedSettings map[string]any
	AppliedText     string
}

type codexApplyRequest struct {
	ApplyID               string           `json:"apply_id"`
	WorkingDirectory      string           `json:"working_directory"`
	AdditionalDirectories []string         `json:"additional_directories"`
	AllowedFiles          []string         `json:"allowed_files"`
	SandboxMode           string           `json:"sandbox_mode"`
	ApprovalPolicy        string           `json:"approval_policy"`
	SkipGitRepoCheck      bool             `json:"skip_git_repo_check"`
	NetworkAccessEnabled  bool             `json:"network_access_enabled"`
	Steps                 []codexApplyStep `json:"steps"`
}

type codexApplyStep struct {
	TargetFile      string         `json:"target_file"`
	Operation       string         `json:"operation"`
	Summary         string         `json:"summary"`
	SettingsUpdates map[string]any `json:"settings_updates,omitempty"`
	ContentPreview  string         `json:"content_preview,omitempty"`
}

type codexApplyResponse struct {
	ThreadID         string                 `json:"thread_id"`
	Status           string                 `json:"status"`
	Summary          string                 `json:"summary"`
	FinalResponse    string                 `json:"final_response"`
	ChangedFiles     []string               `json:"changed_files"`
	ExecutedCommands []codexExecutedCommand `json:"executed_commands"`
}

type codexExecutedCommand struct {
	Command string `json:"command"`
	Status  string `json:"status"`
}

type preflightResult struct {
	ApplyID string          `json:"apply_id"`
	Allowed bool            `json:"allowed"`
	Reason  string          `json:"reason"`
	Steps   []preflightStep `json:"steps"`
}

type preflightStep struct {
	TargetFile   string `json:"target_file"`
	Operation    string `json:"operation"`
	PreviewFile  string `json:"preview_file"`
	Guard        string `json:"guard"`
	Reason       string `json:"reason"`
	TargetSource string `json:"target_source"`
	Allowed      bool   `json:"allowed"`
}

type sessionBatchIngestResp struct {
	Uploaded int                          `json:"uploaded"`
	Items    []response.SessionIngestResp `json:"items"`
}

type apiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "login":
		return runLogin(args[1:])
	case "connect":
		return runConnect(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "session":
		return runSession(args[1:])
	case "snapshots":
		return runSnapshots(args[1:])
	case "sessions":
		return runSessions(args[1:])
	case "recommendations":
		return runRecommendations(args[1:])
	case "status":
		return runStatus(args[1:])
	case "projects":
		return runProjects(args[1:])
	case "history":
		return runHistory(args[1:])
	case "pending":
		return runPending(args[1:])
	case "impact":
		return runImpact(args[1:])
	case "audit":
		return runAudit(args[1:])
	case "sync":
		return runSync(args[1:])
	case "rollback":
		return runRollback(args[1:])
	case "apply":
		return runApply(args[1:])
	case "review":
		return runReview(args[1:])
	case "preflight":
		return runPreflight(args[1:])
	case "--help", "-h", "help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Println(`agentopt commands:
  login             authenticate with an issued CLI token and register this device
  connect           connect a local repo to the shared workspace for the current org
  snapshot          upload a config snapshot from a JSON file
  session           upload one or more session summaries from a JSON file or local Codex session files
  snapshots         list config snapshots for the shared workspace
  sessions          list recent session summaries for the shared workspace
  recommendations   list active recommendations for the shared workspace
  status            print org overview and shared workspace recommendations
  projects          show the shared workspace connected to the current org
  history           list apply history for the shared workspace
  pending           list pending apply jobs visible to the current user and shared workspace
  impact            list recommendation impact summaries for the shared workspace
  audit             list recent audit events for the current org and shared workspace
  sync              pull approved change plans and execute them locally
  rollback          restore the local config backup for a previous apply
  apply             request a change plan and optionally approve/apply it locally
  review            approve or reject a requested change plan
  preflight         validate a change plan against local guard rules`)
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", "http://127.0.0.1:8082", "server base URL")
	token := fs.String("token", os.Getenv("AGENTOPT_TOKEN"), "CLI token issued from the dashboard")
	device := fs.String("device", "", "device name")
	hostname := fs.String("hostname", "", "hostname")
	tools := fs.String("tools", "codex,claude-code", "comma separated tool names")
	platform := fs.String("platform", "", "device platform")
	consent := fs.String("consent", "config_snapshot,session_summary,execution_result", "comma separated collection scopes")
	cliVersion := fs.String("cli-version", "0.1.0-dev", "cli version")
	if err := fs.Parse(args); err != nil {
		return err
	}

	host := strings.TrimSpace(*hostname)
	if host == "" {
		var err error
		host, err = os.Hostname()
		if err != nil {
			host = "unknown-host"
		}
	}
	deviceName := strings.TrimSpace(*device)
	if deviceName == "" {
		deviceName = host
	}

	cliToken := strings.TrimSpace(*token)
	if cliToken == "" {
		prompted, err := promptInput("CLI token")
		if err != nil {
			return err
		}
		cliToken = prompted
	}
	if cliToken == "" {
		return errors.New("login requires a CLI token issued from the dashboard")
	}

	client := newAPIClient(*server, cliToken)
	req := request.CLILoginReq{
		DeviceName:    deviceName,
		Hostname:      host,
		Platform:      defaultString(*platform, runtimePlatform()),
		CLIVersion:    *cliVersion,
		Tools:         splitComma(*tools),
		ConsentScopes: splitComma(*consent),
	}
	var resp response.CLILoginResp
	if err := client.doJSON(http.MethodPost, "/api/v1/auth/cli/login", req, &resp); err != nil {
		return err
	}

	st := state{
		ServerURL:  strings.TrimRight(*server, "/"),
		APIToken:   cliToken,
		OrgID:      resp.OrgID,
		UserID:     resp.UserID,
		AgentID:    firstNonEmpty(resp.DeviceID, resp.AgentID),
		DeviceName: deviceName,
		Hostname:   host,
	}
	if err := saveState(st); err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	projectName := fs.String("project", "", "project name")
	repoHash := fs.String("repo-hash", "", "stable repo hash")
	repoPath := fs.String("repo-path", ".", "repo path")
	tool := fs.String("tool", "codex", "default tool")
	languageMix := fs.String("languages", "go=1.0", "comma separated language shares")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*projectName) == "" {
		return errors.New("connect requires --project")
	}

	st, err := loadState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	repoRoot, err := normalizeRepoPath(*repoPath)
	if err != nil {
		return err
	}

	hash := strings.TrimSpace(*repoHash)
	if hash == "" {
		hash = sanitizeID(*projectName + "-" + repoRoot)
	}

	req := request.RegisterProjectReq{
		OrgID:       st.OrgID,
		AgentID:     st.AgentID,
		Name:        *projectName,
		RepoHash:    hash,
		RepoPath:    repoRoot,
		LanguageMix: parseLanguageMix(*languageMix),
		DefaultTool: *tool,
	}
	var resp response.ProjectRegistrationResp
	if err := client.doJSON(http.MethodPost, "/api/v1/projects/register", req, &resp); err != nil {
		return err
	}

	st.ProjectID = resp.ProjectID
	if err := saveState(st); err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	filePath := fs.String("file", "", "snapshot JSON file path")
	tool := fs.String("tool", "codex", "tool name")
	profileID := fs.String("profile", "baseline", "profile id")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	settings := map[string]any{
		"approval_policy":   "review-required",
		"instructions_pack": "baseline",
		"local_guard":       "strict",
	}
	if *filePath != "" {
		if err := loadJSONFile(*filePath, &settings); err != nil {
			return err
		}
	}

	req := request.ConfigSnapshotReq{
		ProjectID:           st.ProjectID,
		Tool:                *tool,
		ProfileID:           *profileID,
		Settings:            settings,
		EnabledMCPCount:     inferEnabledMCPCount(settings),
		HooksEnabled:        inferHooksEnabled(settings),
		InstructionFiles:    inferInstructionFiles(settings),
		ConfigFingerprint:   sanitizeID(fmt.Sprintf("%s-%s-%d", st.ProjectID, *profileID, len(settings))),
		RecentConfigChanges: []string{"snapshot_collected_by_cli"},
		CapturedAt:          time.Now().UTC(),
	}
	client := newAPIClient(st.ServerURL, st.APIToken)
	var resp response.ConfigSnapshotResp
	if err := client.doJSON(http.MethodPost, "/api/v1/config-snapshots", req, &resp); err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runSession(args []string) error {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	filePath := fs.String("file", "", "session summary JSON or Codex session JSONL path")
	tool := fs.String("tool", "codex", "tool name")
	codexHome := fs.String("codex-home", "", "override Codex home used for automatic session collection")
	recent := fs.Int("recent", 1, "number of recent local Codex session JSONL files to upload when --file is omitted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *recent < 1 {
		return errors.New("--recent must be at least 1")
	}
	if strings.TrimSpace(*filePath) != "" && *recent != 1 {
		return errors.New("--recent can only be used when --file is omitted")
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}

	reqs, err := loadSessionSummaryInputs(*filePath, *tool, *codexHome, *recent)
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	items := make([]response.SessionIngestResp, 0, len(reqs))
	for _, req := range reqs {
		req.ProjectID = st.ProjectID
		if req.Tool == "" {
			req.Tool = *tool
		}
		if req.Timestamp.IsZero() {
			req.Timestamp = time.Now().UTC()
		}

		var resp response.SessionIngestResp
		if err := client.doJSON(http.MethodPost, "/api/v1/session-summaries", req, &resp); err != nil {
			return err
		}
		items = append(items, resp)
	}

	if len(items) == 1 {
		return prettyPrint(items[0])
	}
	return prettyPrint(sessionBatchIngestResp{
		Uploaded: len(items),
		Items:    items,
	})
}

func runSnapshots(args []string) error {
	fs := flag.NewFlagSet("snapshots", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ConfigSnapshotListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/config-snapshots?project_id="+url.QueryEscape(st.ProjectID), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(projectScopedItems(st, resp.Items))
}

func runSessions(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	limit := fs.Int("limit", 5, "max number of recent sessions")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.SessionSummaryListResp
	path := fmt.Sprintf("/api/v1/session-summaries?project_id=%s&limit=%d", url.QueryEscape(st.ProjectID), *limit)
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return prettyPrint(projectScopedItems(st, resp.Items))
}

func runRecommendations(args []string) error {
	fs := flag.NewFlagSet("recommendations", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)
	path := "/api/v1/recommendations?project_id=" + url.QueryEscape(st.ProjectID)
	var resp response.RecommendationListResp
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return prettyPrint(projectScopedItems(st, resp.Items))
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var overview response.DashboardOverviewResp
	if err := client.doJSON(http.MethodGet, "/api/v1/dashboard/overview?org_id="+url.QueryEscape(st.OrgID), nil, &overview); err != nil {
		return err
	}
	var recs response.RecommendationListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/recommendations?project_id="+url.QueryEscape(st.ProjectID), nil, &recs); err != nil {
		return err
	}

	payload := map[string]any{
		"project_id":      st.ProjectID,
		"project_name":    sharedWorkspaceName,
		"overview":        overview,
		"recommendations": recs.Items,
	}
	return prettyPrint(payload)
}

func runProjects(args []string) error {
	fs := flag.NewFlagSet("projects", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ProjectListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/projects?org_id="+url.QueryEscape(st.OrgID), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runHistory(args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ApplyHistoryResp
	if err := client.doJSON(http.MethodGet, "/api/v1/applies?project_id="+url.QueryEscape(st.ProjectID), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(projectScopedItems(st, resp.Items))
}

func runPending(args []string) error {
	fs := flag.NewFlagSet("pending", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.PendingApplyResp
	path := fmt.Sprintf("/api/v1/applies/pending?project_id=%s&user_id=%s", url.QueryEscape(st.ProjectID), url.QueryEscape(st.UserID))
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return prettyPrint(projectScopedItems(st, resp.Items))
}

func runImpact(args []string) error {
	fs := flag.NewFlagSet("impact", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ImpactSummaryResp
	if err := client.doJSON(http.MethodGet, "/api/v1/impact?project_id="+url.QueryEscape(st.ProjectID), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(projectScopedItems(st, resp.Items))
}

func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadState()
	if err != nil {
		return err
	}
	path := "/api/v1/audits?org_id=" + url.QueryEscape(st.OrgID)

	client := newAPIClient(st.ServerURL, st.APIToken)
	var resp response.AuditListResp
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	targetConfig := fs.String("target-config", "", "override local config path for pending apply jobs")
	watch := fs.Bool("watch", false, "poll for pending apply jobs until interrupted")
	interval := fs.Duration("interval", 15*time.Second, "poll interval in watch mode")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	if !*watch {
		return runSyncOnce(st, client, *targetConfig)
	}
	if *interval <= 0 {
		return errors.New("sync --interval must be greater than zero")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	if err := runSyncOnce(st, client, *targetConfig); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Println(`{"watch":"stopped"}`)
			return nil
		case <-ticker.C:
			if err := runSyncOnce(st, client, *targetConfig); err != nil {
				return err
			}
		}
	}
}

func runSyncOnce(st state, client *apiClient, targetConfig string) error {
	path := fmt.Sprintf("/api/v1/applies/pending?project_id=%s&user_id=%s", url.QueryEscape(st.ProjectID), url.QueryEscape(st.UserID))
	var pending response.PendingApplyResp
	if err := client.doJSON(http.MethodGet, path, nil, &pending); err != nil {
		return err
	}

	results := make([]response.ApplyResultResp, 0, len(pending.Items))
	failedApplyIDs := make([]string, 0)
	for _, item := range pending.Items {
		localResult, err := executeLocalApply(st, item.ApplyID, item.PatchPreview, targetConfig)
		if err != nil {
			result, reportErr := reportApplyResult(client, request.ApplyResultReq{
				ApplyID: item.ApplyID,
				Success: false,
				Note:    fmt.Sprintf("local apply failed during sync: %v", err),
			})
			if reportErr != nil {
				return fmt.Errorf("apply %s failed locally: %v; failed to report result: %w", item.ApplyID, err, reportErr)
			}
			results = append(results, result)
			failedApplyIDs = append(failedApplyIDs, item.ApplyID)
			continue
		}

		result, err := reportApplyResult(client, request.ApplyResultReq{
			ApplyID:         item.ApplyID,
			Success:         true,
			Note:            "applied by agentopt sync",
			AppliedFile:     localResult.FilePath,
			AppliedSettings: localResult.AppliedSettings,
			AppliedText:     localResult.AppliedText,
		})
		if err != nil {
			return err
		}
		results = append(results, result)
	}

	if err := prettyPrint(map[string]any{
		"project_id":    st.ProjectID,
		"project_name":  sharedWorkspaceName,
		"pending_count": len(pending.Items),
		"failed_count":  len(failedApplyIDs),
		"results":       results,
	}); err != nil {
		return err
	}
	if len(failedApplyIDs) > 0 {
		return fmt.Errorf("sync completed with failed applies: %s", strings.Join(failedApplyIDs, ", "))
	}
	return nil
}

func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	recommendationID := fs.String("recommendation-id", "", "recommendation id")
	targetConfig := fs.String("target-config", "", "local config path override")
	yes := fs.Bool("yes", false, "apply immediately after preview")
	scope := fs.String("scope", "user", "apply scope")
	note := fs.String("note", "applied by agentopt CLI", "apply result note")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*recommendationID) == "" {
		return errors.New("apply requires --recommendation-id")
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var plan response.ApplyPlanResp
	if err := client.doJSON(http.MethodPost, "/api/v1/recommendations/apply", request.ApplyRecommendationReq{
		RecommendationID: *recommendationID,
		RequestedBy:      st.UserID,
		Scope:            *scope,
	}, &plan); err != nil {
		return err
	}

	if err := prettyPrint(plan); err != nil {
		return err
	}
	if !*yes {
		return nil
	}

	if plan.PolicyMode != "auto_approved" && plan.ApprovalStatus != "approved" {
		if _, err := reviewChangePlan(client, plan.ApplyID, "approve", st.UserID, "approved by local cli"); err != nil {
			return err
		}
	}

	localResult, err := executeLocalApply(st, plan.ApplyID, plan.PatchPreview, *targetConfig)
	if err != nil {
		if _, reportErr := reportApplyResult(client, request.ApplyResultReq{
			ApplyID: plan.ApplyID,
			Success: false,
			Note:    fmt.Sprintf("local apply failed: %v", err),
		}); reportErr != nil {
			return fmt.Errorf("local apply failed: %v; failed to report result: %w", err, reportErr)
		}
		return err
	}

	result, err := reportApplyResult(client, request.ApplyResultReq{
		ApplyID:         plan.ApplyID,
		Success:         true,
		Note:            *note,
		AppliedFile:     localResult.FilePath,
		AppliedSettings: localResult.AppliedSettings,
		AppliedText:     localResult.AppliedText,
	})
	if err != nil {
		return err
	}
	return prettyPrint(result)
}

func runReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	applyID := fs.String("apply-id", "", "change plan id")
	decision := fs.String("decision", "approve", "approve or reject")
	reviewedBy := fs.String("reviewed-by", "", "reviewer id override")
	note := fs.String("note", "", "review note")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*applyID) == "" {
		return errors.New("review requires --apply-id")
	}

	st, err := loadState()
	if err != nil {
		return err
	}
	reviewer := *reviewedBy
	if strings.TrimSpace(reviewer) == "" {
		reviewer = st.UserID
	}

	client := newAPIClient(st.ServerURL, st.APIToken)
	resp, err := reviewChangePlan(client, *applyID, *decision, reviewer, *note)
	if err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runPreflight(args []string) error {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	applyID := fs.String("apply-id", "", "change plan id")
	targetConfig := fs.String("target-config", "", "local config path override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*applyID) == "" {
		return errors.New("preflight requires --apply-id")
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)
	item, err := fetchApplyHistoryItem(client, st.ProjectID, *applyID)
	if err != nil {
		return err
	}

	result, err := preflightLocalApply(st, item.ApplyID, item.PatchPreview, *targetConfig)
	if err != nil {
		return err
	}
	return prettyPrint(result)
}

func runRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	applyID := fs.String("apply-id", "", "apply id to roll back")
	note := fs.String("note", "rollback executed by agentopt CLI", "rollback note")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*applyID) == "" {
		return errors.New("rollback requires --apply-id")
	}

	st, err := loadState()
	if err != nil {
		return err
	}
	backup, err := loadApplyBackup(*applyID)
	if err != nil {
		return err
	}

	files := normalizeApplyBackupFiles(backup)
	for i := len(files) - 1; i >= 0; i-- {
		file := files[i]
		if file.OriginalExists {
			if err := os.MkdirAll(filepath.Dir(file.FilePath), 0o755); err != nil {
				return err
			}
			switch file.FileKind {
			case "text_append", "text_replace":
				if err := os.WriteFile(file.FilePath, []byte(file.OriginalText), 0o644); err != nil {
					return err
				}
			default:
				data, err := json.MarshalIndent(file.OriginalJSON, "", "  ")
				if err != nil {
					return err
				}
				data = append(data, '\n')
				if err := os.WriteFile(file.FilePath, data, 0o644); err != nil {
					return err
				}
			}
		} else {
			if err := os.Remove(file.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}

	client := newAPIClient(st.ServerURL, st.APIToken)
	var result response.ApplyResultResp
	if err := client.doJSON(http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:         backup.ApplyID,
		Success:         true,
		Note:            *note,
		AppliedFile:     rollbackAppliedFile(files),
		AppliedSettings: rollbackAppliedSettings(files),
		AppliedText:     rollbackAppliedText(files),
		RolledBack:      true,
	}, &result); err != nil {
		return err
	}
	if err := deleteApplyBackup(backup.ApplyID); err != nil {
		return err
	}
	return prettyPrint(result)
}

func newAPIClient(baseURL, token string) *apiClient {
	return &apiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func reportApplyResult(client *apiClient, req request.ApplyResultReq) (response.ApplyResultResp, error) {
	var resp response.ApplyResultResp
	err := client.doJSON(http.MethodPost, "/api/v1/applies/result", req, &resp)
	return resp, err
}

func (c *apiClient) doJSON(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("X-AgentOpt-Token", c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	if resp.StatusCode >= 400 || env.Code != 0 {
		if env.Message == "" {
			env.Message = string(raw)
		}
		return fmt.Errorf("request failed: %s", env.Message)
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func loadState() (state, error) {
	path, err := stateFilePath()
	if err != nil {
		return state{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state{}, errors.New("agentopt state not found; run `agentopt login` first")
		}
		return state{}, err
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, err
	}
	return st, nil
}

func loadProjectState() (state, error) {
	st, err := loadState()
	if err != nil {
		return state{}, err
	}
	if st.ProjectID == "" {
		return state{}, errors.New("shared workspace is not connected; run `agentopt connect` first")
	}
	return st, nil
}

func saveState(st state) error {
	path, err := stateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func stateFilePath() (string, error) {
	if root := os.Getenv("AGENTOPT_HOME"); root != "" {
		return filepath.Join(root, "state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agentopt", "state.json"), nil
}

func normalizeRepoPath(path string) (string, error) {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "" {
		cleaned = "."
	}
	absolute, err := filepath.Abs(cleaned)
	if err != nil {
		return "", err
	}
	return absolute, nil
}

func projectScopedItems(st state, items any) map[string]any {
	return map[string]any{
		"project_id":   st.ProjectID,
		"project_name": sharedWorkspaceName,
		"items":        items,
	}
}

func prettyPrint(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func promptInput(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	reader := bufio.NewReader(os.Stdin)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func loadJSONFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func readOptionalJSONMap(path string, out *map[string]any) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			*out = map[string]any{}
			return false, nil
		}
		return false, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		*out = map[string]any{}
		return true, nil
	}
	return true, json.Unmarshal(data, out)
}

func loadSessionSummaryInputs(path, tool, codexHome string, recent int) ([]request.SessionSummaryReq, error) {
	if strings.TrimSpace(path) != "" {
		if strings.EqualFold(filepath.Ext(path), ".jsonl") {
			req, err := collectCodexSessionSummary(path, tool)
			if err != nil {
				return nil, err
			}
			return []request.SessionSummaryReq{req}, nil
		}
		var req request.SessionSummaryReq
		if err := loadJSONFile(path, &req); err != nil {
			return nil, err
		}
		return []request.SessionSummaryReq{req}, nil
	}

	sessionPaths, err := recentCodexSessionFiles(codexHome, recent)
	if err != nil {
		return nil, err
	}
	reqs := make([]request.SessionSummaryReq, 0, len(sessionPaths))
	for _, sessionPath := range sessionPaths {
		req, err := collectCodexSessionSummary(sessionPath, tool)
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, req)
	}
	return reqs, nil
}

func parseLanguageMix(raw string) map[string]float64 {
	out := map[string]float64{}
	for _, item := range splitComma(raw) {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		var value float64
		fmt.Sscanf(parts[1], "%f", &value)
		out[parts[0]] = value
	}
	if len(out) == 0 {
		out["go"] = 1
	}
	return out
}

func splitComma(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func sanitizeID(raw string) string {
	raw = strings.ToLower(raw)
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ".", "-", ":", "-")
	raw = replacer.Replace(raw)
	raw = strings.Trim(raw, "-")
	if raw == "" {
		return "unknown"
	}
	return raw
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func mergeMap(dst, src map[string]any) {
	for key, value := range src {
		dst[key] = value
	}
}

func executeLocalApply(st state, applyID string, previews []response.PatchPreviewItem, targetOverride string) (localApplyResult, error) {
	preflight, err := preflightLocalApply(st, applyID, previews, targetOverride)
	if err != nil {
		return localApplyResult{}, err
	}
	if !preflight.Allowed {
		return localApplyResult{}, fmt.Errorf("local guard rejected apply %s: %s", applyID, preflight.Reason)
	}

	backups, err := createApplyBackups(preflight, previews)
	if err != nil {
		return localApplyResult{}, err
	}

	req, err := newCodexApplyRequest(applyID, preflight, previews)
	if err != nil {
		return localApplyResult{}, err
	}
	codexResp, err := runCodexApply(req)
	if err != nil {
		restoreErr := rollbackAppliedSteps(backups)
		if restoreErr != nil {
			return localApplyResult{}, fmt.Errorf("apply failed: %v; rollback failed: %w", err, restoreErr)
		}
		return localApplyResult{}, err
	}
	if err := validateCodexApply(req, preflight, previews, codexResp); err != nil {
		restoreErr := rollbackAppliedSteps(backups)
		if restoreErr != nil {
			return localApplyResult{}, fmt.Errorf("apply validation failed: %v; rollback failed: %w", err, restoreErr)
		}
		return localApplyResult{}, err
	}

	if err := saveApplyBackup(applyBackup{
		ApplyID:   applyID,
		ProjectID: st.ProjectID,
		Files:     backups,
	}); err != nil {
		restoreErr := rollbackAppliedSteps(backups)
		if restoreErr != nil {
			return localApplyResult{}, fmt.Errorf("save backup failed: %v; rollback failed: %w", err, restoreErr)
		}
		return localApplyResult{}, err
	}

	return localApplyResult{
		FilePath:        appliedFileSummary(req.AllowedFiles),
		FilePaths:       append([]string(nil), req.AllowedFiles...),
		AppliedSettings: plannedAppliedSettings(previews),
		AppliedText:     plannedAppliedText(previews),
	}, nil
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func createApplyBackups(preflight preflightResult, previews []response.PatchPreviewItem) ([]applyFileBackup, error) {
	backups := make([]applyFileBackup, 0, len(previews))
	for index, preview := range previews {
		backup, err := snapshotApplyTarget(preflight.Steps[index].TargetFile, preview)
		if err != nil {
			return nil, err
		}
		backups = append(backups, backup)
	}
	return backups, nil
}

func snapshotApplyTarget(filePath string, preview response.PatchPreviewItem) (applyFileBackup, error) {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return applyFileBackup{}, err
	}
	if isTextApplyOperation(preview.Operation) {
		originalBytes, err := os.ReadFile(filePath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return applyFileBackup{}, err
		}
		return applyFileBackup{
			FilePath:       filePath,
			FileKind:       "text_append",
			OriginalExists: err == nil,
			OriginalText:   string(originalBytes),
		}, nil
	}

	config := map[string]any{}
	originalExists, err := readOptionalJSONMap(filePath, &config)
	if err != nil {
		return applyFileBackup{}, err
	}
	return applyFileBackup{
		FilePath:       filePath,
		FileKind:       "json_merge",
		OriginalExists: originalExists,
		OriginalJSON:   cloneAnyMap(config),
	}, nil
}

func newCodexApplyRequest(applyID string, preflight preflightResult, previews []response.PatchPreviewItem) (codexApplyRequest, error) {
	workingDirectory, additionalDirectories, err := chooseApplyWorkspace(preflight.Steps)
	if err != nil {
		return codexApplyRequest{}, err
	}
	allowedFiles := make([]string, 0, len(preflight.Steps))
	steps := make([]codexApplyStep, 0, len(preflight.Steps))
	for index, step := range preflight.Steps {
		preview := previews[index]
		allowedFiles = append(allowedFiles, step.TargetFile)
		steps = append(steps, codexApplyStep{
			TargetFile:      step.TargetFile,
			Operation:       preview.Operation,
			Summary:         preview.Summary,
			SettingsUpdates: cloneAnyMap(preview.SettingsUpdates),
			ContentPreview:  preview.ContentPreview,
		})
	}
	return codexApplyRequest{
		ApplyID:               applyID,
		WorkingDirectory:      workingDirectory,
		AdditionalDirectories: additionalDirectories,
		AllowedFiles:          allowedFiles,
		SandboxMode:           "workspace-write",
		ApprovalPolicy:        "never",
		SkipGitRepoCheck:      true,
		NetworkAccessEnabled:  false,
		Steps:                 steps,
	}, nil
}

func chooseApplyWorkspace(steps []preflightStep) (string, []string, error) {
	if len(steps) == 0 {
		return "", nil, errors.New("no apply steps available")
	}

	dirs := make([]string, 0, len(steps))
	for _, step := range steps {
		dirs = append(dirs, filepath.Dir(step.TargetFile))
	}

	if cwd, err := os.Getwd(); err == nil && allWithinRoot(cwd, steps) {
		return cwd, nil, nil
	}

	root := dirs[0]
	for _, dir := range dirs[1:] {
		root = sharedPathPrefix(root, dir)
	}
	if root != "" {
		for _, dir := range dirs {
			if filepath.Clean(dir) == filepath.Clean(root) {
				return root, collectAdditionalDirectories(root, steps), nil
			}
		}
	}

	workingDirectory := dirs[0]
	return workingDirectory, collectAdditionalDirectories(workingDirectory, steps), nil
}

func sharedPathPrefix(left, right string) string {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	for !isWithinRoot(left, right) {
		parent := filepath.Dir(left)
		if parent == left {
			return left
		}
		left = parent
	}
	return left
}

func collectAdditionalDirectories(workingDirectory string, steps []preflightStep) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, step := range steps {
		dir := filepath.Dir(step.TargetFile)
		if isWithinRoot(workingDirectory, dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func allWithinRoot(root string, steps []preflightStep) bool {
	for _, step := range steps {
		if !isWithinRoot(root, step.TargetFile) {
			return false
		}
	}
	return true
}

func runCodexApply(req codexApplyRequest) (codexApplyResponse, error) {
	requestFile, err := writeCodexApplyRequest(req)
	if err != nil {
		return codexApplyResponse{}, err
	}
	defer os.Remove(requestFile)

	command, args, err := codexRunnerCommand(requestFile)
	if err != nil {
		return codexApplyResponse{}, err
	}

	timeout, err := codexApplyTimeout()
	if err != nil {
		return codexApplyResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return codexApplyResponse{}, fmt.Errorf("codex runner timed out after %s", timeout)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(string(output))
		}
		if detail == "" {
			detail = err.Error()
		}
		return codexApplyResponse{}, fmt.Errorf("codex runner failed: %s", detail)
	}

	var resp codexApplyResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return codexApplyResponse{}, fmt.Errorf("parse codex runner output: %w", err)
	}
	return resp, nil
}

func codexApplyTimeout() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("AGENTOPT_CODEX_TIMEOUT"))
	if raw == "" {
		return 10 * time.Minute, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid AGENTOPT_CODEX_TIMEOUT %q: %w", raw, err)
	}
	if timeout <= 0 {
		return 0, errors.New("AGENTOPT_CODEX_TIMEOUT must be greater than zero")
	}
	return timeout, nil
}

func writeCodexApplyRequest(req codexApplyRequest) (string, error) {
	file, err := os.CreateTemp("", "agentopt-codex-apply-*.json")
	if err != nil {
		return "", err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(req); err != nil {
		return "", err
	}
	return file.Name(), nil
}

func codexRunnerCommand(requestFile string) (string, []string, error) {
	if override := strings.TrimSpace(os.Getenv("AGENTOPT_CODEX_RUNNER")); override != "" {
		return override, []string{requestFile}, nil
	}

	script, err := locateCodexRunnerScript()
	if err != nil {
		return "", nil, err
	}
	return "node", []string{script, requestFile}, nil
}

func locateCodexRunnerScript() (string, error) {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "tools", "codex-runner", "run.mjs"))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "tools", "codex-runner", "run.mjs"),
			filepath.Join(exeDir, "..", "tools", "codex-runner", "run.mjs"),
		)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Clean(candidate), nil
		}
	}
	return "", errors.New("codex runner not found; install tools/codex-runner dependencies first")
}

func validateCodexApply(req codexApplyRequest, preflight preflightResult, previews []response.PatchPreviewItem, resp codexApplyResponse) error {
	status := strings.TrimSpace(resp.Status)
	switch status {
	case "", "applied", "completed", "ok":
	default:
		return fmt.Errorf("codex apply returned status %q: %s", status, firstNonEmpty(resp.Summary, resp.FinalResponse))
	}

	if err := validateChangedFiles(req, resp.ChangedFiles); err != nil {
		return err
	}
	for index, preview := range previews {
		if err := validateAppliedStep(preflight.Steps[index].TargetFile, preview); err != nil {
			return err
		}
	}
	return nil
}

func validateChangedFiles(req codexApplyRequest, changedFiles []string) error {
	if len(changedFiles) == 0 {
		return nil
	}
	allowed := map[string]struct{}{}
	for _, file := range req.AllowedFiles {
		allowed[filepath.Clean(file)] = struct{}{}
	}
	for _, file := range changedFiles {
		resolved := filepath.Clean(file)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Clean(filepath.Join(req.WorkingDirectory, resolved))
		}
		if _, ok := allowed[resolved]; ok {
			continue
		}
		return fmt.Errorf("codex changed unexpected file %s", file)
	}
	return nil
}

func validateAppliedStep(filePath string, preview response.PatchPreviewItem) error {
	if isTextApplyOperation(preview.Operation) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), preview.ContentPreview) {
			return fmt.Errorf("applied file %s does not contain approved content", filePath)
		}
		return nil
	}

	current := map[string]any{}
	if _, err := readOptionalJSONMap(filePath, &current); err != nil {
		return err
	}
	for key, expected := range preview.SettingsUpdates {
		value, ok := current[key]
		if !ok {
			return fmt.Errorf("applied file %s is missing approved key %s", filePath, key)
		}
		if !sameJSONValue(value, expected) {
			return fmt.Errorf("applied file %s has unexpected value for %s", filePath, key)
		}
	}
	return nil
}

func sameJSONValue(left, right any) bool {
	leftData, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightData, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return bytes.Equal(leftData, rightData)
}

func plannedAppliedSettings(previews []response.PatchPreviewItem) map[string]any {
	out := map[string]any{}
	for _, preview := range previews {
		if isTextApplyOperation(preview.Operation) {
			continue
		}
		mergeMap(out, preview.SettingsUpdates)
	}
	return out
}

func plannedAppliedText(previews []response.PatchPreviewItem) string {
	texts := make([]string, 0, len(previews))
	for _, preview := range previews {
		if !isTextApplyOperation(preview.Operation) {
			continue
		}
		if strings.TrimSpace(preview.ContentPreview) == "" {
			continue
		}
		texts = append(texts, preview.ContentPreview)
	}
	return strings.Join(texts, "\n")
}

func isTextApplyOperation(operation string) bool {
	switch operation {
	case "append_block", "text_append":
		return true
	default:
		return false
	}
}

func rollbackAppliedSteps(files []applyFileBackup) error {
	for i := len(files) - 1; i >= 0; i-- {
		file := files[i]
		if file.OriginalExists {
			if err := os.MkdirAll(filepath.Dir(file.FilePath), 0o755); err != nil {
				return err
			}
			switch file.FileKind {
			case "text_append", "text_replace":
				if err := os.WriteFile(file.FilePath, []byte(file.OriginalText), 0o644); err != nil {
					return err
				}
			default:
				data, err := json.MarshalIndent(file.OriginalJSON, "", "  ")
				if err != nil {
					return err
				}
				data = append(data, '\n')
				if err := os.WriteFile(file.FilePath, data, 0o644); err != nil {
					return err
				}
			}
			continue
		}
		if err := os.Remove(file.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func normalizeApplyBackupFiles(backup applyBackup) []applyFileBackup {
	if len(backup.Files) > 0 {
		out := make([]applyFileBackup, 0, len(backup.Files))
		for _, file := range backup.Files {
			out = append(out, applyFileBackup{
				FilePath:       file.FilePath,
				FileKind:       file.FileKind,
				OriginalExists: file.OriginalExists,
				OriginalJSON:   cloneAnyMap(file.OriginalJSON),
				OriginalText:   file.OriginalText,
			})
		}
		return out
	}
	if strings.TrimSpace(backup.FilePath) == "" {
		return nil
	}
	return []applyFileBackup{{
		FilePath:       backup.FilePath,
		FileKind:       backup.FileKind,
		OriginalExists: backup.OriginalExists,
		OriginalJSON:   cloneAnyMap(backup.OriginalJSON),
		OriginalText:   backup.OriginalText,
	}}
}

func rollbackAppliedFile(files []applyFileBackup) string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.FilePath)
	}
	return appliedFileSummary(paths)
}

func rollbackAppliedSettings(files []applyFileBackup) map[string]any {
	combined := map[string]any{}
	for _, file := range files {
		if file.FileKind == "json_merge" {
			combined[file.FilePath] = cloneAnyMap(file.OriginalJSON)
		}
	}
	return combined
}

func rollbackAppliedText(files []applyFileBackup) string {
	texts := make([]string, 0, len(files))
	for _, file := range files {
		if file.FileKind == "text_append" || file.FileKind == "text_replace" {
			texts = append(texts, file.OriginalText)
		}
	}
	return strings.Join(texts, "\n")
}

func appliedFileSummary(paths []string) string {
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return paths[0]
	default:
		return strings.Join(paths, ",")
	}
}

func applyBackupPath(applyID string) (string, error) {
	root, err := stateFilePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), "applies", applyID+".json"), nil
}

func saveApplyBackup(backup applyBackup) error {
	path, err := applyBackupPath(backup.ApplyID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func loadApplyBackup(applyID string) (applyBackup, error) {
	path, err := applyBackupPath(applyID)
	if err != nil {
		return applyBackup{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return applyBackup{}, fmt.Errorf("backup for apply %s not found", applyID)
		}
		return applyBackup{}, err
	}
	var backup applyBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return applyBackup{}, err
	}
	backup.Files = normalizeApplyBackupFiles(backup)
	return backup, nil
}

func deleteApplyBackup(applyID string) error {
	path, err := applyBackupPath(applyID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func reviewChangePlan(client *apiClient, applyID, decision, reviewedBy, note string) (response.ChangePlanReviewResp, error) {
	var resp response.ChangePlanReviewResp
	err := client.doJSON(http.MethodPost, "/api/v1/change-plans/review", request.ReviewChangePlanReq{
		ApplyID:    applyID,
		Decision:   decision,
		ReviewedBy: reviewedBy,
		ReviewNote: note,
	}, &resp)
	return resp, err
}

func fetchApplyHistoryItem(client *apiClient, projectID, applyID string) (response.ApplyHistoryItem, error) {
	var resp response.ApplyHistoryResp
	if err := client.doJSON(http.MethodGet, "/api/v1/applies?project_id="+url.QueryEscape(projectID), nil, &resp); err != nil {
		return response.ApplyHistoryItem{}, err
	}
	for _, item := range resp.Items {
		if item.ApplyID == applyID {
			return item, nil
		}
	}
	return response.ApplyHistoryItem{}, fmt.Errorf("apply %s not found in project history", applyID)
}

func preflightLocalApply(st state, applyID string, previews []response.PatchPreviewItem, targetOverride string) (preflightResult, error) {
	if len(previews) == 0 {
		return preflightResult{}, errors.New("no patch preview available for apply")
	}

	result := preflightResult{
		ApplyID: applyID,
		Allowed: true,
		Reason:  "preflight passed",
		Steps:   make([]preflightStep, 0, len(previews)),
	}
	resolvedSeen := map[string]struct{}{}
	for index, preview := range previews {
		resolvedPath, source, err := resolveApplyTarget(preview.FilePath, stepTargetOverride(targetOverride, index))
		if err != nil {
			return preflightResult{}, err
		}
		step := preflightStep{
			TargetFile:   resolvedPath,
			Operation:    preview.Operation,
			PreviewFile:  preview.FilePath,
			TargetSource: source,
			Allowed:      true,
			Guard:        "strict",
			Reason:       "preflight passed",
		}
		switch {
		case !isAllowedOperation(preview.Operation):
			step.Allowed = false
			step.Guard = "operation"
			step.Reason = "unsupported patch operation"
		case !isAllowedTarget(preview.FilePath, resolvedPath):
			step.Allowed = false
			step.Guard = "file_scope"
			step.Reason = "target file is outside the local guard allowlist"
		default:
			if _, exists := resolvedSeen[resolvedPath]; exists {
				step.Allowed = false
				step.Guard = "duplicate_target"
				step.Reason = "multiple change plan steps target the same file"
			}
		}
		if step.Allowed {
			resolvedSeen[resolvedPath] = struct{}{}
		} else {
			result.Allowed = false
			result.Reason = "one or more change plan steps failed preflight"
		}
		result.Steps = append(result.Steps, step)
	}
	return result, nil
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func runtimePlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func resolveApplyTarget(previewPath, targetOverride string) (string, string, error) {
	target := previewPath
	source := "preview"
	if strings.TrimSpace(targetOverride) != "" {
		target = targetOverride
		source = "override"
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), source, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	return filepath.Clean(filepath.Join(cwd, target)), source, nil
}

func stepTargetOverride(raw string, index int) string {
	parts := splitComma(raw)
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		if index < len(parts) {
			return parts[index]
		}
		return ""
	}
}

func isAllowedOperation(operation string) bool {
	switch operation {
	case "", "merge_patch", "append_block", "text_append":
		return true
	default:
		return false
	}
}

func isAllowedTarget(previewPath, resolvedPath string) bool {
	allowedRelative := map[string]struct{}{
		filepath.Clean(".codex/config.json"):          {},
		filepath.Clean(".claude/settings.local.json"): {},
		filepath.Clean(".mcp.json"):                   {},
		filepath.Clean("AGENTS.md"):                   {},
		filepath.Clean("CLAUDE.md"):                   {},
	}

	if !filepath.IsAbs(previewPath) {
		if _, ok := allowedRelative[filepath.Clean(previewPath)]; !ok {
			return false
		}
	}

	base := filepath.Base(resolvedPath)
	allowedBase := map[string]struct{}{
		"config.json":         {},
		"settings.local.json": {},
		".mcp.json":           {},
		"AGENTS.md":           {},
		"CLAUDE.md":           {},
	}
	if _, ok := allowedBase[base]; !ok {
		return false
	}

	cwd, err := os.Getwd()
	if err == nil && isWithinRoot(cwd, resolvedPath) {
		return true
	}
	if root := os.Getenv("AGENTOPT_HOME"); strings.TrimSpace(root) != "" && isWithinRoot(root, resolvedPath) {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && isWithinRoot(home, resolvedPath) {
		return true
	}
	return false
}

func isWithinRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if root == target {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func inferEnabledMCPCount(settings map[string]any) int {
	if raw, ok := settings["enabled_mcp_count"].(float64); ok {
		return int(raw)
	}
	return 1
}

func inferHooksEnabled(settings map[string]any) bool {
	if raw, ok := settings["hooks_enabled"].(bool); ok {
		return raw
	}
	_, hasHook := settings["post_edit_hook"]
	return hasHook
}

func inferInstructionFiles(settings map[string]any) []string {
	files := []string{}
	if raw, ok := settings["instruction_files"].([]any); ok {
		for _, item := range raw {
			if text, ok := item.(string); ok {
				files = append(files, text)
			}
		}
	}
	if len(files) == 0 {
		files = append(files, "AGENTS.md")
	}
	return files
}
