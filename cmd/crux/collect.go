package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

const (
	collectSnapshotModeChanged = "changed"
	collectSnapshotModeAlways  = "always"
	collectSnapshotModeSkip    = "skip"
)

type collectRunResp struct {
	CollectedAt     time.Time                    `json:"collected_at"`
	SnapshotStatus  string                       `json:"snapshot_status"`
	Snapshot        *response.ConfigSnapshotResp `json:"snapshot,omitempty"`
	SessionStatus   string                       `json:"session_status"`
	SessionUploaded int                          `json:"session_uploaded"`
	Sessions        []response.SessionIngestResp `json:"sessions,omitempty"`
}

func runCollect(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	snapshotFile := fs.String("snapshot-file", "", "snapshot JSON file path")
	profileID := fs.String("profile-id", "default", "profile identifier for snapshot uploads")
	tool := fs.String("tool", "codex", "tool name")
	sessionFile := fs.String("session-file", "", "session summary JSON or Codex session JSONL path")
	codexHome := fs.String("codex-home", "", "override Codex home used for automatic session collection")
	recent := fs.Int("recent", 1, "number of recent local Codex session JSONL files to upload when --session-file is omitted")
	snapshotMode := fs.String("snapshot-mode", collectSnapshotModeChanged, "snapshot upload mode: changed, always, skip")
	watch := fs.Bool("watch", false, "watch local session files and upload changes until interrupted")
	interval := fs.Duration("interval", 30*time.Minute, "fallback poll interval in watch mode")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *recent < 1 {
		return errors.New("collect --recent must be at least 1")
	}
	if strings.TrimSpace(*sessionFile) != "" && *recent != 1 {
		return errors.New("collect --recent can only be used when --session-file is omitted")
	}
	resolvedSnapshotMode, err := parseCollectSnapshotMode(*snapshotMode)
	if err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newAPIClient(st.ServerURL, st.APIToken)

	if !*watch {
		resp, err := runCollectOnce(&st, client, *snapshotFile, *profileID, *tool, *sessionFile, *codexHome, *recent, resolvedSnapshotMode)
		if err != nil {
			return err
		}
		return prettyPrint(resp)
	}
	if *interval <= 0 {
		return errors.New("collect --interval must be greater than zero")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	resp, err := runCollectOnce(&st, client, *snapshotFile, *profileID, *tool, *sessionFile, *codexHome, *recent, resolvedSnapshotMode)
	if err != nil {
		return err
	}
	if err := prettyPrint(resp); err != nil {
		return err
	}

	sessionChanges, sessionWatchErrors, closeWatcher, err := watchCollectSessionChanges(ctx, *sessionFile, *codexHome)
	if err != nil {
		return err
	}
	defer closeWatcher()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println(`{"watch":"stopped"}`)
			return nil
		case err, ok := <-sessionWatchErrors:
			if !ok {
				sessionWatchErrors = nil
				continue
			}
			if err != nil {
				return err
			}
		case _, ok := <-sessionChanges:
			if !ok {
				sessionChanges = nil
				continue
			}
			resp, err := runCollectOnce(&st, client, *snapshotFile, *profileID, *tool, *sessionFile, *codexHome, *recent, resolvedSnapshotMode)
			if err != nil {
				return err
			}
			if err := prettyPrint(resp); err != nil {
				return err
			}
		case <-ticker.C:
			resp, err := runCollectOnce(&st, client, *snapshotFile, *profileID, *tool, *sessionFile, *codexHome, *recent, resolvedSnapshotMode)
			if err != nil {
				return err
			}
			if err := prettyPrint(resp); err != nil {
				return err
			}
		}
	}
}

func parseCollectSnapshotMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", collectSnapshotModeChanged:
		return collectSnapshotModeChanged, nil
	case collectSnapshotModeAlways:
		return collectSnapshotModeAlways, nil
	case collectSnapshotModeSkip:
		return collectSnapshotModeSkip, nil
	default:
		return "", fmt.Errorf("invalid snapshot mode %q; expected changed, always, or skip", raw)
	}
}

