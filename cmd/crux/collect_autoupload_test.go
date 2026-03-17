package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

func TestRunCollectUploadsSnapshotAndSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	sessionPath := filepath.Join(codexHome, "sessions", "2026", "03", "10", "latest.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sessionPath), 0o755))
	require.NoError(t, os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-collect","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:00Z","type":"turn_context","payload":{"model":"gpt-5.4"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nSummarize the upload pipeline."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":900,"cached_input_tokens":240,"output_tokens":180,"reasoning_output_tokens":60,"total_tokens":1080}}}}`,
		`{"timestamp":"2026-03-10T08:00:02.200Z","type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"Checking the upload order before summarizing the pipeline."}]}}`,
		`{"timestamp":"2026-03-10T08:00:02.500Z","type":"response_item","payload":{"type":"function_call","call_id":"call-1","name":"shell","arguments":"{\"cmd\":\"echo hi\"}"}}`,
		`{"timestamp":"2026-03-10T08:00:02.700Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-1","output":"Exit code: 1\nWall time: 0.1 seconds\nOutput:\npermission denied"}}`,
		`{"timestamp":"2026-03-10T08:00:03Z","type":"event_msg","payload":{"type":"agent_message","message":"The collector uploads snapshots first and session summaries second."}}`,
	}, "\n")+"\n"), 0o644))

	var snapshotReq request.ConfigSnapshotReq
	var sessionReq request.SessionSummaryReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config-snapshots":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotListResp{}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/config-snapshots":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&snapshotReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotResp{
					SnapshotID:        "snapshot-1",
					ProjectID:         "project-1",
					ProfileID:         snapshotReq.ProfileID,
					ConfigFingerprint: snapshotReq.ConfigFingerprint,
					CapturedAt:        snapshotReq.CapturedAt,
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionIngestResp{
					SessionID:   sessionReq.SessionID,
					ProjectID:   sessionReq.ProjectID,
					ReportCount: 1,
					RecordedAt:  sessionReq.Timestamp,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "uploaded", payload.SnapshotStatus)
	require.Equal(t, "uploaded", payload.SessionStatus)
	require.Equal(t, 1, payload.SessionUploaded)
	require.NotNil(t, payload.Snapshot)

	require.Equal(t, "project-1", snapshotReq.ProjectID)
	require.Equal(t, "default", snapshotReq.ProfileID)
	require.Equal(t, "project-1", sessionReq.ProjectID)
	require.Equal(t, "codex-session-collect", sessionReq.SessionID)
	require.Equal(t, 900, sessionReq.TokenIn)
	require.Equal(t, 180, sessionReq.TokenOut)
	require.Equal(t, 240, sessionReq.CachedInputTokens)
	require.Equal(t, 60, sessionReq.ReasoningOutputTokens)
	require.Equal(t, 1, sessionReq.FunctionCallCount)
	require.Equal(t, 1, sessionReq.ToolErrorCount)
	require.Equal(t, 3000, sessionReq.SessionDurationMS)
	require.Equal(t, 100, sessionReq.ToolWallTimeMS)
	require.Equal(t, map[string]int{"shell": 1}, sessionReq.ToolCalls)
	require.Equal(t, map[string]int{"shell": 1}, sessionReq.ToolErrors)
	require.Equal(t, map[string]int{"shell": 100}, sessionReq.ToolWallTimesMS)
	require.Equal(t, []string{"gpt-5.4"}, sessionReq.Models)
	require.Equal(t, "openai", sessionReq.ModelProvider)
	require.Equal(t, 2000, sessionReq.FirstResponseLatencyMS)
	require.Equal(t, []string{"The collector uploads snapshots first and session summaries second."}, sessionReq.AssistantResponses)
	require.Equal(t, []string{"Checking the upload order before summarizing the pipeline."}, sessionReq.ReasoningSummaries)
}

