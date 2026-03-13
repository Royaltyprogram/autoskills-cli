package main

import (
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