func runCollectOnce(st *state, client *apiClient, snapshotFile, profileID, tool, sessionFile, codexHome string, recent int, snapshotMode string) (collectRunResp, error) {
	resp := collectRunResp{
		CollectedAt:     time.Now().UTC(),
		SnapshotStatus:  "skipped",
		SessionStatus:   "skipped",
		SessionUploaded: 0,
	}

	snapshotStatus, snapshot, err := collectSnapshotOnce(*st, client, snapshotFile, profileID, tool, snapshotMode)
	if err != nil {
		return collectRunResp{}, err
	}
	resp.SnapshotStatus = snapshotStatus
	resp.Snapshot = snapshot

	sessionStatus, sessions, err := collectSessionsOnce(st, client, sessionFile, tool, codexHome, recent)
	if err != nil {
		return collectRunResp{}, err
	}
	resp.SessionStatus = sessionStatus
	resp.SessionUploaded = len(sessions)
	resp.Sessions = sessions
	return resp, nil
}

func collectSnapshotOnce(st state, client *apiClient, filePath, profileID, tool, snapshotMode string) (string, *response.ConfigSnapshotResp, error) {
	if snapshotMode == collectSnapshotModeSkip {
		return "skipped", nil, nil
	}

	req, err := buildConfigSnapshotReq(st, filePath, tool, profileID)
	if err != nil {
		return "", nil, err
	}
	if snapshotMode == collectSnapshotModeChanged {
		latest, err := fetchLatestConfigSnapshot(client, st.workspaceID())
		if err != nil {
			return "", nil, err
		}
		if latest != nil && latest.ConfigFingerprint == req.ConfigFingerprint {
			return "unchanged", nil, nil
		}
	}

	var uploaded response.ConfigSnapshotResp
	if err := client.doJSON(http.MethodPost, "/api/v1/config-snapshots", req, &uploaded); err != nil {
		return "", nil, err
	}
	return "uploaded", &uploaded, nil
}

func buildConfigSnapshotReq(st state, filePath, tool, profileID string) (request.ConfigSnapshotReq, error) {
	settings := map[string]any{
		"shell_profile":     "safe",
		"instruction_files": []string{"AGENTS.md"},
		"hooks_enabled":     true,
		"local_guard":       "strict",
	}
	if filePath != "" {
		if err := loadJSONFile(filePath, &settings); err != nil {
			return request.ConfigSnapshotReq{}, err
		}
	}

	return request.ConfigSnapshotReq{
		ProjectID:           st.workspaceID(),
		Tool:                tool,
		ProfileID:           profileID,
		Settings:            settings,
		EnabledMCPCount:     inferEnabledMCPCount(settings),
		HooksEnabled:        inferHooksEnabled(settings),
		InstructionFiles:    inferInstructionFiles(settings),
		ConfigFingerprint:   sanitizeID(fmt.Sprintf("%s-%s-%d", st.workspaceID(), profileID, len(settings))),
		RecentConfigChanges: []string{"snapshot_collected_by_cli"},
		CapturedAt:          time.Now().UTC(),
	}, nil
}

func fetchLatestConfigSnapshot(client *apiClient, workspaceID string) (*response.ConfigSnapshotItem, error) {
	var snapshots response.ConfigSnapshotListResp
	path := "/api/v1/config-snapshots?project_id=" + url.QueryEscape(workspaceID)
	if err := client.doJSON(http.MethodGet, path, nil, &snapshots); err != nil {
		return nil, err
	}
	if len(snapshots.Items) == 0 {
		return nil, nil
	}
	return &snapshots.Items[0], nil
}

