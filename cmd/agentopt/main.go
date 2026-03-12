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
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
	"github.com/Royaltyprogram/aiops/pkg/buildinfo"
	"github.com/Royaltyprogram/aiops/service"
)

type state struct {
	ServerURL   string `json:"server_url"`
	APIToken    string `json:"api_token"`
	OrgID       string `json:"org_id"`
	UserID      string `json:"user_id"`
	AgentID     string `json:"agent_id"`
	DeviceName  string `json:"device_name"`
	Hostname    string `json:"hostname"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type stateDisk struct {
	ServerURL       string `json:"server_url"`
	APIToken        string `json:"api_token"`
	OrgID           string `json:"org_id"`
	UserID          string `json:"user_id"`
	AgentID         string `json:"agent_id"`
	DeviceName      string `json:"device_name"`
	Hostname        string `json:"hostname"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	LegacyProjectID string `json:"project_id,omitempty"`
}

const sharedWorkspaceName = "Shared workspace"

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
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

const defaultAPIClientTimeout = 90 * time.Second

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
	case "version", "--version", "-v":
		printVersion()
		return nil
	case "login":
		return runLogin(args[1:])
	case "connect":
		return runConnect(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "session":
		return runSession(args[1:])
	case "collect":
		return runCollect(args[1:])
	case "snapshots":
		return runSnapshots(args[1:])
	case "sessions":
		return runSessions(args[1:])
	case "recommendations":
		return runRecommendations(args[1:])
	case "status":
		return runStatus(args[1:])
	case "workspace":
		return runWorkspace(args[1:])
	case "history":
		return runHistory(args[1:])
	case "experiments":
		return runExperiments(args[1:])
	case "pending":
		return runPending(args[1:])
	case "impact":
		return runImpact(args[1:])
	case "audit":
		return runAudit(args[1:])
	case "sync":
		return runSync(args[1:])
	case "daemon":
		return runDaemon(args[1:])
	case "rollback":
		return runRollback(args[1:])
	case "apply":
		return runApply(args[1:])
	case "review":
		return runReview(args[1:])
	case "preflight":
		return runPreflight(args[1:])
	case "store-export":
		return runStoreExport(args[1:])
	case "store-import":
		return runStoreImport(args[1:])
	case "--help", "-h", "help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Println(`agentopt commands:
  version           print the CLI build version
  login             authenticate with an issued CLI token and register this device
  connect           connect a local repo to the shared workspace for the current org
  snapshot          upload a config snapshot from a JSON file
  session           upload one or more session summaries from a JSON file or local Codex session files
  collect           upload local usage data now and optionally keep collecting on an interval
  snapshots         list config snapshots for the shared workspace
  sessions          list recent session summaries for the shared workspace
  recommendations   list active recommendations for the shared workspace
  status            print org overview and shared workspace recommendations
  workspace         show the shared workspace connected to the current org
  history           list apply history for the shared workspace
  experiments       list experiment lifecycle records for the shared workspace
  pending           list pending apply jobs visible to the current user and shared workspace
  impact            list recommendation impact summaries for the shared workspace
  audit             list recent audit events for the current org and shared workspace
  sync              pull approved change plans and execute them locally
  daemon            install or inspect background collect + auto-sync automation
  rollback          restore the local config backup for a previous apply
  apply             request a change plan and optionally approve/apply it locally
  review            approve or reject a requested change plan
  preflight         validate a change plan against local guard rules
  store-export      export the runtime analytics store from the configured database
  store-import      import a runtime analytics store backup into the configured database`)
}

func printVersion() {
	fmt.Println(versionString())
}

func versionString() string {
	return buildinfo.Summary("agentopt")
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
	cliVersion := fs.String("cli-version", buildinfo.Version, "cli version")
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
	repoHash := fs.String("repo-hash", "", "stable repo hash")
	repoPath := fs.String("repo-path", ".", "repo path")
	tool := fs.String("tool", "codex", "default tool")
	languageMix := fs.String("languages", "go=1.0", "comma separated language shares")
	if err := fs.Parse(args); err != nil {
		return err
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
		hash = sanitizeID(repoRoot)
	}

	req := request.RegisterProjectReq{
		OrgID:       st.OrgID,
		AgentID:     st.AgentID,
		Name:        sharedWorkspaceName,
		RepoHash:    hash,
		RepoPath:    repoRoot,
		LanguageMix: parseLanguageMix(*languageMix),
		DefaultTool: *tool,
	}
	var resp response.ProjectRegistrationResp
	if err := client.doJSON(http.MethodPost, "/api/v1/projects/register", req, &resp); err != nil {
		return err
	}

	st.setWorkspaceID(resp.ProjectID)
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

	st, err := loadWorkspaceState()
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
		ProjectID:           st.workspaceID(),
		Tool:                *tool,
		ProfileID:           *profileID,
		Settings:            settings,
		EnabledMCPCount:     inferEnabledMCPCount(settings),
		HooksEnabled:        inferHooksEnabled(settings),
		InstructionFiles:    inferInstructionFiles(settings),
		ConfigFingerprint:   sanitizeID(fmt.Sprintf("%s-%s-%d", st.workspaceID(), *profileID, len(settings))),
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

	st, err := loadWorkspaceState()
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
		req.ProjectID = st.workspaceID()
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

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ConfigSnapshotListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/config-snapshots?project_id="+url.QueryEscape(st.workspaceID()), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(workspaceScopedItems(st, resp.Items))
}

func runSessions(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	limit := fs.Int("limit", 5, "max number of recent sessions")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.SessionSummaryListResp
	path := fmt.Sprintf("/api/v1/session-summaries?project_id=%s&limit=%d", url.QueryEscape(st.workspaceID()), *limit)
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return prettyPrint(workspaceScopedItems(st, resp.Items))
}

func runRecommendations(args []string) error {
	fs := flag.NewFlagSet("recommendations", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)
	path := "/api/v1/recommendations?project_id=" + url.QueryEscape(st.workspaceID())
	var resp response.RecommendationListResp
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return prettyPrint(workspaceScopedItems(st, resp.Items))
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var overview response.DashboardOverviewResp
	if err := client.doJSON(http.MethodGet, "/api/v1/dashboard/overview?org_id="+url.QueryEscape(st.OrgID), nil, &overview); err != nil {
		return err
	}
	var recs response.RecommendationListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/recommendations?project_id="+url.QueryEscape(st.workspaceID()), nil, &recs); err != nil {
		return err
	}

	payload := map[string]any{
		"workspace_id":    st.workspaceID(),
		"workspace_name":  sharedWorkspaceName,
		"overview":        overview,
		"recommendations": recs.Items,
	}
	return prettyPrint(payload)
}

func runWorkspace(args []string) error {
	fs := flag.NewFlagSet("workspace", flag.ContinueOnError)
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

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ApplyHistoryResp
	if err := client.doJSON(http.MethodGet, "/api/v1/applies?project_id="+url.QueryEscape(st.workspaceID()), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(workspaceScopedItems(st, resp.Items))
}

func runExperiments(args []string) error {
	fs := flag.NewFlagSet("experiments", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ExperimentListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/experiments?project_id="+url.QueryEscape(st.workspaceID()), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(workspaceScopedItems(st, resp.Items))
}

func runPending(args []string) error {
	fs := flag.NewFlagSet("pending", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.PendingApplyResp
	path := fmt.Sprintf("/api/v1/applies/pending?project_id=%s&user_id=%s", url.QueryEscape(st.workspaceID()), url.QueryEscape(st.UserID))
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return prettyPrint(workspaceScopedItems(st, resp.Items))
}

func runImpact(args []string) error {
	fs := flag.NewFlagSet("impact", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ImpactSummaryResp
	if err := client.doJSON(http.MethodGet, "/api/v1/impact?project_id="+url.QueryEscape(st.workspaceID()), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(workspaceScopedItems(st, resp.Items))
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
	reasoningEffort := fs.String("codex-reasoning-effort", os.Getenv("AGENTOPT_CODEX_REASONING_EFFORT"), "Codex reasoning effort for local apply (minimal, low, medium, high, xhigh)")
	watch := fs.Bool("watch", false, "poll for pending apply jobs until interrupted")
	interval := fs.Duration("interval", 15*time.Second, "poll interval in watch mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedReasoningEffort, err := parseCodexReasoningEffort(*reasoningEffort)
	if err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	if !*watch {
		return runSyncOnce(st, client, *targetConfig, resolvedReasoningEffort)
	}
	if *interval <= 0 {
		return errors.New("sync --interval must be greater than zero")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	if err := runSyncOnce(st, client, *targetConfig, resolvedReasoningEffort); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			fmt.Println(`{"watch":"stopped"}`)
			return nil
		case <-ticker.C:
			if err := runSyncOnce(st, client, *targetConfig, resolvedReasoningEffort); err != nil {
				return err
			}
		}
	}
}

func runSyncOnce(st state, client *apiClient, targetConfig, reasoningEffort string) error {
	path := fmt.Sprintf("/api/v1/applies/pending?project_id=%s&user_id=%s", url.QueryEscape(st.workspaceID()), url.QueryEscape(st.UserID))
	var pending response.PendingApplyResp
	if err := client.doJSON(http.MethodGet, path, nil, &pending); err != nil {
		return err
	}

	results := make([]response.ApplyResultResp, 0, len(pending.Items))
	failedApplyIDs := make([]string, 0)
	for _, item := range pending.Items {
		if item.Action == "rollback" || item.Status == "rollback_requested" {
			localResult, err := executeLocalRollback(item.ApplyID)
			if err != nil {
				result, reportErr := reportApplyResult(client, request.ApplyResultReq{
					ApplyID: item.ApplyID,
					Success: false,
					Note:    fmt.Sprintf("local rollback failed during sync: %v", err),
				})
				if reportErr != nil {
					return fmt.Errorf("rollback %s failed locally: %v; failed to report result: %w", item.ApplyID, err, reportErr)
				}
				results = append(results, result)
				failedApplyIDs = append(failedApplyIDs, item.ApplyID)
				continue
			}

			result, err := reportApplyResult(client, request.ApplyResultReq{
				ApplyID:         item.ApplyID,
				Success:         true,
				Note:            "rolled back by agentopt sync",
				AppliedFile:     localResult.FilePath,
				AppliedSettings: localResult.AppliedSettings,
				AppliedText:     localResult.AppliedText,
				RolledBack:      true,
			})
			if err != nil {
				return err
			}
			if err := deleteApplyBackup(item.ApplyID); err != nil {
				return err
			}
			results = append(results, result)
			continue
		}

		localResult, err := executeLocalApply(st, item.ApplyID, item.PatchPreview, targetConfig, reasoningEffort)
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
		"workspace_id":   st.workspaceID(),
		"workspace_name": sharedWorkspaceName,
		"pending_count":  len(pending.Items),
		"failed_count":   len(failedApplyIDs),
		"results":        results,
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
	reasoningEffort := fs.String("codex-reasoning-effort", os.Getenv("AGENTOPT_CODEX_REASONING_EFFORT"), "Codex reasoning effort for local apply (minimal, low, medium, high, xhigh)")
	yes := fs.Bool("yes", false, "apply immediately after preview")
	scope := fs.String("scope", "user", "apply scope")
	note := fs.String("note", "applied by agentopt CLI", "apply result note")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedReasoningEffort, err := parseCodexReasoningEffort(*reasoningEffort)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*recommendationID) == "" {
		return errors.New("apply requires --recommendation-id")
	}

	st, err := loadWorkspaceState()
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

	localResult, err := executeLocalApply(st, plan.ApplyID, plan.PatchPreview, *targetConfig, resolvedReasoningEffort)
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

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)
	item, err := fetchApplyHistoryItem(client, st.workspaceID(), *applyID)
	if err != nil {
		return err
	}

	result, err := preflightLocalApply(st, item.ApplyID, item.PatchPreview, *targetConfig)
	if err != nil {
		return err
	}
	return prettyPrint(result)
}

func runStoreExport(args []string) error {
	fs := flag.NewFlagSet("store-export", flag.ContinueOnError)
	output := fs.String("output", "-", "backup output path or - for stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := openRuntimeStore()
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	data, err := store.ExportStateJSON()
	if err != nil {
		return err
	}

	path := strings.TrimSpace(*output)
	if path == "" || path == "-" {
		_, err = os.Stdout.Write(data)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return prettyPrint(map[string]any{
		"status": "exported",
		"output": path,
		"bytes":  len(data),
	})
}

func runStoreImport(args []string) error {
	fs := flag.NewFlagSet("store-import", flag.ContinueOnError)
	input := fs.String("input", "", "backup input path or - for stdin")
	force := fs.Bool("yes", false, "overwrite the runtime store without prompting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*input) == "" {
		return errors.New("store-import requires --input")
	}
	if !*force {
		return errors.New("store-import requires --yes because it overwrites the runtime store")
	}

	data, err := readInputData(*input)
	if err != nil {
		return err
	}

	store, err := openRuntimeStore()
	if err != nil {
		return err
	}
	defer func() {
		_ = store.Close()
	}()

	if err := store.ImportStateJSON(data); err != nil {
		return err
	}
	return prettyPrint(map[string]any{
		"status": "imported",
		"input":  strings.TrimSpace(*input),
		"bytes":  len(data),
	})
}

func parseCodexReasoningEffort(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "", nil
	}
	switch value {
	case "minimal", "low", "medium", "high", "xhigh":
		return value, nil
	default:
		return "", fmt.Errorf("invalid Codex reasoning effort %q: want minimal, low, medium, high, or xhigh", raw)
	}
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
	localResult, err := executeLocalRollback(*applyID)
	if err != nil {
		return err
	}

	client := newAPIClient(st.ServerURL, st.APIToken)
	var result response.ApplyResultResp
	if err := client.doJSON(http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:         *applyID,
		Success:         true,
		Note:            *note,
		AppliedFile:     localResult.FilePath,
		AppliedSettings: localResult.AppliedSettings,
		AppliedText:     localResult.AppliedText,
		RolledBack:      true,
	}, &result); err != nil {
		return err
	}
	if err := deleteApplyBackup(*applyID); err != nil {
		return err
	}
	return prettyPrint(result)
}

func newAPIClient(baseURL, token string) *apiClient {
	return &apiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: defaultAPIClientTimeout,
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
	var disk stateDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return state{}, err
	}
	return state{
		ServerURL:   disk.ServerURL,
		APIToken:    disk.APIToken,
		OrgID:       disk.OrgID,
		UserID:      disk.UserID,
		AgentID:     disk.AgentID,
		DeviceName:  disk.DeviceName,
		Hostname:    disk.Hostname,
		WorkspaceID: firstNonEmpty(disk.WorkspaceID, disk.LegacyProjectID),
	}, nil
}

func loadWorkspaceState() (state, error) {
	st, err := loadState()
	if err != nil {
		return state{}, err
	}
	if st.workspaceID() == "" {
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
	payload := stateDisk{
		ServerURL:   st.ServerURL,
		APIToken:    st.APIToken,
		OrgID:       st.OrgID,
		UserID:      st.UserID,
		AgentID:     st.AgentID,
		DeviceName:  st.DeviceName,
		Hostname:    st.Hostname,
		WorkspaceID: st.workspaceID(),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
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

func workspaceScopedItems(st state, items any) map[string]any {
	return map[string]any{
		"workspace_id":   st.workspaceID(),
		"workspace_name": sharedWorkspaceName,
		"items":          items,
	}
}

func (s state) workspaceID() string {
	return strings.TrimSpace(s.WorkspaceID)
}

func (s *state) setWorkspaceID(id string) {
	s.WorkspaceID = strings.TrimSpace(id)
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

func openRuntimeStore() (*service.AnalyticsStore, error) {
	conf, err := configs.InitConfig()
	if err != nil {
		return nil, err
	}
	return service.NewAnalyticsStore(conf)
}

func readInputData(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
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

func fetchApplyHistoryItem(client *apiClient, workspaceID, applyID string) (response.ApplyHistoryItem, error) {
	var resp response.ApplyHistoryResp
	if err := client.doJSON(http.MethodGet, "/api/v1/applies?project_id="+url.QueryEscape(workspaceID), nil, &resp); err != nil {
		return response.ApplyHistoryItem{}, err
	}
	for _, item := range resp.Items {
		if item.ApplyID == applyID {
			return item, nil
		}
	}
	return response.ApplyHistoryItem{}, fmt.Errorf("apply %s not found in workspace history", applyID)
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
	if expanded, ok, err := expandUserPath(target); err != nil {
		return "", "", err
	} else if ok {
		return expanded, source, nil
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

func expandUserPath(target string) (string, bool, error) {
	if target == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false, err
		}
		return filepath.Clean(home), true, nil
	}
	prefix := "~" + string(os.PathSeparator)
	if !strings.HasPrefix(target, prefix) {
		return "", false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	return filepath.Clean(filepath.Join(home, strings.TrimPrefix(target, prefix))), true, nil
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
	case "", "merge_patch", "append_block", "text_append", "text_replace":
		return true
	default:
		return false
	}
}

func isAllowedTarget(previewPath, resolvedPath string) bool {
	allowSkillTarget := isAllowedAgentoptSkillTarget(previewPath) || isAllowedAgentoptSkillTarget(resolvedPath)
	allowedRelative := map[string]struct{}{
		filepath.Clean(".codex/config.json"):          {},
		filepath.Clean(".claude/settings.local.json"): {},
		filepath.Clean(".mcp.json"):                   {},
		filepath.Clean("AGENTS.md"):                   {},
		filepath.Clean("~/.codex/AGENTS.md"):          {},
		filepath.Clean("CLAUDE.md"):                   {},
	}

	if !filepath.IsAbs(previewPath) {
		if _, ok := allowedRelative[filepath.Clean(previewPath)]; !ok && !allowSkillTarget {
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
		"SKILL.md":            {},
	}
	if _, ok := allowedBase[base]; !ok {
		return false
	}
	if base == "SKILL.md" && !allowSkillTarget {
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

func isAllowedAgentoptSkillTarget(target string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(target)))
	if cleaned == "" || !strings.HasSuffix(cleaned, "/SKILL.md") {
		return false
	}
	switch {
	case strings.HasPrefix(cleaned, ".codex/skills/agentopt-"):
		return true
	case strings.HasPrefix(cleaned, "~/.codex/skills/agentopt-"):
		return true
	case strings.Contains(cleaned, "/.codex/skills/agentopt-"):
		return true
	default:
		return false
	}
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
