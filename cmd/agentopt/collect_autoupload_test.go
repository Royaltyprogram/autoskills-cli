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
	t.Setenv("AGENTOPT_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	sessionPath := filepath.Join(codexHome, "sessions", "2026", "03", "10", "latest.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sessionPath), 0o755))
	require.NoError(t, os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-collect","timestamp":"2026-03-10T08:00:00Z"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nSummarize the upload pipeline."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":900,"output_tokens":180,"total_tokens":1080}}}}`,
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
					SessionID:           sessionReq.SessionID,
					ProjectID:           sessionReq.ProjectID,
					RecommendationCount: 1,
					RecordedAt:          sessionReq.Timestamp,
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
}

func TestRunCollectSkipsUnchangedSnapshotAndHandlesMissingSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

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

func TestRunAutouploadEnableStatusDisable(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("AGENTOPT_HOME", filepath.Join(root, ".agentopt"))
	t.Setenv("HOME", home)

	launchctlLog := filepath.Join(root, "launchctl.log")
	launchctlStub := filepath.Join(root, "launchctl-stub.sh")
	require.NoError(t, os.WriteFile(launchctlStub, []byte(`#!/bin/sh
set -eu
printf '%s %s
' "$1" "$*" >> "$AGENTOPT_LAUNCHCTL_LOG"
exit 0
`), 0o755))
	t.Setenv("AGENTOPT_LAUNCHCTL_BIN", launchctlStub)
	t.Setenv("AGENTOPT_LAUNCHCTL_LOG", launchctlLog)

	require.NoError(t, saveState(state{
		ServerURL:   "http://127.0.0.1:8082",
		APIToken:    "token-autoupload",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	enableOutput := captureStdout(t, func() {
		require.NoError(t, runAutoupload([]string{"enable", "--interval", "45m", "--recent", "2", "--snapshot-mode", "skip"}))
	})

	var enabled autouploadStatusResp
	require.NoError(t, json.Unmarshal([]byte(enableOutput), &enabled))
	require.True(t, enabled.Enabled)
	require.True(t, enabled.Loaded)
	require.NotNil(t, enabled.Metadata)
	require.Equal(t, 2700, enabled.Metadata.IntervalSeconds)
	require.Equal(t, 2, enabled.Metadata.Recent)
	require.Equal(t, "skip", enabled.Metadata.SnapshotMode)
	require.FileExists(t, enabled.Metadata.PlistPath)
	require.FileExists(t, enabled.Metadata.ScriptPath)

	scriptData, err := os.ReadFile(enabled.Metadata.ScriptPath)
	require.NoError(t, err)
	require.Contains(t, string(scriptData), "collect")
	require.Contains(t, string(scriptData), "--snapshot-mode")
	require.Contains(t, string(scriptData), "skip")

	statusOutput := captureStdout(t, func() {
		require.NoError(t, runAutoupload([]string{"status"}))
	})
	var status autouploadStatusResp
	require.NoError(t, json.Unmarshal([]byte(statusOutput), &status))
	require.True(t, status.Enabled)
	require.True(t, status.Loaded)
	require.NotNil(t, status.Metadata)
	require.Equal(t, enabled.Metadata.Label, status.Metadata.Label)

	disableOutput := captureStdout(t, func() {
		require.NoError(t, runAutoupload([]string{"disable"}))
	})
	var disabled autouploadDisableResp
	require.NoError(t, json.Unmarshal([]byte(disableOutput), &disabled))
	require.True(t, disabled.Disabled)
	require.NoFileExists(t, enabled.Metadata.PlistPath)
	require.NoFileExists(t, enabled.Metadata.ScriptPath)

	logData, err := os.ReadFile(launchctlLog)
	require.NoError(t, err)
	require.Contains(t, string(logData), "load")
	require.Contains(t, string(logData), "list")
	require.Contains(t, string(logData), "unload")
}

func TestResolveAutouploadBaseCommandPrefersInstalledAgentopt(t *testing.T) {
	root, err := os.MkdirTemp(".", ".agentopt-bin-*")
	require.NoError(t, err)
	root, err = filepath.Abs(root)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(root)
	})

	binDir := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	agentoptPath := filepath.Join(binDir, "agentopt")
	require.NoError(t, os.WriteFile(agentoptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)

	command, err := resolveAutouploadBaseCommand()
	require.NoError(t, err)
	require.Equal(t, agentoptPath, command.Program)
	require.Empty(t, command.Args)
	require.Empty(t, command.Workdir)
}