func TestRunCollectSkipsUnchangedSnapshotAndHandlesMissingSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	require.NoError(t, saveState(state{
		ServerURL:   "http://example.com",
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))
	st, err := loadWorkspaceState()
	require.NoError(t, err)
	snapshotReq, err := buildConfigSnapshotReq(st, "", "codex", "default")
	require.NoError(t, err)

	postCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config-snapshots":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotListResp{
					Items: []response.ConfigSnapshotItem{{
						ID:                "snapshot-existing",
						ProjectID:         "project-1",
						ProfileID:         "default",
						ConfigFingerprint: snapshotReq.ConfigFingerprint,
						CapturedAt:        time.Now().UTC(),
					}},
				}),
			}))
		case r.Method == http.MethodPost:
			postCount++
			t.Fatalf("unexpected POST request: %s", r.URL.Path)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", filepath.Join(root, "missing-codex")}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "unchanged", payload.SnapshotStatus)
	require.Equal(t, "no_local_sessions", payload.SessionStatus)
	require.Zero(t, payload.SessionUploaded)
	require.Equal(t, 0, postCount)
}

func TestRunCollectUploadsAllNewSessionsAfterCursor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nInspect session cursor handling."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"I inspected the session cursor handling."}}`,
	})

	sessionReqs := make([]request.SessionSummaryReq, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == sessionSummaryBatchPath:
			var batchReq request.SessionSummaryBatchReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&batchReq))
			sessionReqs = append(sessionReqs, batchReq.Sessions...)
			items := make([]response.SessionBatchIngestItemResp, 0, len(batchReq.Sessions))
			for _, item := range batchReq.Sessions {
				items = append(items, uploadedSessionBatchItem(item))
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, sessionBatchResp("project-1", items...)),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			var sessionReq request.SessionSummaryReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			sessionReqs = append(sessionReqs, sessionReq)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionIngestResp{
					SessionID:  sessionReq.SessionID,
					ProjectID:  sessionReq.ProjectID,
					RecordedAt: sessionReq.Timestamp,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	firstOutput := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--recent", "1", "--snapshot-mode", "skip"}))
	})

	var firstPayload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(firstOutput), &firstPayload))
	require.Equal(t, "uploaded", firstPayload.SessionStatus)
	require.Equal(t, 1, firstPayload.SessionUploaded)

	st, err := loadState()
	require.NoError(t, err)
	require.NotNil(t, st.LastUploadedSessionCursor)
	require.Equal(t, "session-1", st.LastUploadedSessionCursor.SessionID)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpload every new logical session after the cursor."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"I will upload every new logical session after the cursor."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-3.jsonl"), baseTime.Add(4*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:04:00Z","type":"session_meta","payload":{"id":"session-3","timestamp":"2026-03-10T08:04:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:04:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nVerify the collector no longer drops older new sessions."}}`,
		`{"timestamp":"2026-03-10T08:04:02Z","type":"event_msg","payload":{"type":"agent_message","message":"I verified the collector no longer drops older new sessions."}}`,
	})

	secondOutput := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--recent", "1", "--snapshot-mode", "skip"}))
	})

	var secondPayload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(secondOutput), &secondPayload))
	require.Equal(t, "uploaded", secondPayload.SessionStatus)
	require.Equal(t, 2, secondPayload.SessionUploaded)
	require.Len(t, sessionReqs, 3)
	require.Equal(t, []string{"session-1", "session-2", "session-3"}, []string{
		sessionReqs[0].SessionID,
		sessionReqs[1].SessionID,
		sessionReqs[2].SessionID,
	})

	st, err = loadState()
	require.NoError(t, err)
	require.NotNil(t, st.LastUploadedSessionCursor)
	require.Equal(t, "session-3", st.LastUploadedSessionCursor.SessionID)
}

