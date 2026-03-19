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
	"strconv"
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
	ServerURL                 string               `json:"server_url"`
	APIToken                  string               `json:"api_token"`
	AccessToken               string               `json:"access_token"`
	RefreshToken              string               `json:"refresh_token"`
	TokenType                 string               `json:"token_type"`
	AccessExpiresAt           *time.Time           `json:"access_expires_at,omitempty"`
	RefreshExpiresAt          *time.Time           `json:"refresh_expires_at,omitempty"`
	OrgID                     string               `json:"org_id"`
	UserID                    string               `json:"user_id"`
	AgentID                   string               `json:"agent_id"`
	DeviceName                string               `json:"device_name"`
	Hostname                  string               `json:"hostname"`
	WorkspaceID               string               `json:"workspace_id,omitempty"`
	LastUploadedSessionCursor *sessionUploadCursor `json:"last_uploaded_session_cursor,omitempty"`
}

type stateDisk struct {
	ServerURL                 string               `json:"server_url"`
	AccessToken               string               `json:"access_token,omitempty"`
	RefreshToken              string               `json:"refresh_token,omitempty"`
	TokenType                 string               `json:"token_type,omitempty"`
	AccessExpiresAt           *time.Time           `json:"access_expires_at,omitempty"`
	RefreshExpiresAt          *time.Time           `json:"refresh_expires_at,omitempty"`
	APIToken                  string               `json:"api_token,omitempty"`
	OrgID                     string               `json:"org_id"`
	UserID                    string               `json:"user_id"`
	AgentID                   string               `json:"agent_id"`
	DeviceName                string               `json:"device_name"`
	Hostname                  string               `json:"hostname"`
	WorkspaceID               string               `json:"workspace_id,omitempty"`
	LegacyProjectID           string               `json:"project_id,omitempty"`
	LastUploadedSessionCursor *sessionUploadCursor `json:"last_uploaded_session_cursor,omitempty"`
}

const sharedWorkspaceName = "Shared workspace"
const defaultServerURL = "https://useautoskills.com"
const reportAPISchemaVersion = "report-api.v1"

var (
	errStateNotFound         = errors.New("autoskills state not found")
	errWorkspaceNotConnected = errors.New("shared workspace is not connected")
)

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

type apiClient struct {
	baseURL string
	token   string
	state   *state
	http    *http.Client
}

type apiError struct {
	StatusCode int
	Code       int
	Message    string
	Method     string
	Path       string
	Request    string
	Response   string
	RetryAfter time.Duration
}

func (e *apiError) Error() string {
	base := fmt.Sprintf("request failed: %s", e.Message)
	if !cruxDebugHTTPEnabled() {
		return base
	}
	var builder strings.Builder
	builder.WriteString(base)
	if e.Method != "" || e.Path != "" {
		builder.WriteString("\nhttp request: ")
		builder.WriteString(strings.TrimSpace(e.Method))
		if strings.TrimSpace(e.Path) != "" {
			if strings.TrimSpace(e.Method) != "" {
				builder.WriteByte(' ')
			}
			builder.WriteString(strings.TrimSpace(e.Path))
		}
	}
	if e.Request != "" {
		builder.WriteString("\nrequest body: ")
		builder.WriteString(e.Request)
	}
	if e.Response != "" {
		builder.WriteString("\nresponse body: ")
		builder.WriteString(e.Response)
	}
	return builder.String()
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
	SkillSet      *skillSetSyncResp                `json:"skill_set,omitempty"`
	Background    backgroundSetupResp              `json:"background"`
}

type resetResp struct {
	HomeDir            string              `json:"home_dir"`
	StatePath          string              `json:"state_path"`
	StateRemoved       bool                `json:"state_removed"`
	StateAlreadyAbsent bool                `json:"state_already_absent,omitempty"`
	Background         backgroundResetResp `json:"background"`
	Warnings           []string            `json:"warnings,omitempty"`
}

const defaultAPIClientTimeout = 90 * time.Second
const sessionSummaryBatchPath = "/api/v1/session-summaries/batch"
const maxSessionSummaryBatchSize = 25

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
	case "reset":
		return runReset(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "session":
		return runSession(args[1:])
	case "collect":
		return runCollect(args[1:])
	case "skills":
		return runSkills(args[1:])
	case "snapshots":
		return runSnapshots(args[1:])
	case "sessions":
		return runSessions(args[1:])
	case "reports":
		return runReports(args[1:])
	case "imports":
		return runImports(args[1:])
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
	command := "autoskills setup"
	trimmedServerURL := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if trimmedServerURL != "" && trimmedServerURL != defaultServerURL {
		command = "autoskills setup --server " + trimmedServerURL
	}
	fmt.Println(`AutoSkills is not set up yet.

Next step:
  ` + command + `

The CLI will prompt for the issued token.
Use ` + "`autoskills help`" + ` for advanced commands.`)
}

