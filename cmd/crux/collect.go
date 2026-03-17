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

var (
	sessionUploadRetryBaseDelay    = time.Second
	sessionUploadRetryMaxDelay     = 8 * time.Second
	sessionUploadRetryMaxAttempt   = 6
	sessionImportJobAsyncThreshold = 100
	sessionImportJobPollInterval   = time.Second
)

type collectRunResp struct {
	CollectedAt        time.Time                      `json:"collected_at"`
	SnapshotStatus     string                         `json:"snapshot_status"`
	Snapshot           *response.ConfigSnapshotResp   `json:"snapshot,omitempty"`
	SessionCursorReset bool                           `json:"session_cursor_reset,omitempty"`
	SessionStatus      string                         `json:"session_status"`
	SessionUploaded    int                            `json:"session_uploaded"`
	SessionFailures    []collectSessionFailure        `json:"session_failures,omitempty"`
	Sessions           []response.SessionIngestResp   `json:"sessions,omitempty"`
	ImportJob          *response.SessionImportJobResp `json:"import_job,omitempty"`
	SkillSet           *skillSetSyncResp              `json:"skill_set,omitempty"`
}

type collectSessionFailure struct {
	SessionID    string `json:"session_id"`
	Error        string `json:"error"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	APIErrorCode int    `json:"api_error_code,omitempty"`
	HTTPMethod   string `json:"http_method,omitempty"`
	HTTPPath     string `json:"http_path,omitempty"`
	RequestBody  string `json:"request_body,omitempty"`
	ResponseBody string `json:"response_body,omitempty"`
}

func runCollect(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	snapshotFile := fs.String("snapshot-file", "", "snapshot JSON file path")
	profileID := fs.String("profile-id", "default", "profile identifier for snapshot uploads")
	tool := fs.String("tool", "codex", "tool name")
	sessionFile := fs.String("session-file", "", "session summary JSON or Codex session JSONL path")
	codexHome := fs.String("codex-home", "", "override Codex home used for automatic session collection")
	recent := fs.Int("recent", 1, "number of recent local Codex session JSONL files to upload when --session-file is omitted")
	resetSessions := fs.Bool("reset-sessions", false, "clear the saved session upload cursor and re-upload all local Codex sessions")
	detach := fs.Bool("detach", false, "return after queuing an async import job instead of waiting for completion")
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
	if *resetSessions && strings.TrimSpace(*sessionFile) != "" {
		return errors.New("collect --reset-sessions can only be used when --session-file is omitted")
	}
	if *detach && *watch {
		return errors.New("collect --detach cannot be used with --watch")
	}
	resolvedSnapshotMode, err := parseCollectSnapshotMode(*snapshotMode)
	if err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	client := newStateAPIClient(&st)
	sessionCursorReset := false
	effectiveRecent := *recent
	if *resetSessions {
		if err := resetSessionUploadCursor(&st); err != nil {
			return err
		}
		sessionCursorReset = true
		effectiveRecent = 0
	}

	if !*watch {
		resp, err := runCollectOnce(&st, client, *snapshotFile, *profileID, *tool, *sessionFile, *codexHome, effectiveRecent, resolvedSnapshotMode, *detach)
		if err != nil {
			return err
		}
		resp.SessionCursorReset = sessionCursorReset
		return prettyPrint(resp)
	}
	if *interval <= 0 {
		return errors.New("collect --interval must be greater than zero")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	resp, err := runCollectOnce(&st, client, *snapshotFile, *profileID, *tool, *sessionFile, *codexHome, effectiveRecent, resolvedSnapshotMode, false)
	if err != nil {
		return err
	}
	resp.SessionCursorReset = sessionCursorReset
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
			resp, err := runCollectOnce(&st, client, *snapshotFile, *profileID, *tool, *sessionFile, *codexHome, *recent, resolvedSnapshotMode, false)
			if err != nil {
				return err
			}
			if err := prettyPrint(resp); err != nil {
				return err
			}
		case <-ticker.C:
			resp, err := runCollectOnce(&st, client, *snapshotFile, *profileID, *tool, *sessionFile, *codexHome, *recent, resolvedSnapshotMode, false)
			if err != nil {
				return err
			}
			if err := prettyPrint(resp); err != nil {
				return err
			}
		}
	}
}

func resetSessionUploadCursor(st *state) error {
	if st == nil || st.LastUploadedSessionCursor == nil {
		return nil
	}
	st.LastUploadedSessionCursor = nil
	return saveState(*st)
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

func runCollectOnce(st *state, client *apiClient, snapshotFile, profileID, tool, sessionFile, codexHome string, recent int, snapshotMode string, detach bool) (collectRunResp, error) {
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

	sessionStatus, uploadedCount, sessions, failures, importJob, err := collectSessionsOnce(st, client, sessionFile, tool, codexHome, recent, detach)
	if err != nil {
		return collectRunResp{}, err
	}
	resp.SessionStatus = sessionStatus
	resp.SessionUploaded = uploadedCount
	resp.SessionFailures = failures
	resp.Sessions = sessions
	resp.ImportJob = importJob

	skillSetResp, syncErr := syncLatestSkillSet(st, client, codexHome)
	if syncErr != nil {
		return collectRunResp{}, syncErr
	}
	resp.SkillSet = &skillSetResp
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

func collectSessionsOnce(st *state, client *apiClient, filePath, tool, codexHome string, recent int, detach bool) (string, int, []response.SessionIngestResp, []collectSessionFailure, *response.SessionImportJobResp, error) {
	if strings.TrimSpace(filePath) != "" {
		reqs, err := loadSessionSummaryInputs(filePath, tool, codexHome, recent)
		if err != nil {
			return "", 0, nil, nil, nil, err
		}
		if len(reqs) == 0 {
			return "no_local_sessions", 0, nil, nil, nil, nil
		}

		batchResp, err := uploadSessionSummariesDetailed(*st, client, reqs, tool)
		if err != nil {
			return "", 0, nil, nil, nil, err
		}

		items, failures, err := collectSessionResultsFromBatchResp(batchResp)
		if err != nil {
			return "", 0, nil, nil, nil, err
		}
		if len(failures) == 0 {
			return "uploaded", len(items), items, nil, nil, nil
		}
		if len(items) == 0 {
			return "skipped_invalid_sessions", 0, nil, failures, nil, nil
		}
		return "uploaded_with_failures", len(items), items, failures, nil, nil
	}

	parsedSessions, err := loadCodexParsedSessions(codexHome, tool)
	if err != nil {
		if isNoLocalSessionError(err) {
			return "no_local_sessions", 0, nil, nil, nil, nil
		}
		return "", 0, nil, nil, nil, err
	}

	pendingSessions := selectCodexParsedSessionsAfterCursor(parsedSessions, st.LastUploadedSessionCursor)
	if st.LastUploadedSessionCursor == nil && recent > 0 && len(pendingSessions) > recent {
		pendingSessions = pendingSessions[len(pendingSessions)-recent:]
	}
	if len(pendingSessions) == 0 {
		return "up_to_date", 0, nil, nil, nil, nil
	}

	if shouldUseSessionImportJob(len(pendingSessions)) {
		sessionStatus, uploadedCount, failures, importJob, asyncErr := uploadCodexParsedSessionImportJob(st, client, pendingSessions, tool, detach)
		if asyncErr == nil {
			if len(failures) == 0 {
				return sessionStatus, uploadedCount, nil, nil, importJob, nil
			}
			if uploadedCount == 0 {
				return sessionStatus, 0, nil, failures, importJob, nil
			}
			return sessionStatus, uploadedCount, nil, failures, importJob, nil
		}
		if !isSessionImportJobUnsupported(asyncErr) {
			return "", 0, nil, nil, nil, asyncErr
		}
	}

	items, failures, err := uploadCodexParsedSessionSummaries(st, client, pendingSessions, tool)
	if err != nil {
		return "", 0, nil, nil, nil, err
	}
	if len(failures) == 0 {
		return "uploaded", len(items), items, nil, nil, nil
	}
	if len(items) == 0 {
		return "skipped_invalid_sessions", 0, nil, failures, nil, nil
	}
	return "uploaded_with_failures", len(items), items, failures, nil, nil
}

func shouldUseSessionImportJob(sessionCount int) bool {
	return sessionCount >= sessionImportJobAsyncThreshold
}

func uploadCodexParsedSessionImportJob(st *state, client *apiClient, sessions []codexParsedSession, tool string, detach bool) (string, int, []collectSessionFailure, *response.SessionImportJobResp, error) {
	job, err := createSessionImportJob(*st, client, len(sessions))
	if err != nil {
		return "", 0, nil, nil, err
	}
	if job.Reused {
		fmt.Fprintf(os.Stderr, "Resuming backfill job %s (%d/%d sessions staged)\n", job.JobID, job.ReceivedSessions, maxInt(job.TotalSessions, len(sessions)))
	} else {
		fmt.Fprintf(os.Stderr, "Started backfill job %s (%d sessions)\n", job.JobID, len(sessions))
	}

	if strings.TrimSpace(job.Status) == "receiving_chunks" {
		for start := 0; start < len(sessions); start += maxSessionSummaryBatchSize {
			end := start + maxSessionSummaryBatchSize
			if end > len(sessions) {
				end = len(sessions)
			}
			reqs := make([]request.SessionSummaryReq, 0, end-start)
			for _, session := range sessions[start:end] {
				reqs = append(reqs, prepareSessionSummaryReq(*st, session.req, tool))
			}
			fmt.Fprintf(os.Stderr, "[%d-%d/%d] Uploading import chunk for job %s\n", start+1, end, len(sessions), job.JobID)
			updatedJob, chunkErr := appendSessionImportJobChunk(client, job.JobID, reqs)
			if chunkErr != nil {
				return "", 0, nil, nil, chunkErr
			}
			job = updatedJob
		}

		job, err = completeSessionImportJob(client, job.JobID)
		if err != nil {
			return "", 0, nil, nil, err
		}
	}

	if detach {
		fmt.Fprintf(os.Stderr, "Detached from backfill job %s. Re-run `crux collect --reset-sessions` to resume polling.\n", job.JobID)
		return "import_job_queued", 0, nil, job, nil
	}

	job, err = waitForSessionImportJobCompletion(client, job)
	if err != nil {
		return "", 0, nil, nil, err
	}

	if job.Status == "failed" || job.Status == "canceled" {
		return "", 0, nil, job, fmt.Errorf("session import job %s %s: %s", job.JobID, strings.TrimSpace(job.Status), firstNonEmpty(strings.TrimSpace(job.LastError), strings.TrimSpace(job.Status)))
	}

	if len(sessions) > 0 {
		if err := advanceSessionUploadCursor(st, sessions[len(sessions)-1]); err != nil {
			return "", 0, nil, job, err
		}
	}

	failures := make([]collectSessionFailure, 0, len(job.Failures))
	for _, item := range job.Failures {
		failures = append(failures, collectSessionFailure{
			SessionID:    strings.TrimSpace(item.SessionID),
			Error:        firstNonEmpty(strings.TrimSpace(item.Error), "request failed"),
			HTTPStatus:   item.HTTPStatus,
			APIErrorCode: item.APIErrorCode,
		})
	}
	sessionStatus := "uploaded"
	if len(failures) > 0 {
		if job.UploadedSessions+job.UpdatedSessions == 0 {
			sessionStatus = "skipped_invalid_sessions"
		} else {
			sessionStatus = "uploaded_with_failures"
		}
	}
	return sessionStatus, job.UploadedSessions + job.UpdatedSessions, failures, job, nil
}

func waitForSessionImportJobCompletion(client *apiClient, job *response.SessionImportJobResp) (*response.SessionImportJobResp, error) {
	if job == nil {
		return nil, errors.New("session import job is missing")
	}
	lastProcessed := -1
	lastFailureCount := 0
	for {
		nextJob, err := getSessionImportJob(client, job.JobID)
		if err != nil {
			return nil, err
		}
		job = nextJob
		if job.ProcessedSessions != lastProcessed && (job.Status == "running" || job.Status == "queued" || job.Status == "receiving_chunks") {
			lastProcessed = job.ProcessedSessions
			fmt.Fprintf(os.Stderr, "    Server processing import job: %d/%d sessions%s\n", job.ProcessedSessions, maxInt(job.TotalSessions, job.ReceivedSessions), importJobETASuffix(job))
		}
		if len(job.Failures) > lastFailureCount {
			lastFailureCount = len(job.Failures)
			fmt.Fprintf(os.Stderr, "    Latest import failure: %s\n", summarizeImportJobFailure(job.Failures[lastFailureCount-1]))
		}
		if isTerminalSessionImportJobStatus(job.Status) {
			return job, nil
		}
		time.Sleep(sessionImportJobPollInterval)
	}
}

func importJobETASuffix(job *response.SessionImportJobResp) string {
	if job == nil || job.StartedAt == nil {
		return ""
	}
	target := maxInt(job.TotalSessions, job.ReceivedSessions)
	if target <= 0 || job.ProcessedSessions <= 0 || job.ProcessedSessions >= target {
		return ""
	}
	elapsed := time.Since(job.StartedAt.UTC())
	if elapsed < time.Second {
		return ""
	}
	ratePerSecond := float64(job.ProcessedSessions) / elapsed.Seconds()
	if ratePerSecond <= 0 {
		return ""
	}
	remaining := target - job.ProcessedSessions
	if remaining <= 0 {
		return ""
	}
	eta := time.Duration(float64(remaining) / ratePerSecond * float64(time.Second))
	if eta <= 0 {
		return ""
	}
	return fmt.Sprintf(" (ETA %s)", formatImportJobETA(eta))
}

func formatImportJobETA(eta time.Duration) string {
	if eta <= 0 {
		return "<1s"
	}
	return eta.Round(time.Second).String()
}

func summarizeImportJobFailure(item response.SessionImportJobFailureResp) string {
	parts := make([]string, 0, 4)
	if sessionID := strings.TrimSpace(item.SessionID); sessionID != "" {
		parts = append(parts, sessionID)
	}
	if message := strings.TrimSpace(item.Error); message != "" {
		parts = append(parts, message)
	}
	if item.HTTPStatus > 0 {
		parts = append(parts, fmt.Sprintf("http %d", item.HTTPStatus))
	}
	if item.APIErrorCode > 0 {
		parts = append(parts, fmt.Sprintf("api %d", item.APIErrorCode))
	}
	return firstNonEmpty(strings.Join(parts, " - "), "request failed")
}

func createSessionImportJob(st state, client *apiClient, totalSessions int) (*response.SessionImportJobResp, error) {
	var resp response.SessionImportJobResp
	if err := client.doJSON(http.MethodPost, "/api/v1/session-import-jobs", request.SessionImportJobCreateReq{
		ProjectID:     st.workspaceID(),
		TotalSessions: totalSessions,
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func appendSessionImportJobChunk(client *apiClient, jobID string, reqs []request.SessionSummaryReq) (*response.SessionImportJobResp, error) {
	var resp response.SessionImportJobResp
	path := "/api/v1/session-import-jobs/" + url.PathEscape(strings.TrimSpace(jobID)) + "/chunks"
	if err := client.doJSON(http.MethodPost, path, request.SessionImportJobChunkReq{Sessions: reqs}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func completeSessionImportJob(client *apiClient, jobID string) (*response.SessionImportJobResp, error) {
	var resp response.SessionImportJobResp
	path := "/api/v1/session-import-jobs/" + url.PathEscape(strings.TrimSpace(jobID)) + "/complete"
	if err := client.doJSON(http.MethodPost, path, request.SessionImportJobCompleteReq{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func getSessionImportJob(client *apiClient, jobID string) (*response.SessionImportJobResp, error) {
	var resp response.SessionImportJobResp
	path := "/api/v1/session-import-jobs/" + url.PathEscape(strings.TrimSpace(jobID))
	if err := client.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func isSessionImportJobUnsupported(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed
}

func isTerminalSessionImportJobStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "succeeded", "partially_failed", "failed", "canceled":
		return true
	default:
		return false
	}
}

func uploadCodexParsedSessionSummaries(st *state, client *apiClient, sessions []codexParsedSession, tool string) ([]response.SessionIngestResp, []collectSessionFailure, error) {
	items := make([]response.SessionIngestResp, 0, len(sessions))
	failures := make([]collectSessionFailure, 0)
	for start := 0; start < len(sessions); start += maxSessionSummaryBatchSize {
		end := start + maxSessionSummaryBatchSize
		if end > len(sessions) {
			end = len(sessions)
		}

		chunk := sessions[start:end]
		if len(chunk) == 1 {
			uploaded, err := uploadSessionSummary(*st, client, chunk[0].req, tool, start+1, len(sessions))
			if err != nil {
				if !isSkippableSessionUploadError(err) {
					return nil, nil, err
				}
				failure := newCollectSessionFailure(strings.TrimSpace(chunk[0].req.SessionID), err)
				failures = append(failures, failure)
				fmt.Fprintf(os.Stderr, "    Skipping session %s: %s\n", firstNonEmpty(failure.SessionID, "pending-id"), failure.Error)
				if err := advanceSessionUploadCursor(st, chunk[0]); err != nil {
					return nil, nil, err
				}
				continue
			}
			items = append(items, uploaded)
			if err := advanceSessionUploadCursor(st, chunk[0]); err != nil {
				return nil, nil, err
			}
			continue
		}

		reqs := make([]request.SessionSummaryReq, 0, len(chunk))
		for _, session := range chunk {
			reqs = append(reqs, session.req)
		}

		batchResp, err := uploadSessionSummaryBatch(*st, client, reqs, tool, start+1, len(sessions))
		if err != nil {
			if !isSessionSummaryBatchUnsupported(err) {
				return nil, nil, err
			}
			for idx, session := range chunk {
				uploaded, itemErr := uploadSessionSummary(*st, client, session.req, tool, start+idx+1, len(sessions))
				if itemErr != nil {
					if !isSkippableSessionUploadError(itemErr) {
						return nil, nil, itemErr
					}
					failure := newCollectSessionFailure(strings.TrimSpace(session.req.SessionID), itemErr)
					failures = append(failures, failure)
					fmt.Fprintf(os.Stderr, "    Skipping session %s: %s\n", firstNonEmpty(failure.SessionID, "pending-id"), failure.Error)
					if err := advanceSessionUploadCursor(st, session); err != nil {
						return nil, nil, err
					}
					continue
				}
				items = append(items, uploaded)
				if err := advanceSessionUploadCursor(st, session); err != nil {
					return nil, nil, err
				}
			}
			continue
		}

		if len(batchResp.Items) != len(chunk) {
			return nil, nil, fmt.Errorf("session batch upload returned %d items for %d requests", len(batchResp.Items), len(chunk))
		}

		for idx, item := range batchResp.Items {
			session := chunk[idx]
			if strings.EqualFold(strings.TrimSpace(item.Status), "failed") {
				if !isSkippableBatchSessionUploadFailure(item) {
					return nil, nil, batchSessionUploadFailure(item)
				}
				failure := newCollectSessionFailureFromBatchItem(item)
				failures = append(failures, failure)
				fmt.Fprintf(os.Stderr, "    Skipping session %s: %s\n", firstNonEmpty(failure.SessionID, "pending-id"), failure.Error)
				if err := advanceSessionUploadCursor(st, session); err != nil {
					return nil, nil, err
				}
				continue
			}

			uploaded, ok := sessionIngestRespFromBatchItem(item, batchResp)
			if !ok {
				return nil, nil, fmt.Errorf("session batch upload returned malformed success item for session %s", firstNonEmpty(item.SessionID, session.req.SessionID))
			}
			items = append(items, uploaded)
			if err := advanceSessionUploadCursor(st, session); err != nil {
				return nil, nil, err
			}
		}
	}
	return items, failures, nil
}

func advanceSessionUploadCursor(st *state, session codexParsedSession) error {
	cursor := session.uploadCursor()
	if cursor == nil {
		return nil
	}
	st.LastUploadedSessionCursor = cursor
	return saveState(*st)
}

func isSkippableSessionUploadError(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusBadRequest
}

func newCollectSessionFailure(sessionID string, err error) collectSessionFailure {
	failure := collectSessionFailure{
		SessionID: strings.TrimSpace(sessionID),
		Error:     err.Error(),
	}

	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return failure
	}

	failure.HTTPStatus = apiErr.StatusCode
	failure.APIErrorCode = apiErr.Code
	if !cruxDebugHTTPEnabled() {
		return failure
	}

	failure.HTTPMethod = strings.TrimSpace(apiErr.Method)
	failure.HTTPPath = strings.TrimSpace(apiErr.Path)
	failure.RequestBody = strings.TrimSpace(apiErr.Request)
	failure.ResponseBody = strings.TrimSpace(apiErr.Response)
	return failure
}

func collectSessionResultsFromBatchResp(batchResp *response.SessionBatchIngestResp) ([]response.SessionIngestResp, []collectSessionFailure, error) {
	if batchResp == nil {
		return nil, nil, nil
	}

	items := make([]response.SessionIngestResp, 0, len(batchResp.Items))
	failures := make([]collectSessionFailure, 0)
	for _, item := range batchResp.Items {
		if strings.EqualFold(strings.TrimSpace(item.Status), "failed") {
			if !isSkippableBatchSessionUploadFailure(item) {
				return nil, nil, batchSessionUploadFailure(item)
			}
			failures = append(failures, newCollectSessionFailureFromBatchItem(item))
			continue
		}

		uploaded, ok := sessionIngestRespFromBatchItem(item, batchResp)
		if !ok {
			return nil, nil, fmt.Errorf("session batch upload returned malformed success item for session %s", firstNonEmpty(item.SessionID, "pending-id"))
		}
		items = append(items, uploaded)
	}
	return items, failures, nil
}

func isSkippableBatchSessionUploadFailure(item response.SessionBatchIngestItemResp) bool {
	return item.HTTPStatus == http.StatusBadRequest
}

func batchSessionUploadFailure(item response.SessionBatchIngestItemResp) error {
	return &apiError{
		StatusCode: item.HTTPStatus,
		Code:       item.APIErrorCode,
		Message:    firstNonEmpty(strings.TrimSpace(item.Error), "batch session upload failed"),
		Method:     http.MethodPost,
		Path:       sessionSummaryBatchPath,
	}
}

func newCollectSessionFailureFromBatchItem(item response.SessionBatchIngestItemResp) collectSessionFailure {
	failure := collectSessionFailure{
		SessionID:    strings.TrimSpace(item.SessionID),
		Error:        batchSessionUploadFailure(item).Error(),
		HTTPStatus:   item.HTTPStatus,
		APIErrorCode: item.APIErrorCode,
	}
	if !cruxDebugHTTPEnabled() {
		return failure
	}
	failure.HTTPMethod = http.MethodPost
	failure.HTTPPath = sessionSummaryBatchPath
	return failure
}

func uploadSessionSummary(st state, client *apiClient, req request.SessionSummaryReq, tool string, index, total int) (response.SessionIngestResp, error) {
	req = prepareSessionSummaryReq(st, req, tool)
	fmt.Fprintf(os.Stderr, "[%d/%d] Uploading session %s\n", index, total, firstNonEmpty(strings.TrimSpace(req.SessionID), "pending-id"))
	fmt.Fprintln(os.Stderr, "    The server may spend a while generating the next feedback report after this upload.")

	var uploaded response.SessionIngestResp
	for attempt := 1; ; attempt++ {
		if err := client.doJSON(http.MethodPost, "/api/v1/session-summaries", req, &uploaded); err != nil {
			delay, retry := nextSessionUploadRetryDelay(err, attempt)
			if !retry {
				return response.SessionIngestResp{}, err
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
	return uploaded, nil
}

func nextSessionUploadRetryDelay(err error, attempt int) (time.Duration, bool) {
	if attempt >= sessionUploadRetryMaxAttempt {
		return 0, false
	}
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return 0, false
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	if apiErr.RetryAfter > 0 {
		return apiErr.RetryAfter, true
	}
	delay := sessionUploadRetryBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= sessionUploadRetryMaxDelay {
			return sessionUploadRetryMaxDelay, true
		}
	}
	if delay > sessionUploadRetryMaxDelay {
		delay = sessionUploadRetryMaxDelay
	}
	return delay, true
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