func TestRunCollectResetSessionsReuploadsFullLocalHistory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpload session one."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Uploaded session one."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpload session two."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Uploaded session two."}}`,
	})

	sessionReqs := make([]request.SessionSummaryReq, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == sessionSummaryBatchPath:
			var batchReq request.SessionSummaryBatchReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&batchReq))
			sessionReqs = append(sessionReqs, batchReq.Sessions...)
			items := make([]response.SessionBatchIngestItemResp, 0, len(batchReq.Sessions))
			for _, item := range batchReq.Sessions {
				items = append(items, uploadedSessionBatchItem(item))
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, sessionBatchResp("project-1", items...)),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			var sessionReq request.SessionSummaryReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			sessionReqs = append(sessionReqs, sessionReq)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionIngestResp{
					SessionID:  sessionReq.SessionID,
					ProjectID:  sessionReq.ProjectID,
					RecordedAt: sessionReq.Timestamp,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	initialOutput := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--recent", "1", "--snapshot-mode", "skip"}))
	})

	var initialPayload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(initialOutput), &initialPayload))
	require.Equal(t, "uploaded", initialPayload.SessionStatus)
	require.Equal(t, 1, initialPayload.SessionUploaded)

	st, err := loadState()
	require.NoError(t, err)
	require.NotNil(t, st.LastUploadedSessionCursor)
	require.Equal(t, "session-2", st.LastUploadedSessionCursor.SessionID)

	resetOutput := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--reset-sessions", "--snapshot-mode", "skip"}))
	})

	var resetPayload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(resetOutput), &resetPayload))
	require.True(t, resetPayload.SessionCursorReset)
	require.Equal(t, "uploaded", resetPayload.SessionStatus)
	require.Equal(t, 2, resetPayload.SessionUploaded)
	require.Len(t, sessionReqs, 3)
	require.Equal(t, []string{"session-2", "session-1", "session-2"}, []string{
		sessionReqs[0].SessionID,
		sessionReqs[1].SessionID,
		sessionReqs[2].SessionID,
	})

	st, err = loadState()
	require.NoError(t, err)
	require.NotNil(t, st.LastUploadedSessionCursor)
	require.Equal(t, "session-2", st.LastUploadedSessionCursor.SessionID)
}

func TestRunCollectFallsBackToSingleSessionEndpointWhenBatchUnsupported(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nFallback to the legacy single-session endpoint."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Falling back to the legacy endpoint."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nRetry with one request per session."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Retrying with one request per session."}}`,
	})

	batchAttempts := 0
	sessionReqs := make([]request.SessionSummaryReq, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == sessionSummaryBatchPath:
			batchAttempts++
			w.WriteHeader(http.StatusNotFound)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code:    1000,
				Message: "Not Found",
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			var sessionReq request.SessionSummaryReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			sessionReqs = append(sessionReqs, sessionReq)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionIngestResp{
					SessionID:  sessionReq.SessionID,
					ProjectID:  sessionReq.ProjectID,
					RecordedAt: sessionReq.Timestamp,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "uploaded", payload.SessionStatus)
	require.Equal(t, 2, payload.SessionUploaded)
	require.Equal(t, 1, batchAttempts)
	require.Equal(t, []string{"session-1", "session-2"}, []string{sessionReqs[0].SessionID, sessionReqs[1].SessionID})
}