func printUsage() {
	fmt.Println(`AutoSkills quickstart:
  setup             register this device, connect the current repo, backfill local Codex history on first setup, and enable background collection when supported

Common commands:
  status            print org overview and shared workspace feedback reports
  reports           list feedback reports for the shared workspace
  imports           list recent async session import jobs, or run autoskills imports cancel <job_id>
  collect           upload local usage data now and optionally keep collecting on an interval
  skills            manage the auto-synced personal skill bundle on this machine
  sessions          list recent session summaries for the shared workspace
  snapshots         list config snapshots for the shared workspace

Advanced commands:
  version           print the CLI build version
  login             authenticate with an issued CLI token and register this device
  connect           connect a local repo to the shared workspace for the current org
  reset             remove saved cli auth/workspace state and background collector artifacts on this machine
  snapshot          upload a config snapshot from a JSON file
  session           upload one or more session summaries from a JSON file or local Codex session files
  workspace         show the shared workspace connected to the current org
  audit             list recent audit events for the current org and shared workspace
  store-export      export the runtime analytics store from the configured database
  store-import      import a runtime analytics store backup into the configured database

Install and onboard:
  curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/autoskills-cli/main/scripts/install.sh | sh
  autoskills setup`)
}

func printVersion() {
	fmt.Println(versionString())
}

func versionString() string {
	return buildinfo.Summary("autoskills")
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", defaultServerURL, "server base URL")
	token := fs.String("token", envOrFallback("AUTOSKILLS_TOKEN", "CRUX_TOKEN"), "CLI token issued from the dashboard")
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
	serverExplicit := flagProvided(args, "server")
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	server := fs.String("server", defaultServerURL, "server base URL")
	token := fs.String("token", envOrFallback("AUTOSKILLS_TOKEN", "CRUX_TOKEN"), "CLI token issued from the dashboard")
	repoPath := fs.String("repo-path", ".", "repo path to connect")
	device := fs.String("device", "", "device name")
	codexHome := fs.String("codex-home", "", "override Codex home used for initial session collection")
	recent := fs.Int("recent", 1, "number of recent local Codex session JSONL files to upload during setup")
	upload := fs.Bool("upload", true, "upload an initial snapshot and local session history after connecting; first setup backfills all local Codex sessions")
	background := fs.Bool("background", true, "enable background collection automatically when supported")
	backgroundInterval := fs.Duration("background-interval", 30*time.Minute, "fallback scan interval for background collection")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *recent < 1 {
		return errors.New("setup --recent must be at least 1")
	}
	if *backgroundInterval <= 0 {
		return errors.New("setup --background-interval must be greater than zero")
	}
	initialWorkspaceSetup := shouldBackfillFullHistoryOnSetup()

	st, loginResp, err := resolveSetupState(loginOptions{
		ServerURL:  *server,
		Token:      *token,
		DeviceName: *device,
		Tools:      "codex,claude-code",
		Consent:    "config_snapshot,session_summary,execution_result",
		CLIVersion: buildinfo.Version,
	}, serverExplicit)
	if err != nil {
		return err
	}
	if loginResp.Status == "reused" {
		fmt.Fprintf(os.Stderr, "Using saved device login for %s\n", strings.TrimRight(st.ServerURL, "/"))
	} else {
		fmt.Fprintf(os.Stderr, "Registering this device with %s\n", strings.TrimRight(st.ServerURL, "/"))
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
	var skillSetResp *skillSetSyncResp
	if *upload {
		uploadRecent := *recent
		if initialWorkspaceSetup {
			uploadRecent = 0
			fmt.Fprintln(os.Stderr, "Uploading an initial snapshot and the full local Codex session history")
		} else if *recent == 1 {
			fmt.Fprintln(os.Stderr, "Uploading an initial snapshot and the latest local Codex session")
		} else {
			fmt.Fprintf(os.Stderr, "Uploading an initial snapshot and the latest %d local Codex sessions\n", *recent)
		}
		client := newStateAPIClient(&st)
		resp, err := runCollectOnce(&st, client, "", "default", "codex", "", *codexHome, uploadRecent, collectSnapshotModeChanged, false, initialWorkspaceSetup)
		if err != nil {
			return err
		}
		collectResp = &resp
		skillSetResp = collectResp.SkillSet
	}

	if skillSetResp == nil {
		client := newStateAPIClient(&st)
		resp, err := syncLatestSkillSet(&st, client, *codexHome)
		if err != nil {
			return err
		}
		skillSetResp = &resp
	}

	// Inject the autoskills-personal-skillset section into the global AGENTS.md
	// so that every query automatically consults the user's personal skillset.
	codexRoot, err := codexHomePath(*codexHome)
	if err == nil {
		if injErr := ensureAgentsMDSkillSetSection(codexRoot); injErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update AGENTS.md with skillset section: %v\n", injErr)
		} else {
			fmt.Fprintln(os.Stderr, "Global AGENTS.md updated with autoskills-personal-skillset instructions")
		}
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
		SkillSet:      skillSetResp,
		Background:    backgroundResp,
	})
}

