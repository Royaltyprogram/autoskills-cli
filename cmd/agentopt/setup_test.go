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

func TestRunSetupLogsInConnectsAndCollects(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	repoPath := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))

	codexHome := filepath.Join(root, ".codex")
	sessionPath := filepath.Join(codexHome, "sessions", "2026", "03", "12", "latest.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sessionPath), 0o755))
	require.NoError(t, os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-12T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-setup","timestamp":"2026-03-12T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-12T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nSimplify the setup flow."}}`,
		`{"timestamp":"2026-03-12T08:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":400,"cached_input_tokens":100,"output_tokens":80,"reasoning_output_tokens":20,"total_tokens":480}}}}`,
	}, "\n")+"\n"), 0o644))

	var loginReq request.CLILoginReq
	var projectReq request.RegisterProjectReq
	var snapshotReq request.ConfigSnapshotReq
	var sessionReq request.SessionSummaryReq

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/cli/login":
			require.Equal(t, "setup-token", r.Header.Get("X-AgentOpt-Token"))
			require.NoError(t, json.NewDecoder(r.Body).Decode(&loginReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.CLILoginResp{
					AgentID:      "agent-1",
					DeviceID:     "device-1",
					OrgID:        "org-1",
					UserID:       "user-1",
					Status:       "registered",
					RegisteredAt: time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/register":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&projectReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ProjectRegistrationResp{
					ProjectID:   "project-1",
					Status:      "connected",
					ConnectedAt: time.Now().UTC(),
				}),
			}))
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
					RecordedAt:  sessionReq.Timestamp,
					ReportCount: 1,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		require.NoError(t, run([]string{
			"setup",
			"--server", server.URL,
			"--token", "setup-token",
			"--repo-path", repoPath,
			"--codex-home", codexHome,
			"--background=false",
		}))
	})

	var payload setupResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, server.URL, payload.ServerURL)
	require.Equal(t, "project-1", payload.WorkspaceID)
	require.Equal(t, sharedWorkspaceName, payload.WorkspaceName)
	require.Equal(t, repoPath, payload.RepoPath)
	require.NotNil(t, payload.Collect)
	require.Equal(t, "uploaded", payload.Collect.SnapshotStatus)
	require.Equal(t, "uploaded", payload.Collect.SessionStatus)
	require.Equal(t, 1, payload.Collect.SessionUploaded)
	require.Equal(t, "disabled", payload.Background.Status)
	require.Contains(t, payload.Background.Command, "agentopt")

	require.NotEmpty(t, loginReq.DeviceName)
	require.NotEmpty(t, loginReq.Platform)
	require.Equal(t, []string{"codex", "claude-code"}, loginReq.Tools)
	require.Equal(t, "org-1", projectReq.OrgID)
	require.Equal(t, "device-1", projectReq.AgentID)
	require.Equal(t, repoPath, projectReq.RepoPath)
	require.Equal(t, "codex", projectReq.DefaultTool)
	require.Equal(t, map[string]float64{"go": 1}, projectReq.LanguageMix)
	require.Equal(t, "project-1", snapshotReq.ProjectID)
	require.Equal(t, "project-1", sessionReq.ProjectID)
	require.Equal(t, "codex-session-setup", sessionReq.SessionID)
}

func TestRunSetupEnablesBackgroundWhenInstalledBinaryAndLaunchctlAreAvailable(t *testing.T) {
	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(homeDir, 0o755))
	t.Setenv("HOME", homeDir)
	t.Setenv("AGENTOPT_HOME", filepath.Join(root, "agentopt-home"))

	binRoot, err := os.MkdirTemp(".", ".agentopt-installed-*")
	require.NoError(t, err)
	binRoot, err = filepath.Abs(binRoot)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(binRoot)
	})
	binDir := filepath.Join(binRoot, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	agentoptPath := filepath.Join(binDir, "agentopt")
	require.NoError(t, os.WriteFile(agentoptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	launchctlLog := filepath.Join(root, "launchctl.log")
	launchctlPath := filepath.Join(root, "launchctl")
	require.NoError(t, os.WriteFile(launchctlPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$LAUNCHCTL_LOG\"\nexit 0\n"), 0o755))
	t.Setenv("LAUNCHCTL_LOG", launchctlLog)
	t.Setenv("AGENTOPT_LAUNCHCTL_BIN", launchctlPath)

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)

	repoPath := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))

	var loginReq request.CLILoginReq
	var projectReq request.RegisterProjectReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/cli/login":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&loginReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.CLILoginResp{
					AgentID:      "agent-1",
					DeviceID:     "device-1",
					OrgID:        "org-1",
					UserID:       "user-1",
					Status:       "registered",
					RegisteredAt: time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/register":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&projectReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ProjectRegistrationResp{
					ProjectID:   "project-1",
					Status:      "connected",
					ConnectedAt: time.Now().UTC(),
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		require.NoError(t, run([]string{
			"setup",
			"--server", server.URL,
			"--token", "setup-token",
			"--repo-path", repoPath,
			"--upload=false",
			"--background-interval", "10m",
		}))
	})

	var payload setupResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "enabled", payload.Background.Status)
	require.Equal(t, "10m0s", payload.Background.Interval)
	require.NotEmpty(t, payload.Background.PlistPath)
	require.FileExists(t, payload.Background.PlistPath)
	require.NotEmpty(t, loginReq.DeviceName)
	require.Equal(t, repoPath, projectReq.RepoPath)

	plistData, err := os.ReadFile(payload.Background.PlistPath)
	require.NoError(t, err)
	plistText := string(plistData)
	require.Contains(t, plistText, agentoptPath)
	require.Contains(t, plistText, "<string>collect</string>")
	require.Contains(t, plistText, "<string>--watch</string>")
	require.Contains(t, plistText, "<string>--interval</string>")
	require.Contains(t, plistText, "<string>10m0s</string>")

	logData, err := os.ReadFile(launchctlLog)
	require.NoError(t, err)
	logText := string(logData)
	require.Contains(t, logText, "load")
	require.Contains(t, logText, payload.Background.PlistPath)
}

func TestDefaultCommandShowsSetupHintWhenUnconfigured(t *testing.T) {
	t.Setenv("AGENTOPT_HOME", t.TempDir())

	output := captureStdout(t, func() {
		require.NoError(t, run(nil))
	})

	require.Contains(t, output, "agentopt is not set up yet.")
	require.Contains(t, output, "agentopt setup --server <server-url>")
}

func TestHelpHighlightsSetup(t *testing.T) {
	output := captureStdout(t, func() {
		require.NoError(t, run([]string{"help"}))
	})

	require.Contains(t, output, "agentopt quickstart:")
	require.Contains(t, output, "setup             register this device")
	require.Contains(t, output, "agentopt setup --server http://127.0.0.1:8082")
}

func TestRunWithoutArgsUsesSavedServerHintWhenWorkspaceMissing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	require.NoError(t, saveState(state{
		ServerURL: "https://agentopt.example.com",
		APIToken:  "token",
		OrgID:     "org-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, run(nil))
	})

	require.Contains(t, output, "agentopt setup --server https://agentopt.example.com")
}

func TestRunWithoutArgsShowsStatusWhenConfigured(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/dashboard/overview":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.DashboardOverviewResp{
					OrgID: "org-1",
				}),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/reports":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ReportListResp{}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token",
		OrgID:       "org-1",
		UserID:      "user-1",
		AgentID:     "agent-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, run(nil))
	})

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "project-1", payload["workspace_id"])
	require.Equal(t, sharedWorkspaceName, payload["workspace_name"])
}