func TestRunCollectUsesSessionImportJobForLargeBackfill(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	originalThreshold := sessionImportJobAsyncThreshold
	originalPollInterval := sessionImportJobPollInterval
	sessionImportJobAsyncThreshold = 2
	sessionImportJobPollInterval = time.Millisecond
	t.Cleanup(func() {
		sessionImportJobAsyncThreshold = originalThreshold
		sessionImportJobPollInterval = originalPollInterval
	})

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nRun the large backfill through an async import job."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Running the large backfill through an async import job."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nPoll the async import job until it completes."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Polling the async import job until it completes."}}`,
	})

	jobID := "import-1"
	stagedSessions := make([]request.SessionSummaryReq, 0, 2)
	jobStatus := "receiving_chunks"
	var startedAt, completedAt *time.Time

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion: reportAPISchemaVersion,
					JobID:         jobID,
					ProjectID:     "project-1",
					Status:        jobStatus,
					CreatedAt:     time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/"+jobID+"/chunks":
			var chunkReq request.SessionImportJobChunkReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&chunkReq))
			stagedSessions = append(stagedSessions, chunkReq.Sessions...)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           jobStatus,
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/"+jobID+"/complete":
			jobStatus = "queued"
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           jobStatus,
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session-import-jobs/"+jobID:
			if jobStatus == "queued" {
				now := time.Now().UTC()
				startedAt = &now
				completedAt = &now
				jobStatus = "succeeded"
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:     reportAPISchemaVersion,
					JobID:             jobID,
					ProjectID:         "project-1",
					Status:            jobStatus,
					ReceivedSessions:  len(stagedSessions),
					ProcessedSessions: len(stagedSessions),
					UploadedSessions:  len(stagedSessions),
					CreatedAt:         time.Now().UTC(),
					StartedAt:         startedAt,
					CompletedAt:       completedAt,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "uploaded", payload.SessionStatus)
	require.Equal(t, 2, payload.SessionUploaded)
	require.Nil(t, payload.Sessions)
	require.NotNil(t, payload.ImportJob)
	require.Equal(t, jobID, payload.ImportJob.JobID)
	require.Equal(t, "succeeded", payload.ImportJob.Status)
	require.Len(t, stagedSessions, 2)
	require.Equal(t, []string{"session-1", "session-2"}, []string{stagedSessions[0].SessionID, stagedSessions[1].SessionID})

	st, err := loadState()
	require.NoError(t, err)
	require.NotNil(t, st.LastUploadedSessionCursor)
	require.Equal(t, "session-2", st.LastUploadedSessionCursor.SessionID)
}

func TestRunCollectDetachThenResumeSessionImportJob(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	originalThreshold := sessionImportJobAsyncThreshold
	originalPollInterval := sessionImportJobPollInterval
	sessionImportJobAsyncThreshold = 2
	sessionImportJobPollInterval = time.Millisecond
	t.Cleanup(func() {
		sessionImportJobAsyncThreshold = originalThreshold
		sessionImportJobPollInterval = originalPollInterval
	})

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nDetach after queueing the async import job."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Detaching after queueing the async import job."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nResume the existing async import job on the next run."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Resuming the existing async import job on the next run."}}`,
	})

	jobID := "import-resume-1"
	createCount := 0
	chunkCount := 0
	completeCount := 0
	getCount := 0
	stagedSessions := make([]request.SessionSummaryReq, 0, 2)
	jobStatus := "receiving_chunks"
	var startedAt, completedAt *time.Time

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs":
			createCount++
			var createReq request.SessionImportJobCreateReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&createReq))
			reused := createCount > 1
			if reused {
				jobStatus = "queued"
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           jobStatus,
					Reused:           reused,
					TotalSessions:    createReq.TotalSessions,
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/"+jobID+"/chunks":
			chunkCount++
			var chunkReq request.SessionImportJobChunkReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&chunkReq))
			stagedSessions = append(stagedSessions[:0], chunkReq.Sessions...)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           "receiving_chunks",
					TotalSessions:    len(chunkReq.Sessions),
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/"+jobID+"/complete":
			completeCount++
			jobStatus = "queued"
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           jobStatus,
					TotalSessions:    len(stagedSessions),
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session-import-jobs/"+jobID:
			getCount++
			if getCount == 1 {
				now := time.Now().UTC()
				startedAt = &now
				completedAt = &now
				jobStatus = "succeeded"
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:     reportAPISchemaVersion,
					JobID:             jobID,
					ProjectID:         "project-1",
					Status:            jobStatus,
					TotalSessions:     len(stagedSessions),
					ReceivedSessions:  len(stagedSessions),
					ProcessedSessions: len(stagedSessions),
					UploadedSessions:  len(stagedSessions),
					CreatedAt:         time.Now().UTC(),
					StartedAt:         startedAt,
					CompletedAt:       completedAt,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	detachOutput := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions", "--detach"}))
	})

	var detachPayload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(detachOutput), &detachPayload))
	require.Equal(t, "import_job_queued", detachPayload.SessionStatus)
	require.Equal(t, 0, detachPayload.SessionUploaded)
	require.NotNil(t, detachPayload.ImportJob)
	require.Equal(t, jobID, detachPayload.ImportJob.JobID)
	require.Equal(t, "queued", detachPayload.ImportJob.Status)
	require.Equal(t, 1, createCount)
	require.Equal(t, 1, chunkCount)
	require.Equal(t, 1, completeCount)
	require.Zero(t, getCount)

	st, err := loadState()
	require.NoError(t, err)
	require.Nil(t, st.LastUploadedSessionCursor)

	resumeOutput := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"}))
	})

	var resumePayload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(resumeOutput), &resumePayload))
	require.Equal(t, "uploaded", resumePayload.SessionStatus)
	require.Equal(t, 2, resumePayload.SessionUploaded)
	require.NotNil(t, resumePayload.ImportJob)
	require.Equal(t, jobID, resumePayload.ImportJob.JobID)
	require.Equal(t, "succeeded", resumePayload.ImportJob.Status)
	require.Equal(t, 2, createCount)
	require.Equal(t, 1, chunkCount)
	require.Equal(t, 1, completeCount)
	require.Equal(t, 1, getCount)

	st, err = loadState()
	require.NoError(t, err)
	require.NotNil(t, st.LastUploadedSessionCursor)
	require.Equal(t, "session-2", st.LastUploadedSessionCursor.SessionID)
}