func resolveSetupState(opts loginOptions, serverExplicit bool) (state, response.CLILoginResp, error) {
	requestedServer := normalizedServerURL(opts.ServerURL)
	existing, err := loadState()
	if err == nil {
		savedServer := normalizedServerURL(existing.ServerURL)
		if savedServer == "" {
			savedServer = defaultServerURL
		}

		if serverExplicit && strings.TrimSpace(opts.Token) == "" && requestedServer != "" && requestedServer != savedServer {
			return state{}, response.CLILoginResp{}, fmt.Errorf(
				"saved cli state is for %s, but setup requested %s; run `autoskills login --server %s --token <CLI_TOKEN_FROM_DASHBOARD>` or `autoskills reset` first",
				savedServer,
				requestedServer,
				requestedServer,
			)
		}

		if !serverExplicit {
			requestedServer = savedServer
		}

		if strings.TrimSpace(opts.Token) == "" {
			if normalizedServerURL(existing.ServerURL) != requestedServer {
				existing.ServerURL = requestedServer
				if err := saveState(existing); err != nil {
					return state{}, response.CLILoginResp{}, err
				}
			}
			return existing, response.CLILoginResp{
				AgentID:   existing.AgentID,
				DeviceID:  existing.AgentID,
				OrgID:     existing.OrgID,
				UserID:    existing.UserID,
				Status:    "reused",
				TokenType: defaultString(strings.TrimSpace(existing.TokenType), "Bearer"),
			}, nil
		}
	}
	if err != nil && !errors.Is(err, errStateNotFound) {
		return state{}, response.CLILoginResp{}, err
	}

	if requestedServer == "" {
		requestedServer = defaultServerURL
	}
	opts.ServerURL = requestedServer
	return loginAndSaveState(opts)
}

func runReset(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	statePath, err := stateFilePath()
	if err != nil {
		return err
	}
	homeDir := filepath.Dir(statePath)

	resp := resetResp{
		HomeDir:   homeDir,
		StatePath: statePath,
	}

	if err := os.Remove(statePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			resp.StateAlreadyAbsent = true
		} else {
			return err
		}
	} else {
		resp.StateRemoved = true
	}

	background := resetBackgroundCollection(homeDir)
	resp.Background = background
	resp.Warnings = append(resp.Warnings, background.Warnings...)

	if err := removeDirIfEmpty(homeDir); err != nil {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("failed to prune empty autoskills home dir: %v", err))
	}

	return prettyPrint(resp)
}

func shouldBackfillFullHistoryOnSetup() bool {
	_, err := loadWorkspaceState()
	if err == nil {
		return false
	}
	return errors.Is(err, errStateNotFound) || errors.Is(err, errWorkspaceNotConnected)
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
	client := newStateAPIClient(&st)
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
	client := newStateAPIClient(&st)

	resp, err := uploadSessionSummariesDetailed(st, client, reqs, *tool)
	if err != nil {
		return err
	}

	if len(resp.Items) == 1 && resp.Failed == 0 {
		item, ok := sessionIngestRespFromBatchItem(resp.Items[0], resp)
		if ok {
			return prettyPrint(item)
		}
	}
	return prettyPrint(resp)
}

func uploadSessionSummariesDetailed(st state, client *apiClient, reqs []request.SessionSummaryReq, tool string) (*response.SessionBatchIngestResp, error) {
	aggregated := &response.SessionBatchIngestResp{
		SchemaVersion: reportAPISchemaVersion,
		ProjectID:     st.workspaceID(),
		Items:         make([]response.SessionBatchIngestItemResp, 0, len(reqs)),
	}
	if len(reqs) == 0 {
		return aggregated, nil
	}

	for start := 0; start < len(reqs); start += maxSessionSummaryBatchSize {
		end := start + maxSessionSummaryBatchSize
		if end > len(reqs) {
			end = len(reqs)
		}

		chunk := cloneSessionSummaryReqs(reqs[start:end])
		if len(chunk) == 1 {
			item, err := uploadSingleSessionSummaryAsBatchItem(st, client, chunk[0], tool, start+1, len(reqs))
			if err != nil {
				return nil, err
			}
			mergeSessionBatchIngestResp(aggregated, item)
			continue
		}

		batchResp, err := uploadSessionSummaryBatch(st, client, chunk, tool, start+1, len(reqs))
		if err != nil {
			if !isSessionSummaryBatchUnsupported(err) {
				return nil, err
			}
			for idx, req := range chunk {
				item, itemErr := uploadSingleSessionSummaryAsBatchItem(st, client, req, tool, start+idx+1, len(reqs))
				if itemErr != nil {
					return nil, itemErr
				}
				mergeSessionBatchIngestResp(aggregated, item)
			}
			continue
		}

		mergeSessionBatchIngestResp(aggregated, batchResp)
	}

	return aggregated, nil
}

