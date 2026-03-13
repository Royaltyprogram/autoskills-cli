package main

import (
	"bufio"
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
	"runtime"
	"strings"
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
const defaultServerURL = "http://127.0.0.1:8082"

var (
	errStateNotFound         = errors.New("crux state not found")
	errWorkspaceNotConnected = errors.New("shared workspace is not connected")
)

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

type loginOptions struct {
	ServerURL  string
	Token      string
	DeviceName string
	Hostname   string
	Tools      string
	Platform   string
	Consent    string
	CLIVersion string
}

type connectOptions struct {
	RepoHash    string
	RepoPath    string
	DefaultTool string
	LanguageMix string
}

type setupResp struct {
	ServerURL     string                           `json:"server_url"`
	OrgID         string                           `json:"org_id"`
	UserID        string                           `json:"user_id"`
	AgentID       string                           `json:"agent_id"`
	DeviceName    string                           `json:"device_name"`
	WorkspaceID   string                           `json:"workspace_id"`
	WorkspaceName string                           `json:"workspace_name"`
	RepoPath      string                           `json:"repo_path"`
	Login         response.CLILoginResp            `json:"login"`
	Connect       response.ProjectRegistrationResp `json:"connect"`
	Collect       *collectRunResp                  `json:"collect,omitempty"`
	Background    backgroundSetupResp              `json:"background"`
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
		return runDefaultCommand()
	}

	switch args[0] {
	case "version", "--version", "-v":
		printVersion()
		return nil
	case "setup":
		return runSetup(args[1:])
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
	case "reports":
		return runReports(args[1:])
	case "status":
		return runStatus(args[1:])
	case "workspace":
		return runWorkspace(args[1:])
	case "audit":
		return runAudit(args[1:])
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

func runDefaultCommand() error {
	_, err := loadWorkspaceState()
	if err == nil {
		return runStatus(nil)
	}
	if errors.Is(err, errStateNotFound) {
		printDefaultSetupHint("")
		return nil
	}
	if errors.Is(err, errWorkspaceNotConnected) {
		st, stateErr := loadState()
		if stateErr == nil {
			printDefaultSetupHint(st.ServerURL)
			return nil
		}
		printDefaultSetupHint("")
		return nil
	}
	return err
}

func printDefaultSetupHint(serverURL string) {
	serverHint := "<server-url>"
	if strings.TrimSpace(serverURL) != "" {
		serverHint = strings.TrimRight(strings.TrimSpace(serverURL), "/")
	}
	fmt.Println(`Crux is not set up yet.

Next step:
  crux setup --server ` + serverHint + `

The CLI will prompt for the issued token.
Use ` + "`crux help`" + ` for advanced commands.`)
}

func printUsage() {
	fmt.Println(`Crux quickstart:
  setup             register this device, connect the current repo, upload initial local data, and enable background collection when supported

Common commands:
  status            print org overview and shared workspace feedback reports
  reports           list active feedback reports for the shared workspace
  collect           upload local usage data now and optionally keep collecting on an interval
  sessions          list recent session summaries for the shared workspace
  snapshots         list config snapshots for the shared workspace

Advanced commands:
  version           print the CLI build version
  login             authenticate with an issued CLI token and register this device
  connect           connect a local repo to the shared workspace for the current org
  snapshot          upload a config snapshot from a JSON file
  session           upload one or more session summaries from a JSON file or local Codex session files
  workspace         show the shared workspace connected to the current org
  audit             list recent audit events for the current org and shared workspace
  store-export      export the runtime analytics store from the configured database
  store-import      import a runtime analytics store backup into the configured database

Install and onboard:
  curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/aiops/main/scripts/install.sh | sh
  crux setup --server ` + defaultServerURL)
}

func printVersion() {
	fmt.Println(versionString())
}

func versionString() string {
	return buildinfo.Summary("crux")
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", defaultServerURL, "server base URL")
	token := fs.String("token", os.Getenv("CRUX_TOKEN"), "CLI token issued from the dashboard")
	device := fs.String("device", "", "device name")
	hostname := fs.String("hostname", "", "hostname")
	tools := fs.String("tools", "codex,claude-code", "comma separated tool names")
	platform := fs.String("platform", "", "device platform")
	consent := fs.String("consent", "config_snapshot,session_summary,execution_result", "comma separated collection scopes")
	cliVersion := fs.String("cli-version", buildinfo.Version, "cli version")
	if err := fs.Parse(args); err != nil {
		return err
	}

	_, resp, err := loginAndSaveState(loginOptions{
		ServerURL:  *server,
		Token:      *token,
		DeviceName: *device,
		Hostname:   *hostname,
		Tools:      *tools,
		Platform:   *platform,
		Consent:    *consent,
		CLIVersion: *cliVersion,
	})
	if err != nil {
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
	_, resp, _, err := connectAndSaveWorkspace(st, connectOptions{
		RepoHash:    *repoHash,
		RepoPath:    *repoPath,
		DefaultTool: *tool,
		LanguageMix: *languageMix,
	})
	if err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	server := fs.String("server", defaultServerURL, "server base URL")
	token := fs.String("token", os.Getenv("CRUX_TOKEN"), "CLI token issued from the dashboard")
	repoPath := fs.String("repo-path", ".", "repo path to connect")
	device := fs.String("device", "", "device name")
	codexHome := fs.String("codex-home", "", "override Codex home used for initial session collection")
	recent := fs.Int("recent", 1, "number of recent local Codex session JSONL files to upload during setup")
	upload := fs.Bool("upload", true, "upload an initial snapshot and recent local session after connecting")
	background := fs.Bool("background", true, "enable background collection automatically when supported")
	backgroundInterval := fs.Duration("background-interval", 30*time.Minute, "poll interval for background collection")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *recent < 1 {
		return errors.New("setup --recent must be at least 1")
	}
	if *backgroundInterval <= 0 {
		return errors.New("setup --background-interval must be greater than zero")
	}

	fmt.Fprintf(os.Stderr, "Registering this device with %s\n", strings.TrimRight(*server, "/"))
	st, loginResp, err := loginAndSaveState(loginOptions{
		ServerURL:  *server,
		Token:      *token,
		DeviceName: *device,
		Tools:      "codex,claude-code",
		Consent:    "config_snapshot,session_summary,execution_result",
		CLIVersion: buildinfo.Version,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Connecting %s to the shared workspace\n", firstNonEmpty(strings.TrimSpace(*repoPath), "."))
	st, connectResp, repoRoot, err := connectAndSaveWorkspace(st, connectOptions{
		RepoPath:    *repoPath,
		DefaultTool: "codex",
		LanguageMix: "go=1.0",
	})
	if err != nil {
		return err
	}

	var collectResp *collectRunResp
	if *upload {
		fmt.Fprintln(os.Stderr, "Uploading an initial snapshot and the latest local Codex session")
		client := newAPIClient(st.ServerURL, st.APIToken)
		resp, err := runCollectOnce(st, client, "", "default", "codex", "", *codexHome, *recent, collectSnapshotModeChanged)
		if err != nil {
			return err
		}
		collectResp = &resp
	}

	fmt.Fprintln(os.Stderr, "Configuring background collection")
	backgroundResp := ensureBackgroundCollection(backgroundSetupOptions{
		Enabled:   *background,
		CodexHome: *codexHome,
		Recent:    *recent,
		Interval:  *backgroundInterval,
	})
	switch backgroundResp.Status {
	case "enabled":
		fmt.Fprintf(os.Stderr, "Background collection enabled every %s\n", backgroundResp.Interval)
	case "disabled":
		fmt.Fprintf(os.Stderr, "Background collection disabled. Manual command: %s\n", backgroundResp.Command)
	default:
		if strings.TrimSpace(backgroundResp.Reason) != "" {
			fmt.Fprintf(os.Stderr, "Background collection not enabled: %s\n", backgroundResp.Reason)
		}
		if strings.TrimSpace(backgroundResp.Command) != "" {
			fmt.Fprintf(os.Stderr, "Manual fallback: %s\n", backgroundResp.Command)
		}
	}

	return prettyPrint(setupResp{
		ServerURL:     st.ServerURL,
		OrgID:         st.OrgID,
		UserID:        st.UserID,
		AgentID:       st.AgentID,
		DeviceName:    st.DeviceName,
		WorkspaceID:   st.workspaceID(),
		WorkspaceName: sharedWorkspaceName,
		RepoPath:      repoRoot,
		Login:         loginResp,
		Connect:       connectResp,
		Collect:       collectResp,
		Background:    backgroundResp,
	})
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

func runReports(args []string) error {
	fs := flag.NewFlagSet("reports", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)
	path := "/api/v1/reports?project_id=" + url.QueryEscape(st.workspaceID())
	var resp response.ReportListResp
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
	var recs response.ReportListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/reports?project_id="+url.QueryEscape(st.workspaceID()), nil, &recs); err != nil {
		return err
	}

	payload := map[string]any{
		"workspace_id":   st.workspaceID(),
		"workspace_name": sharedWorkspaceName,
		"overview":       overview,
		"reports":        recs.Items,
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

func loginAndSaveState(opts loginOptions) (state, response.CLILoginResp, error) {
	host := strings.TrimSpace(opts.Hostname)
	if host == "" {
		var err error
		host, err = os.Hostname()
		if err != nil {
			host = "unknown-host"
		}
	}

	deviceName := strings.TrimSpace(opts.DeviceName)
	if deviceName == "" {
		deviceName = host
	}

	cliToken := strings.TrimSpace(opts.Token)
	if cliToken == "" {
		prompted, err := promptInput("CLI token")
		if err != nil {
			return state{}, response.CLILoginResp{}, err
		}
		cliToken = prompted
	}
	if cliToken == "" {
		return state{}, response.CLILoginResp{}, errors.New("login requires a CLI token issued from the dashboard")
	}

	serverURL := strings.TrimSpace(opts.ServerURL)
	if serverURL == "" {
		serverURL = defaultServerURL
	}

	client := newAPIClient(serverURL, cliToken)
	req := request.CLILoginReq{
		DeviceName:    deviceName,
		Hostname:      host,
		Platform:      defaultString(opts.Platform, runtimePlatform()),
		CLIVersion:    opts.CLIVersion,
		Tools:         splitComma(opts.Tools),
		ConsentScopes: splitComma(opts.Consent),
	}

	var resp response.CLILoginResp
	if err := client.doJSON(http.MethodPost, "/api/v1/auth/cli/login", req, &resp); err != nil {
		return state{}, response.CLILoginResp{}, err
	}

	st := state{
		ServerURL:  strings.TrimRight(serverURL, "/"),
		APIToken:   cliToken,
		OrgID:      resp.OrgID,
		UserID:     resp.UserID,
		AgentID:    firstNonEmpty(resp.DeviceID, resp.AgentID),
		DeviceName: deviceName,
		Hostname:   host,
	}
	if err := saveState(st); err != nil {
		return state{}, response.CLILoginResp{}, err
	}
	return st, resp, nil
}

func connectAndSaveWorkspace(st state, opts connectOptions) (state, response.ProjectRegistrationResp, string, error) {
	client := newAPIClient(st.ServerURL, st.APIToken)

	repoRoot, err := normalizeRepoPath(opts.RepoPath)
	if err != nil {
		return state{}, response.ProjectRegistrationResp{}, "", err
	}

	hash := strings.TrimSpace(opts.RepoHash)
	if hash == "" {
		hash = sanitizeID(repoRoot)
	}

	req := request.RegisterProjectReq{
		OrgID:       st.OrgID,
		AgentID:     st.AgentID,
		Name:        sharedWorkspaceName,
		RepoHash:    hash,
		RepoPath:    repoRoot,
		LanguageMix: parseLanguageMix(opts.LanguageMix),
		DefaultTool: opts.DefaultTool,
	}

	var resp response.ProjectRegistrationResp
	if err := client.doJSON(http.MethodPost, "/api/v1/projects/register", req, &resp); err != nil {
		return state{}, response.ProjectRegistrationResp{}, "", err
	}

	st.setWorkspaceID(resp.ProjectID)
	if err := saveState(st); err != nil {
		return state{}, response.ProjectRegistrationResp{}, "", err
	}
	return st, resp, repoRoot, nil
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
		req.Header.Set("X-Crux-Token", c.token)
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
			return state{}, fmt.Errorf("%w; run `crux setup --server <url>` first", errStateNotFound)
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
		return state{}, fmt.Errorf("%w; run `crux setup --server <url>` or `crux connect` first", errWorkspaceNotConnected)
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
	if root := os.Getenv("CRUX_HOME"); root != "" {
		return filepath.Join(root, "state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".crux", "state.json"), nil
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