func TestRunCollectReturnsErrorWhenSessionImportJobCanceled(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	originalThreshold := sessionImportJobAsyncThreshold
	originalPollInterval := sessionImportJobPollInterval
	sessionImportJobAsyncThreshold = 2
	sessionImportJobPollInterval = time.Millisecond
	t.Cleanup(func() {
		sessionImportJobAsyncThreshold = originalThreshold
		sessionImportJobPollInterval = originalPollInterval
	})

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nSurface canceled async import jobs as errors."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Polling the async import job until it is canceled."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nDo not advance the upload cursor when the import job gets canceled."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Leaving the upload cursor untouched because the import job was canceled."}}`,
	})

	jobID := "import-canceled-1"
	stagedSessions := make([]request.SessionSummaryReq, 0, 2)
	jobStatus := "receiving_chunks"
	var startedAt, completedAt *time.Time

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion: reportAPISchemaVersion,
					JobID:         jobID,
					ProjectID:     "project-1",
					Status:        jobStatus,
					CreatedAt:     time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/"+jobID+"/chunks":
			var chunkReq request.SessionImportJobChunkReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&chunkReq))
			stagedSessions = append(stagedSessions, chunkReq.Sessions...)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           jobStatus,
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/"+jobID+"/complete":
			jobStatus = "queued"
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           jobStatus,
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session-import-jobs/"+jobID:
			if jobStatus == "queued" {
				now := time.Now().UTC()
				startedAt = &now
				completedAt = &now
				jobStatus = "canceled"
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:     reportAPISchemaVersion,
					JobID:             jobID,
					ProjectID:         "project-1",
					Status:            jobStatus,
					ReceivedSessions:  len(stagedSessions),
					ProcessedSessions: 0,
					UploadedSessions:  0,
					CreatedAt:         time.Now().UTC(),
					StartedAt:         startedAt,
					CompletedAt:       completedAt,
					LastError:         "session import job canceled",
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	err := runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session import job "+jobID+" canceled")

	st, loadErr := loadState()
	require.NoError(t, loadErr)
	require.Nil(t, st.LastUploadedSessionCursor)
}

func TestRunCollectShowsImportJobETAAndLatestFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	originalThreshold := sessionImportJobAsyncThreshold
	originalPollInterval := sessionImportJobPollInterval
	sessionImportJobAsyncThreshold = 2
	sessionImportJobPollInterval = time.Millisecond
	t.Cleanup(func() {
		sessionImportJobAsyncThreshold = originalThreshold
		sessionImportJobPollInterval = originalPollInterval
	})

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nShow import progress with ETA."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Printing async import ETA while the job runs."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nSurface the latest import failure summary."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Printing the latest import failure when the async job degrades."}}`,
	})

	jobID := "import-progress-1"
	stagedSessions := make([]request.SessionSummaryReq, 0, 2)
	status := "receiving_chunks"
	pollCount := 0
	startedAt := time.Now().UTC().Add(-10 * time.Second)
	completedAt := startedAt.Add(12 * time.Second)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion: reportAPISchemaVersion,
					JobID:         jobID,
					ProjectID:     "project-1",
					Status:        status,
					CreatedAt:     time.Now().UTC(),
					TotalSessions: 2,
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/"+jobID+"/chunks":
			var chunkReq request.SessionImportJobChunkReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&chunkReq))
			stagedSessions = append(stagedSessions, chunkReq.Sessions...)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           status,
					TotalSessions:    2,
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/"+jobID+"/complete":
			status = "queued"
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion:    reportAPISchemaVersion,
					JobID:            jobID,
					ProjectID:        "project-1",
					Status:           status,
					TotalSessions:    2,
					ReceivedSessions: len(stagedSessions),
					CreatedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session-import-jobs/"+jobID:
			pollCount++
			jobResp := response.SessionImportJobResp{
				SchemaVersion:     reportAPISchemaVersion,
				JobID:             jobID,
				ProjectID:         "project-1",
				Status:            "running",
				TotalSessions:     2,
				ReceivedSessions:  len(stagedSessions),
				ProcessedSessions: 1,
				UploadedSessions:  1,
				FailedSessions:    1,
				CreatedAt:         time.Now().UTC(),
				StartedAt:         &startedAt,
				Failures: []response.SessionImportJobFailureResp{{
					SessionID:    "session-2",
					Error:        "upstream rejected duplicate",
					HTTPStatus:   http.StatusTooManyRequests,
					APIErrorCode: 1001,
				}},
				LastError: "upstream rejected duplicate",
			}
			if pollCount > 1 {
				jobResp.Status = "partially_failed"
				jobResp.ProcessedSessions = 2
				jobResp.CompletedAt = &completedAt
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, jobResp),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	var output string
	stderr := captureStderr(t, func() {
		output = captureStdout(t, func() {
			require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"}))
		})
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "uploaded_with_failures", payload.SessionStatus)
	require.Equal(t, 1, payload.SessionUploaded)
	require.NotNil(t, payload.ImportJob)
	require.Equal(t, "partially_failed", payload.ImportJob.Status)
	require.Contains(t, stderr, "Server processing import job: 1/2 sessions (ETA")
	require.Contains(t, stderr, "Latest import failure: session-2 - upstream rejected duplicate - http 429 - api 1001")
}