func uploadSingleSessionSummaryAsBatchItem(st state, client *apiClient, req request.SessionSummaryReq, tool string, index, total int) (*response.SessionBatchIngestResp, error) {
	uploaded, err := uploadSessionSummary(st, client, req, tool, index, total)
	if err != nil {
		return nil, err
	}

	recordedAt := uploaded.RecordedAt
	return &response.SessionBatchIngestResp{
		SchemaVersion:   uploaded.SchemaVersion,
		ProjectID:       uploaded.ProjectID,
		Accepted:        1,
		Uploaded:        1,
		ReportCount:     uploaded.ReportCount,
		LatestReportIDs: cloneStringSlice(uploaded.LatestReportIDs),
		Items: []response.SessionBatchIngestItemResp{{
			SessionID:  uploaded.SessionID,
			ProjectID:  uploaded.ProjectID,
			Status:     "uploaded",
			RecordedAt: &recordedAt,
		}},
		ResearchStatus: cloneReportResearchStatusResp(uploaded.ResearchStatus),
	}, nil
}

func uploadSessionSummaryBatch(st state, client *apiClient, reqs []request.SessionSummaryReq, tool string, startIndex, total int) (*response.SessionBatchIngestResp, error) {
	if len(reqs) == 0 {
		return &response.SessionBatchIngestResp{
			SchemaVersion: reportAPISchemaVersion,
			ProjectID:     st.workspaceID(),
		}, nil
	}

	prepared := cloneSessionSummaryReqs(reqs)
	for idx := range prepared {
		prepared[idx] = prepareSessionSummaryReq(st, prepared[idx], tool)
	}

	endIndex := startIndex + len(prepared) - 1
	fmt.Fprintf(os.Stderr, "[%d-%d/%d] Uploading %d sessions\n", startIndex, endIndex, total, len(prepared))
	fmt.Fprintln(os.Stderr, "    The server may spend a while generating the next feedback report after this upload.")

	payload := request.SessionSummaryBatchReq{
		ProjectID: st.workspaceID(),
		Sessions:  prepared,
	}
	var uploaded response.SessionBatchIngestResp
	for attempt := 1; ; attempt++ {
		if err := client.doJSON(http.MethodPost, sessionSummaryBatchPath, payload, &uploaded); err != nil {
			delay, retry := nextSessionUploadRetryDelay(err, attempt)
			if !retry {
				return nil, err
			}
			fmt.Fprintf(os.Stderr, "    Server rate limited this upload. Retrying in %s.\n", delay.Round(time.Millisecond))
			time.Sleep(delay)
			continue
		}
		break
	}
	if uploaded.ResearchStatus != nil {
		fmt.Fprintf(os.Stderr, "    %s\n", formatResearchStatusSummary(uploaded.ResearchStatus))
	}
	return &uploaded, nil
}

func prepareSessionSummaryReq(st state, req request.SessionSummaryReq, tool string) request.SessionSummaryReq {
	req.ProjectID = st.workspaceID()
	if strings.TrimSpace(req.Tool) == "" {
		req.Tool = tool
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now().UTC()
	}
	return req
}

func cloneSessionSummaryReqs(reqs []request.SessionSummaryReq) []request.SessionSummaryReq {
	if len(reqs) == 0 {
		return nil
	}
	cloned := make([]request.SessionSummaryReq, 0, len(reqs))
	for _, req := range reqs {
		cloned = append(cloned, request.SessionSummaryReq{
			ProjectID:              strings.TrimSpace(req.ProjectID),
			SessionID:              strings.TrimSpace(req.SessionID),
			Tool:                   strings.TrimSpace(req.Tool),
			TokenIn:                req.TokenIn,
			TokenOut:               req.TokenOut,
			CachedInputTokens:      req.CachedInputTokens,
			ReasoningOutputTokens:  req.ReasoningOutputTokens,
			FunctionCallCount:      req.FunctionCallCount,
			ToolErrorCount:         req.ToolErrorCount,
			SessionDurationMS:      req.SessionDurationMS,
			ToolWallTimeMS:         req.ToolWallTimeMS,
			ToolCalls:              cloneIntMap(req.ToolCalls),
			ToolErrors:             cloneIntMap(req.ToolErrors),
			ToolWallTimesMS:        cloneIntMap(req.ToolWallTimesMS),
			RawQueries:             cloneStringSlice(req.RawQueries),
			Models:                 cloneStringSlice(req.Models),
			ModelProvider:          strings.TrimSpace(req.ModelProvider),
			FirstResponseLatencyMS: req.FirstResponseLatencyMS,
			AssistantResponses:     cloneStringSlice(req.AssistantResponses),
			ReasoningSummaries:     cloneStringSlice(req.ReasoningSummaries),
			Timestamp:              req.Timestamp,
		})
	}
	return cloned
}

