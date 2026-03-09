package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/dto/response"
)

type state struct {
	ServerURL   string `json:"server_url"`
	APIToken    string `json:"api_token"`
	OrgID       string `json:"org_id"`
	UserID      string `json:"user_id"`
	AgentID     string `json:"agent_id"`
	DeviceName  string `json:"device_name"`
	Hostname    string `json:"hostname"`
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
}

type applyBackup struct {
	ApplyID        string         `json:"apply_id"`
	ProjectID      string         `json:"project_id"`
	FilePath       string         `json:"file_path"`
	OriginalExists bool           `json:"original_exists"`
	OriginalJSON   map[string]any `json:"original_json"`
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

type localApplyResult struct {
	FilePath        string
	AppliedSettings map[string]any
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
	case "--help", "-h", "help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Println(`agentopt commands:
  login             register a CLI agent and persist local state
  connect           connect a project to the current org/agent
  snapshot          upload a config snapshot from a JSON file
  session           upload a session summary from a JSON file or defaults
  snapshots         list config snapshots for the current project
  sessions          list recent session summaries for the current project
  recommendations   list active recommendations for the current project
  status            print org overview and project recommendations
  projects          list projects under the current org
  history           list apply history for the current project
  impact            list recommendation impact summaries for the current project
  audit             list recent audit events for the current org or project
  sync              pull pending apply jobs and execute them locally
  rollback          restore the local config backup for a previous apply
  apply             request a patch preview and optionally write local config`)
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", "http://127.0.0.1:8082", "server base URL")
	token := fs.String("token", os.Getenv("AGENTOPT_TOKEN"), "api token")
	orgID := fs.String("org", "demo-org", "organization id")
	orgName := fs.String("org-name", "", "organization display name")
	userID := fs.String("user", "demo-user", "user id")
	email := fs.String("email", "demo@example.com", "user email")
	device := fs.String("device", "", "device name")
	hostname := fs.String("hostname", "", "hostname")
	tools := fs.String("tools", "codex,claude-code", "comma separated tool names")
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
	if *orgName == "" {
		*orgName = *orgID
	}

	client := newAPIClient(*server, *token)
	req := request.RegisterAgentReq{
		OrgID:      *orgID,
		OrgName:    *orgName,
		UserID:     *userID,
		UserEmail:  *email,
		DeviceName: deviceName,
		Hostname:   host,
		CLIVersion: *cliVersion,
		Tools:      splitComma(*tools),
	}
	var resp response.AgentRegistrationResp
	if err := client.doJSON(http.MethodPost, "/api/v1/agents/register", req, &resp); err != nil {
		return err
	}

	st := state{
		ServerURL:  strings.TrimRight(*server, "/"),
		APIToken:   *token,
		OrgID:      resp.OrgID,
		UserID:     resp.UserID,
		AgentID:    resp.AgentID,
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
	projectID := fs.String("project-id", "", "project id override")
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

	hash := strings.TrimSpace(*repoHash)
	if hash == "" {
		hash = sanitizeID(*projectName + "-" + *repoPath)
	}

	req := request.RegisterProjectReq{
		OrgID:       st.OrgID,
		AgentID:     st.AgentID,
		ProjectID:   *projectID,
		Name:        *projectName,
		RepoHash:    hash,
		RepoPath:    *repoPath,
		LanguageMix: parseLanguageMix(*languageMix),
		DefaultTool: *tool,
	}
	var resp response.ProjectRegistrationResp
	if err := client.doJSON(http.MethodPost, "/api/v1/projects/register", req, &resp); err != nil {
		return err
	}

	st.ProjectID = resp.ProjectID
	st.ProjectName = *projectName
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
		"approval_policy":   "interactive",
		"instructions_pack": "baseline",
	}
	if *filePath != "" {
		if err := loadJSONFile(*filePath, &settings); err != nil {
			return err
		}
	}

	req := request.ConfigSnapshotReq{
		ProjectID:  st.ProjectID,
		Tool:       *tool,
		ProfileID:  *profileID,
		Settings:   settings,
		CapturedAt: time.Now().UTC(),
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
	filePath := fs.String("file", "", "session summary JSON file path")
	tool := fs.String("tool", "codex", "tool name")
	task := fs.String("task", "bugfix", "task type")
	repoSize := fs.String("repo-size", "large", "repo size bucket")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}

	req := request.SessionSummaryReq{
		Tool:                  *tool,
		TaskType:              *task,
		ProjectHash:           sanitizeID(st.ProjectName),
		LanguageMix:           map[string]float64{"go": 0.9, "yaml": 0.1},
		TotalPromptsCount:     14,
		TotalToolCalls:        32,
		BashCallsCount:        9,
		ReadOps:               24,
		EditOps:               11,
		WriteOps:              4,
		MCPUsageCount:         1,
		PermissionRejectCount: 3,
		RetryCount:            2,
		TokenIn:               18000,
		TokenOut:              6400,
		EstimatedCost:         0.82,
		RepoSizeBucket:        *repoSize,
		ConfigProfileID:       "baseline",
		Timestamp:             time.Now().UTC(),
	}
	if *filePath != "" {
		if err := loadJSONFile(*filePath, &req); err != nil {
			return err
		}
	}
	req.ProjectID = st.ProjectID
	if req.Tool == "" {
		req.Tool = *tool
	}
	if req.TaskType == "" {
		req.TaskType = *task
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now().UTC()
	}

	client := newAPIClient(st.ServerURL, st.APIToken)
	var resp response.SessionIngestResp
	if err := client.doJSON(http.MethodPost, "/api/v1/session-summaries", req, &resp); err != nil {
		return err
	}
	return prettyPrint(resp)
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
	return prettyPrint(resp)
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
	return prettyPrint(resp)
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
	return prettyPrint(resp)
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
	projectID := fs.String("project-id", "", "project id override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	targetProjectID := st.ProjectID
	if strings.TrimSpace(*projectID) != "" {
		targetProjectID = *projectID
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ApplyHistoryResp
	if err := client.doJSON(http.MethodGet, "/api/v1/applies?project_id="+url.QueryEscape(targetProjectID), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runImpact(args []string) error {
	fs := flag.NewFlagSet("impact", flag.ContinueOnError)
	projectID := fs.String("project-id", "", "project id override")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	targetProjectID := st.ProjectID
	if strings.TrimSpace(*projectID) != "" {
		targetProjectID = *projectID
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	var resp response.ImpactSummaryResp
	if err := client.doJSON(http.MethodGet, "/api/v1/impact?project_id="+url.QueryEscape(targetProjectID), nil, &resp); err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	projectID := fs.String("project-id", "", "optional project id filter")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadState()
	if err != nil {
		return err
	}
	path := "/api/v1/audits?org_id=" + url.QueryEscape(st.OrgID)
	if strings.TrimSpace(*projectID) != "" {
		path += "&project_id=" + url.QueryEscape(*projectID)
	}

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
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadProjectState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	path := fmt.Sprintf("/api/v1/applies/pending?project_id=%s&requested_by=%s", url.QueryEscape(st.ProjectID), url.QueryEscape(st.UserID))
	var pending response.PendingApplyResp
	if err := client.doJSON(http.MethodGet, path, nil, &pending); err != nil {
		return err
	}

	results := make([]response.ApplyResultResp, 0, len(pending.Items))
	for _, item := range pending.Items {
		localResult, err := executeLocalApply(st, item.ApplyID, item.PatchPreview, *targetConfig)
		if err != nil {
			return err
		}

		var result response.ApplyResultResp
		if err := client.doJSON(http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
			ApplyID:         item.ApplyID,
			Success:         true,
			Note:            "applied by agentopt sync",
			AppliedFile:     localResult.FilePath,
			AppliedSettings: localResult.AppliedSettings,
		}, &result); err != nil {
			return err
		}
		results = append(results, result)
	}

	return prettyPrint(map[string]any{
		"pending_count": len(pending.Items),
		"results":       results,
	})
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

	localResult, err := executeLocalApply(st, plan.ApplyID, plan.PatchPreview, *targetConfig)
	if err != nil {
		return err
	}

	var result response.ApplyResultResp
	if err := client.doJSON(http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:         plan.ApplyID,
		Success:         true,
		Note:            *note,
		AppliedFile:     localResult.FilePath,
		AppliedSettings: localResult.AppliedSettings,
	}, &result); err != nil {
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

	if backup.OriginalExists {
		data, err := json.MarshalIndent(backup.OriginalJSON, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err := os.MkdirAll(filepath.Dir(backup.FilePath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(backup.FilePath, data, 0o644); err != nil {
			return err
		}
	} else {
		if err := os.Remove(backup.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	client := newAPIClient(st.ServerURL, st.APIToken)
	var result response.ApplyResultResp
	if err := client.doJSON(http.MethodPost, "/api/v1/applies/result", request.ApplyResultReq{
		ApplyID:         backup.ApplyID,
		Success:         true,
		Note:            *note,
		AppliedFile:     backup.FilePath,
		AppliedSettings: backup.OriginalJSON,
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
		return state{}, errors.New("project is not connected; run `agentopt connect` first")
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

func prettyPrint(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
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

func mergeMap(dst, src map[string]any) {
	for key, value := range src {
		dst[key] = value
	}
}

func executeLocalApply(st state, applyID string, previews []response.PatchPreviewItem, targetOverride string) (localApplyResult, error) {
	if len(previews) == 0 {
		return localApplyResult{}, errors.New("no patch preview available for apply")
	}
	preview := previews[0]

	filePath := targetOverride
	if filePath == "" {
		filePath = preview.FilePath
	}
	if !filepath.IsAbs(filePath) {
		cwd, err := os.Getwd()
		if err != nil {
			return localApplyResult{}, err
		}
		filePath = filepath.Join(cwd, filePath)
	}

	config := map[string]any{}
	originalExists, err := readOptionalJSONMap(filePath, &config)
	if err != nil {
		return localApplyResult{}, err
	}
	backup := applyBackup{
		ApplyID:        applyID,
		ProjectID:      st.ProjectID,
		FilePath:       filePath,
		OriginalExists: originalExists,
		OriginalJSON:   cloneAnyMap(config),
	}
	mergeMap(config, preview.SettingsUpdates)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return localApplyResult{}, err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return localApplyResult{}, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return localApplyResult{}, err
	}
	if err := saveApplyBackup(backup); err != nil {
		return localApplyResult{}, err
	}

	return localApplyResult{
		FilePath:        filePath,
		AppliedSettings: cloneAnyMap(preview.SettingsUpdates),
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