func TestRunCollectFallsBackToBatchWhenImportJobUnsupported(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	originalThreshold := sessionImportJobAsyncThreshold
	sessionImportJobAsyncThreshold = 2
	t.Cleanup(func() {
		sessionImportJobAsyncThreshold = originalThreshold
	})

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nFallback to batch when async import jobs are unsupported."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Falling back to batch when async import jobs are unsupported."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUse the batch endpoint after the async create call fails."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Using the batch endpoint after the async create call fails."}}`,
	})

	asyncAttempts := 0
	stagedSessions := make([]request.SessionSummaryReq, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs":
			asyncAttempts++
			w.WriteHeader(http.StatusNotFound)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code:    1000,
				Message: "Not Found",
			}))
		case r.Method == http.MethodPost && r.URL.Path == sessionSummaryBatchPath:
			var batchReq request.SessionSummaryBatchReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&batchReq))
			stagedSessions = append(stagedSessions, batchReq.Sessions...)
			items := make([]response.SessionBatchIngestItemResp, 0, len(batchReq.Sessions))
			for _, item := range batchReq.Sessions {
				items = append(items, uploadedSessionBatchItem(item))
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, sessionBatchResp("project-1", items...)),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "uploaded", payload.SessionStatus)
	require.Equal(t, 2, payload.SessionUploaded)
	require.Nil(t, payload.ImportJob)
	require.Equal(t, 1, asyncAttempts)
	require.Len(t, stagedSessions, 2)
	require.Equal(t, []string{"session-1", "session-2"}, []string{stagedSessions[0].SessionID, stagedSessions[1].SessionID})
}

func TestRunCollectSkipsInvalidSessionAndAdvancesCursor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpload session one."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Uploaded session one."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpload session two."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Uploaded session two."}}`,
	})

	sessionReqs := make([]request.SessionSummaryReq, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == sessionSummaryBatchPath:
			var batchReq request.SessionSummaryBatchReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&batchReq))
			sessionReqs = append(sessionReqs, batchReq.Sessions...)
			items := make([]response.SessionBatchIngestItemResp, 0, len(batchReq.Sessions))
			for _, item := range batchReq.Sessions {
				if item.SessionID == "session-2" {
					items = append(items, response.SessionBatchIngestItemResp{
						SessionID:    item.SessionID,
						Status:       "failed",
						Error:        "Invalid Params",
						HTTPStatus:   http.StatusBadRequest,
						APIErrorCode: 1000,
					})
					continue
				}
				items = append(items, uploadedSessionBatchItem(item))
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, sessionBatchResp("project-1", items...)),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			var sessionReq request.SessionSummaryReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			sessionReqs = append(sessionReqs, sessionReq)
			if sessionReq.SessionID == "session-2" {
				w.WriteHeader(http.StatusBadRequest)
				require.NoError(t, json.NewEncoder(w).Encode(envelope{
					Code:    1000,
					Message: "Invalid Params",
				}))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionIngestResp{
					SessionID:  sessionReq.SessionID,
					ProjectID:  sessionReq.ProjectID,
					RecordedAt: sessionReq.Timestamp,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "uploaded_with_failures", payload.SessionStatus)
	require.Equal(t, 1, payload.SessionUploaded)
	require.Len(t, payload.SessionFailures, 1)
	require.Equal(t, "session-2", payload.SessionFailures[0].SessionID)
	require.Contains(t, payload.SessionFailures[0].Error, "Invalid Params")
	require.Equal(t, http.StatusBadRequest, payload.SessionFailures[0].HTTPStatus)
	require.Equal(t, 1000, payload.SessionFailures[0].APIErrorCode)
	require.Len(t, sessionReqs, 2)

	st, err := loadState()
	require.NoError(t, err)
	require.NotNil(t, st.LastUploadedSessionCursor)
	require.Equal(t, "session-2", st.LastUploadedSessionCursor.SessionID)

	retryOutput := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip"}))
	})

	var retryPayload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(retryOutput), &retryPayload))
	require.Equal(t, "up_to_date", retryPayload.SessionStatus)
}