func mergeSessionBatchIngestResp(dst, src *response.SessionBatchIngestResp) {
	if dst == nil || src == nil {
		return
	}
	if strings.TrimSpace(dst.SchemaVersion) == "" {
		dst.SchemaVersion = src.SchemaVersion
	}
	if strings.TrimSpace(dst.ProjectID) == "" {
		dst.ProjectID = src.ProjectID
	}
	dst.Accepted += src.Accepted
	dst.Uploaded += src.Uploaded
	dst.Updated += src.Updated
	dst.Failed += src.Failed
	dst.Items = append(dst.Items, src.Items...)
	dst.ReportCount = src.ReportCount
	dst.LatestReportIDs = cloneStringSlice(src.LatestReportIDs)
	dst.ResearchStatus = cloneReportResearchStatusResp(src.ResearchStatus)
}

func sessionIngestRespFromBatchItem(item response.SessionBatchIngestItemResp, batch *response.SessionBatchIngestResp) (response.SessionIngestResp, bool) {
	if strings.TrimSpace(item.Status) == "failed" || item.RecordedAt == nil || batch == nil {
		return response.SessionIngestResp{}, false
	}
	return response.SessionIngestResp{
		SchemaVersion:   batch.SchemaVersion,
		SessionID:       item.SessionID,
		ProjectID:       firstNonEmpty(strings.TrimSpace(item.ProjectID), batch.ProjectID),
		ReportCount:     batch.ReportCount,
		LatestReportIDs: cloneStringSlice(batch.LatestReportIDs),
		RecordedAt:      item.RecordedAt.UTC(),
		ResearchStatus:  cloneReportResearchStatusResp(batch.ResearchStatus),
	}, true
}

func isSessionSummaryBatchUnsupported(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed
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
	client := newStateAPIClient(&st)

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
	client := newStateAPIClient(&st)

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
	client := newStateAPIClient(&st)
	path := "/api/v1/reports?project_id=" + url.QueryEscape(st.workspaceID())
	var resp response.ReportListResp
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return err
	}
	return prettyPrint(workspaceScopedItems(st, resp.Items))
}

func runImports(args []string) error {
	if len(args) > 0 && strings.TrimSpace(args[0]) == "cancel" {
		return runImportsCancel(args[1:])
	}

	fs := flag.NewFlagSet("imports", flag.ContinueOnError)
	projectID := fs.String("project-id", "", "override workspace/project id")
	agentID := fs.String("agent-id", "", "filter jobs by agent id")
	status := fs.String("status", "", "filter jobs by status")
	failedOnly := fs.Bool("failed-only", false, "show only failed or partially failed jobs")
	cursor := fs.String("cursor", "", "return the next page after the given import job id")
	limit := fs.Int("limit", 10, "max number of recent import jobs")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newStateAPIClient(&st)
	selectedProjectID := strings.TrimSpace(*projectID)
	if selectedProjectID == "" {
		selectedProjectID = st.workspaceID()
	}

	query := url.Values{
		"project_id": []string{selectedProjectID},
		"limit":      []string{strconv.Itoa(*limit)},
	}
	if strings.TrimSpace(*agentID) != "" {
		query.Set("agent_id", strings.TrimSpace(*agentID))
	}
	if strings.TrimSpace(*status) != "" {
		query.Set("status", strings.TrimSpace(*status))
	}
	if *failedOnly {
		query.Set("failed_only", "true")
	}
	if strings.TrimSpace(*cursor) != "" {
		query.Set("cursor", strings.TrimSpace(*cursor))
	}

	var resp response.SessionImportJobListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/session-import-jobs?"+query.Encode(), nil, &resp); err != nil {
		return err
	}
	output := map[string]any{
		"workspace_id":                 selectedProjectID,
		"workspace_name":               sharedWorkspaceName,
		"last_uploaded_session_cursor": cloneSessionUploadCursor(st.LastUploadedSessionCursor),
		"items":                        resp.Items,
	}
	if strings.TrimSpace(resp.NextCursor) != "" {
		output["next_cursor"] = strings.TrimSpace(resp.NextCursor)
	}
	return prettyPrint(output)
}