func collectSessionsOnce(st *state, client *apiClient, filePath, tool, codexHome string, recent int) (string, []response.SessionIngestResp, error) {
	if strings.TrimSpace(filePath) != "" {
		reqs, err := loadSessionSummaryInputs(filePath, tool, codexHome, recent)
		if err != nil {
			return "", nil, err
		}
		if len(reqs) == 0 {
			return "no_local_sessions", nil, nil
		}

		items, err := uploadSessionSummaries(*st, client, reqs, tool)
		if err != nil {
			return "", nil, err
		}
		return "uploaded", items, nil
	}

	parsedSessions, err := loadCodexParsedSessions(codexHome, tool)
	if err != nil {
		if isNoLocalSessionError(err) {
			return "no_local_sessions", nil, nil
		}
		return "", nil, err
	}

	pendingSessions := selectCodexParsedSessionsAfterCursor(parsedSessions, st.LastUploadedSessionCursor)
	if st.LastUploadedSessionCursor == nil && recent > 0 && len(pendingSessions) > recent {
		pendingSessions = pendingSessions[len(pendingSessions)-recent:]
	}
	if len(pendingSessions) == 0 {
		return "up_to_date", nil, nil
	}

	items, err := uploadCodexParsedSessionSummaries(st, client, pendingSessions, tool)
	if err != nil {
		return "", nil, err
	}
	return "uploaded", items, nil
}

func uploadSessionSummaries(st state, client *apiClient, reqs []request.SessionSummaryReq, tool string) ([]response.SessionIngestResp, error) {
	items := make([]response.SessionIngestResp, 0, len(reqs))
	for idx, req := range reqs {
		uploaded, err := uploadSessionSummary(st, client, req, tool, idx+1, len(reqs))
		if err != nil {
			return nil, err
		}
		items = append(items, uploaded)
	}
	return items, nil
}

func uploadCodexParsedSessionSummaries(st *state, client *apiClient, sessions []codexParsedSession, tool string) ([]response.SessionIngestResp, error) {
	items := make([]response.SessionIngestResp, 0, len(sessions))
	for idx, session := range sessions {
		uploaded, err := uploadSessionSummary(*st, client, session.req, tool, idx+1, len(sessions))
		if err != nil {
			return nil, err
		}
		items = append(items, uploaded)

		cursor := session.uploadCursor()
		if cursor == nil {
			continue
		}
		st.LastUploadedSessionCursor = cursor
		if err := saveState(*st); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func uploadSessionSummary(st state, client *apiClient, req request.SessionSummaryReq, tool string, index, total int) (response.SessionIngestResp, error) {
	req.ProjectID = st.workspaceID()
	if req.Tool == "" {
		req.Tool = tool
	}
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now().UTC()
	}
	fmt.Fprintf(os.Stderr, "[%d/%d] Uploading session %s\n", index, total, firstNonEmpty(strings.TrimSpace(req.SessionID), "pending-id"))
	fmt.Fprintln(os.Stderr, "    The server may spend a while generating the next feedback report after this upload.")

	var uploaded response.SessionIngestResp
	if err := client.doJSON(http.MethodPost, "/api/v1/session-summaries", req, &uploaded); err != nil {
		return response.SessionIngestResp{}, err
	}
	if uploaded.ResearchStatus != nil {
		fmt.Fprintf(os.Stderr, "    %s\n", formatResearchStatusSummary(uploaded.ResearchStatus))
	}
	return uploaded, nil
}

func formatResearchStatusSummary(status *response.ReportResearchStatusResp) string {
	if status == nil {
		return "Upload recorded."
	}
	reportCount := status.ReportCount
	summary := strings.TrimSpace(status.Summary)
	if summary != "" {
		return summary
	}
	switch strings.TrimSpace(status.State) {
	case "waiting_for_min_sessions":
		return fmt.Sprintf("Collected %d of %d sessions needed before generating the first feedback report.", status.SessionCount, status.MinimumSessions)
	case "disabled":
		return "Feedback report research is disabled on the server."
	case "running":
		return "Upload recorded. The dashboard will show the next feedback report after the server analyzes the new sessions."
	case "succeeded":
		return fmt.Sprintf("Published %d feedback report(s).", reportCount)
	case "no_reports":
		return "Finished analyzing sessions but did not publish a new feedback report."
	case "failed":
		return firstNonEmpty(strings.TrimSpace(status.LastError), "Feedback report research failed.")
	default:
		return "Upload recorded."
	}
}

func isNoLocalSessionError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no Codex sessions found under ")
}