func TestRunCollectIncludesStructuredDebugFailureFields(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)
	t.Setenv("CRUX_DEBUG_HTTP", "1")

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-debug.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-debug","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nReproduce invalid params with debug enabled."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Attempting reproduction."}}`,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			w.WriteHeader(http.StatusBadRequest)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code:    1000,
				Message: "Invalid Params",
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "skipped_invalid_sessions", payload.SessionStatus)
	require.Equal(t, 0, payload.SessionUploaded)
	require.Len(t, payload.SessionFailures, 1)
	require.Equal(t, "session-debug", payload.SessionFailures[0].SessionID)
	require.Equal(t, http.StatusBadRequest, payload.SessionFailures[0].HTTPStatus)
	require.Equal(t, 1000, payload.SessionFailures[0].APIErrorCode)
	require.Equal(t, http.MethodPost, payload.SessionFailures[0].HTTPMethod)
	require.Equal(t, "/api/v1/session-summaries", payload.SessionFailures[0].HTTPPath)
	require.Contains(t, payload.SessionFailures[0].RequestBody, `"session_id":"session-debug"`)
	require.Contains(t, payload.SessionFailures[0].RequestBody, `"tool":"codex"`)
	require.Contains(t, payload.SessionFailures[0].ResponseBody, `"msg":"Invalid Params"`)
}

func TestRunCollectRetriesRateLimitedSessionUpload(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	originalBaseDelay := sessionUploadRetryBaseDelay
	originalMaxDelay := sessionUploadRetryMaxDelay
	originalMaxAttempt := sessionUploadRetryMaxAttempt
	sessionUploadRetryBaseDelay = time.Millisecond
	sessionUploadRetryMaxDelay = 2 * time.Millisecond
	sessionUploadRetryMaxAttempt = 4
	t.Cleanup(func() {
		sessionUploadRetryBaseDelay = originalBaseDelay
		sessionUploadRetryMaxDelay = originalMaxDelay
		sessionUploadRetryMaxAttempt = originalMaxAttempt
	})

	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "session-retry.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"session-retry","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nRetry when the server rate limits this upload."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Retrying after rate limiting."}}`,
	})

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			requestCount++
			if requestCount < 3 {
				w.WriteHeader(http.StatusTooManyRequests)
				require.NoError(t, json.NewEncoder(w).Encode(envelope{
					Code:    1000,
					Message: "Invalid Params",
				}))
				return
			}
			var sessionReq request.SessionSummaryReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionIngestResp{
					SessionID:  sessionReq.SessionID,
					ProjectID:  sessionReq.ProjectID,
					RecordedAt: sessionReq.Timestamp,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-collect",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome, "--snapshot-mode", "skip", "--reset-sessions"}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "uploaded", payload.SessionStatus)
	require.Equal(t, 1, payload.SessionUploaded)
	require.Len(t, payload.Sessions, 1)
	require.Equal(t, 3, requestCount)
}

func TestWatchCollectSessionChangesEmitsOnSessionWrite(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	sessionPath := filepath.Join(codexHome, "sessions", "2026", "03", "10", "watch.jsonl")
	writeCodexSessionFixture(t, sessionPath, time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC), []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"watch-session","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nWatch session file changes."}}`,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changes, errs, closeWatcher, err := watchCollectSessionChanges(ctx, "", codexHome)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, closeWatcher())
	}()

	file, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = file.WriteString(`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"The watcher should react to this append."}}` + "\n")
	require.NoError(t, err)
	require.NoError(t, file.Close())

	select {
	case <-changes:
	case err := <-errs:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for session file watcher event")
	}
}

func TestResolveBackgroundBaseCommandPrefersInstalledCrux(t *testing.T) {
	root, err := os.MkdirTemp(".", ".crux-bin-*")
	require.NoError(t, err)
	root, err = filepath.Abs(root)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
	})

	binDir := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	cruxPath := filepath.Join(binDir, "crux")
	require.NoError(t, os.WriteFile(cruxPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)

	command, err := resolveBackgroundBaseCommand()
	require.NoError(t, err)
	require.Equal(t, cruxPath, command.Program)
	require.Empty(t, command.Args)
	require.Empty(t, command.Workdir)
}