func runImportsCancel(args []string) error {
	fs := flag.NewFlagSet("imports cancel", flag.ContinueOnError)
	jobID := fs.String("job-id", "", "import job id to cancel")
	if err := fs.Parse(args); err != nil {
		return err
	}

	selectedJobID := strings.TrimSpace(*jobID)
	if selectedJobID == "" && fs.NArg() > 0 {
		selectedJobID = strings.TrimSpace(fs.Arg(0))
	}
	if selectedJobID == "" {
		return errors.New("imports cancel requires a job id")
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newStateAPIClient(&st)

	var resp response.SessionImportJobResp
	path := "/api/v1/session-import-jobs/" + url.PathEscape(selectedJobID) + "/cancel"
	if err := client.doJSON(http.MethodPost, path, request.SessionImportJobCancelReq{}, &resp); err != nil {
		return err
	}
	return prettyPrint(map[string]any{
		"workspace_id":   st.workspaceID(),
		"workspace_name": sharedWorkspaceName,
		"job":            resp,
	})
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
	client := newStateAPIClient(&st)

	var overview response.DashboardOverviewResp
	if err := client.doJSON(http.MethodGet, "/api/v1/dashboard/overview?org_id="+url.QueryEscape(st.OrgID), nil, &overview); err != nil {
		return err
	}
	var recs response.ReportListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/reports?project_id="+url.QueryEscape(st.workspaceID()), nil, &recs); err != nil {
		return err
	}

	payload := map[string]any{
		"workspace_id":                 st.workspaceID(),
		"workspace_name":               sharedWorkspaceName,
		"last_uploaded_session_cursor": cloneSessionUploadCursor(st.LastUploadedSessionCursor),
		"overview":                     overview,
		"reports":                      recs.Items,
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
	client := newStateAPIClient(&st)

	var resp response.ProjectListResp
	if err := client.doJSON(http.MethodGet, "/api/v1/projects?org_id="+url.QueryEscape(st.OrgID), nil, &resp); err != nil {
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

	client := newStateAPIClient(&st)
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
	if err := validateCLILoginResp(resp); err != nil {
		return state{}, response.CLILoginResp{}, err
	}

	st := state{
		ServerURL:        strings.TrimRight(serverURL, "/"),
		APIToken:         strings.TrimSpace(resp.AccessToken),
		AccessToken:      strings.TrimSpace(resp.AccessToken),
		RefreshToken:     strings.TrimSpace(resp.RefreshToken),
		TokenType:        defaultString(strings.TrimSpace(resp.TokenType), "Bearer"),
		AccessExpiresAt:  cloneTime(resp.AccessExpiresAt),
		RefreshExpiresAt: cloneTime(resp.RefreshExpiresAt),
		OrgID:            resp.OrgID,
		UserID:           resp.UserID,
		AgentID:          firstNonEmpty(resp.DeviceID, resp.AgentID),
		DeviceName:       deviceName,
		Hostname:         host,
	}
	if err := saveState(st); err != nil {
		return state{}, response.CLILoginResp{}, err
	}
	return st, resp, nil
}

func connectAndSaveWorkspace(st state, opts connectOptions) (state, response.ProjectRegistrationResp, string, error) {
	client := newStateAPIClient(&st)

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

func newStateAPIClient(st *state) *apiClient {
	client := newAPIClient("", "")
	if st == nil {
		return client
	}
	client.baseURL = strings.TrimRight(st.ServerURL, "/")
	client.state = st
	return client
}

func (c *apiClient) doJSON(method, path string, body any, out any) error {
	if err := c.doJSONOnce(method, path, body, out, c.authToken()); err != nil {
		var apiErr *apiError
		if c.state == nil || !errors.As(err, &apiErr) || apiErr.Code != service.ErrCodeDeviceAccessTokenExpired || strings.TrimSpace(c.state.RefreshToken) == "" || path == "/api/v1/auth/cli/refresh" {
			return c.annotateStateAuthError(err)
		}
		if refreshErr := c.refreshAccessToken(); refreshErr != nil {
			return c.annotateStateAuthError(refreshErr)
		}
		return c.annotateStateAuthError(c.doJSONOnce(method, path, body, out, c.authToken()))
	}
	return nil
}

func (c *apiClient) doJSONOnce(method, path string, body any, out any, token string) error {
	var reader io.Reader
	requestPreview := ""
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestPreview = compactDebugJSON(payload)
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("X-AutoSkills-Token", token)
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
	responsePreview := compactDebugJSON(raw)

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		if cruxDebugHTTPEnabled() {
			return fmt.Errorf("decode envelope: %w\nhttp request: %s %s\nresponse body: %s", err, strings.TrimSpace(method), strings.TrimSpace(path), responsePreview)
		}
		return fmt.Errorf("decode envelope: %w", err)
	}
	if resp.StatusCode >= 400 || env.Code != 0 {
		if env.Message == "" {
			env.Message = string(raw)
		}
		return &apiError{
			StatusCode: resp.StatusCode,
			Code:       env.Code,
			Message:    env.Message,
			Method:     strings.TrimSpace(method),
			Path:       strings.TrimSpace(path),
			Request:    requestPreview,
			Response:   responsePreview,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func envOrFallback(primary, legacy string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	return os.Getenv(legacy)
}

func cruxDebugHTTPEnabled() bool {
	value := strings.TrimSpace(envOrFallback("AUTOSKILLS_DEBUG_HTTP", "CRUX_DEBUG_HTTP"))
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func compactDebugJSON(raw []byte) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, trimmed); err == nil {
		return truncateDebugPreview(compacted.String(), 4096)
	}
	return truncateDebugPreview(string(trimmed), 4096)
}

func truncateDebugPreview(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || limit <= 0 || len(raw) <= limit {
		return raw
	}
	if limit <= len("...(truncated)") {
		return raw[:limit]
	}
	return raw[:limit-len("...(truncated)")] + "...(truncated)"
}

func parseRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	retryAt, err := http.ParseTime(raw)
	if err != nil {
		return 0
	}
	delay := retryAt.Sub(now)
	if delay <= 0 {
		return 0
	}
	return delay
}

func (c *apiClient) authToken() string {
	if c.state != nil {
		return c.state.accessToken()
	}
	return strings.TrimSpace(c.token)
}

func (c *apiClient) refreshAccessToken() error {
	if c.state == nil {
		return errors.New("device token refresh requires cli state")
	}
	if strings.TrimSpace(c.state.RefreshToken) == "" {
		return errors.New("device token refresh requires a refresh token")
	}

	var resp response.CLIRefreshResp
	if err := c.doJSONOnce(http.MethodPost, "/api/v1/auth/cli/refresh", request.CLIRefreshReq{
		RefreshToken: c.state.RefreshToken,
	}, &resp, ""); err != nil {
		return err
	}
	if strings.TrimSpace(resp.AccessToken) == "" {
		return errors.New("device token refresh returned an empty access token")
	}

	c.state.AccessToken = strings.TrimSpace(resp.AccessToken)
	c.state.APIToken = c.state.AccessToken
	c.state.RefreshToken = strings.TrimSpace(resp.RefreshToken)
	c.state.TokenType = defaultString(strings.TrimSpace(resp.TokenType), c.state.TokenType)
	c.state.AccessExpiresAt = cloneTime(resp.AccessExpiresAt)
	c.state.RefreshExpiresAt = cloneTime(resp.RefreshExpiresAt)
	if strings.TrimSpace(resp.AgentID) != "" {
		c.state.AgentID = strings.TrimSpace(resp.AgentID)
	}
	return saveState(*c.state)
}

func validateCLILoginResp(resp response.CLILoginResp) error {
	if strings.TrimSpace(resp.AccessToken) == "" || strings.TrimSpace(resp.RefreshToken) == "" {
		return errors.New("cli login succeeded but server did not return device tokens; update the CLI/server pair and retry `autoskills login` after clearing stale state")
	}
	return nil
}

func (c *apiClient) annotateStateAuthError(err error) error {
	if err == nil || c == nil || c.state == nil {
		return err
	}

	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return err
	}

	token := strings.TrimSpace(c.state.accessToken())
	if apiErr.Code == 1001 && token != "" && strings.TrimSpace(c.state.RefreshToken) == "" {
		statePath, pathErr := stateFilePath()
		if pathErr != nil {
			statePath = "~/.autoskills/state.json"
		}
		if isLegacyEnrollmentToken(token) {
			return fmt.Errorf("%w; saved cli state still contains a legacy enrollment token (%s). Remove %s and run `autoskills login` or `autoskills setup` again", err, tokenPreview(token), statePath)
		}
		return fmt.Errorf("%w; saved cli state looks stale and has no refresh token. Remove %s and run `autoskills login` or `autoskills setup` again", err, statePath)
	}

	return err
}

func loadState() (state, error) {
	path, err := stateFilePath()
	if err != nil {
		return state{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state{}, fmt.Errorf("%w; run `autoskills setup` first", errStateNotFound)
		}
		return state{}, err
	}
	if err := ensureStateFilePermissions(path); err != nil {
		return state{}, err
	}
	var disk stateDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return state{}, err
	}
	accessToken := firstNonEmpty(strings.TrimSpace(disk.AccessToken), strings.TrimSpace(disk.APIToken))
	return state{
		ServerURL:                 disk.ServerURL,
		APIToken:                  accessToken,
		AccessToken:               accessToken,
		RefreshToken:              strings.TrimSpace(disk.RefreshToken),
		TokenType:                 strings.TrimSpace(disk.TokenType),
		AccessExpiresAt:           cloneTime(disk.AccessExpiresAt),
		RefreshExpiresAt:          cloneTime(disk.RefreshExpiresAt),
		OrgID:                     disk.OrgID,
		UserID:                    disk.UserID,
		AgentID:                   disk.AgentID,
		DeviceName:                disk.DeviceName,
		Hostname:                  disk.Hostname,
		WorkspaceID:               firstNonEmpty(disk.WorkspaceID, disk.LegacyProjectID),
		LastUploadedSessionCursor: cloneSessionUploadCursor(disk.LastUploadedSessionCursor),
	}, nil
}

func loadWorkspaceState() (state, error) {
	st, err := loadState()
	if err != nil {
		return state{}, err
	}
	if st.workspaceID() == "" {
		return state{}, fmt.Errorf("%w; run `autoskills setup` or `autoskills connect` first", errWorkspaceNotConnected)
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
		ServerURL:                 st.ServerURL,
		AccessToken:               strings.TrimSpace(st.accessToken()),
		RefreshToken:              strings.TrimSpace(st.RefreshToken),
		TokenType:                 strings.TrimSpace(st.TokenType),
		AccessExpiresAt:           cloneTime(st.AccessExpiresAt),
		RefreshExpiresAt:          cloneTime(st.RefreshExpiresAt),
		OrgID:                     st.OrgID,
		UserID:                    st.UserID,
		AgentID:                   st.AgentID,
		DeviceName:                st.DeviceName,
		Hostname:                  st.Hostname,
		WorkspaceID:               st.workspaceID(),
		LastUploadedSessionCursor: cloneSessionUploadCursor(st.LastUploadedSessionCursor),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return ensureStateFilePermissions(path)
}

func stateFilePath() (string, error) {
	if root := envOrFallback("AUTOSKILLS_HOME", "CRUX_HOME"); root != "" {
		return filepath.Join(root, "state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".autoskills", "state.json"), nil
}

func normalizedServerURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.TrimRight(trimmed, "/")
}

func flagProvided(args []string, name string) bool {
	short := "-" + strings.TrimSpace(name)
	long := "--" + strings.TrimSpace(name)
	for _, arg := range args {
		if arg == short || arg == long || strings.HasPrefix(arg, short+"=") || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
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
		"workspace_id":                 st.workspaceID(),
		"workspace_name":               sharedWorkspaceName,
		"last_uploaded_session_cursor": cloneSessionUploadCursor(st.LastUploadedSessionCursor),
		"items":                        items,
	}
}

func (s state) workspaceID() string {
	return strings.TrimSpace(s.WorkspaceID)
}

func (s state) accessToken() string {
	return firstNonEmpty(strings.TrimSpace(s.AccessToken), strings.TrimSpace(s.APIToken))
}

func (s *state) setWorkspaceID(id string) {
	s.WorkspaceID = strings.TrimSpace(id)
}

func isLegacyEnrollmentToken(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "agt_enr_") || strings.HasPrefix(token, "agt_cli_")
}

func tokenPreview(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 14 {
		return token
	}
	return token[:14]
}

func ensureStateFilePermissions(path string) error {
	return os.Chmod(path, 0o600)
}

func removeDirIfEmpty(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTEMPTY) {
		return nil
	}
	return err
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]int, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneReportResearchStatusResp(status *response.ReportResearchStatusResp) *response.ReportResearchStatusResp {
	if status == nil {
		return nil
	}
	return &response.ReportResearchStatusResp{
		SchemaVersion:    status.SchemaVersion,
		State:            status.State,
		Summary:          status.Summary,
		Provider:         status.Provider,
		Model:            status.Model,
		MinimumSessions:  status.MinimumSessions,
		SessionCount:     status.SessionCount,
		RawQueryCount:    status.RawQueryCount,
		ReportCount:      status.ReportCount,
		TriggerSessionID: status.TriggerSessionID,
		LastError:        status.LastError,
		TriggeredAt:      cloneTime(status.TriggeredAt),
		StartedAt:        cloneTime(status.StartedAt),
		CompletedAt:      cloneTime(status.CompletedAt),
		LastSuccessfulAt: cloneTime(status.LastSuccessfulAt),
		LastDurationMS:   status.LastDurationMS,
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

func loadCodexParsedSessions(codexHome, tool string) ([]codexParsedSession, error) {
	sessionFiles, err := listCodexSessionFiles(codexHome)
	if err != nil {
		return nil, err
	}
	parsedSessions := make([]codexParsedSession, 0, len(sessionFiles))
	for _, sessionFile := range sessionFiles {
		parsed, err := collectCodexParsedSession(sessionFile.path, tool)
		if err != nil {
			if isCodexSkippableSessionError(err) {
				continue
			}
			return nil, err
		}
		if parsed.classification != codexSessionClassificationPrimary {
			continue
		}
		parsed.tailPath = sessionFile.path
		parsed.tailModTime = sessionFile.modTime
		parsed.tailSize = sessionFile.size
		parsedSessions = append(parsedSessions, parsed)
	}
	parsedSessions = coalesceCodexParsedSessions(parsedSessions)
	if len(parsedSessions) == 0 {
		root, rootErr := codexHomePath(codexHome)
		if rootErr != nil {
			return nil, rootErr
		}
		return nil, fmt.Errorf("no Codex sessions found under %s; pass --file or set --codex-home", filepath.Join(root, "sessions"))
	}
	return parsedSessions, nil
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

	parsedSessions, err := loadCodexParsedSessions(codexHome, tool)
	if err != nil {
		return nil, err
	}

	collectAll := recent < 1
	reqCapacity := recent
	if collectAll || reqCapacity > len(parsedSessions) {
		reqCapacity = len(parsedSessions)
	}
	reqs := make([]request.SessionSummaryReq, 0, reqCapacity)
	for idx := len(parsedSessions) - 1; idx >= 0 && (collectAll || len(reqs) < recent); idx-- {
		reqs = append(reqs, parsedSessions[idx].req)
	}
	for left, right := 0, len(reqs)-1; left < right; left, right = left+1, right-1 {
		reqs[left], reqs[right] = reqs[right], reqs[left]
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
