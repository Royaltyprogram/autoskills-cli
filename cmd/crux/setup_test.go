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

func testCLILoginResp(deviceID, orgID, userID string) response.CLILoginResp {
	now := time.Now().UTC()
	accessExpiresAt := now.Add(24 * time.Hour)
	refreshExpiresAt := now.Add(30 * 24 * time.Hour)
	return response.CLILoginResp{
		AccessToken:      "agt_dva_" + deviceID,
		AccessExpiresAt:  &accessExpiresAt,
		RefreshToken:     "agt_dvr_" + deviceID,
		RefreshExpiresAt: &refreshExpiresAt,
		TokenType:        "Bearer",
		AgentID:          deviceID,
		DeviceID:         deviceID,
		OrgID:            orgID,
		UserID:           userID,
		Status:           "registered",
		RegisteredAt:     now,
	}
}

func TestRunSetupLogsInConnectsAndCollects(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

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
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/cli/login":
			require.Equal(t, "setup-token", r.Header.Get("X-Crux-Token"))
			require.NoError(t, json.NewDecoder(r.Body).Decode(&loginReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, testCLILoginResp("device-1", "org-1", "user-1")),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/register":
			require.Equal(t, "agt_dva_device-1", r.Header.Get("X-Crux-Token"))
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
	require.Contains(t, payload.Background.Command, "crux")

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

func TestRunSetupBackfillsFullCodexHistoryOnFirstWorkspaceSetup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	repoPath := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))

	codexHome := filepath.Join(root, ".codex")
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "older.jsonl"), time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC), []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nReview the collector flow."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "12", "newer.jsonl"), time.Date(2026, 3, 12, 8, 0, 0, 0, time.UTC), []string{
		`{"timestamp":"2026-03-12T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-2","timestamp":"2026-03-12T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-12T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nVerify the latest setup upload."}}`,
	})

	sessionIDs := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/cli/login":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, testCLILoginResp("device-1", "org-1", "user-1")),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/register":
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
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotResp{
					SnapshotID: "snapshot-1",
					ProjectID:  "project-1",
					CapturedAt: time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == sessionSummaryBatchPath:
			var batchReq request.SessionSummaryBatchReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&batchReq))
			for _, sessionReq := range batchReq.Sessions {
				sessionIDs = append(sessionIDs, sessionReq.SessionID)
			}
			items := make([]response.SessionBatchIngestItemResp, 0, len(batchReq.Sessions))
			for _, sessionReq := range batchReq.Sessions {
				items = append(items, uploadedSessionBatchItem(sessionReq))
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, sessionBatchResp("project-1", items...)),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			var sessionReq request.SessionSummaryReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			sessionIDs = append(sessionIDs, sessionReq.SessionID)
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
	require.NotNil(t, payload.Collect)
	require.Equal(t, "uploaded", payload.Collect.SessionStatus)
	require.Equal(t, 2, payload.Collect.SessionUploaded)
	require.Equal(t, []string{"codex-session-1", "codex-session-2"}, sessionIDs)
}

func TestRunSetupKeepsRecentIncrementalUploadWhenWorkspaceAlreadyConfigured(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	require.NoError(t, saveState(state{
		ServerURL:   "https://existing.example.com",
		APIToken:    "existing-token",
		OrgID:       "org-1",
		UserID:      "user-1",
		AgentID:     "agent-1",
		WorkspaceID: "existing-workspace",
	}))

	repoPath := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))

	codexHome := filepath.Join(root, ".codex")
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "older.jsonl"), time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC), []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nReview the collector flow."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "12", "newer.jsonl"), time.Date(2026, 3, 12, 8, 0, 0, 0, time.UTC), []string{
		`{"timestamp":"2026-03-12T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-2","timestamp":"2026-03-12T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-12T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nVerify the latest setup upload."}}`,
	})

	sessionIDs := make([]string, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/cli/login":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, testCLILoginResp("device-2", "org-1", "user-1")),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/register":
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
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotResp{
					SnapshotID: "snapshot-1",
					ProjectID:  "project-1",
					CapturedAt: time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			var sessionReq request.SessionSummaryReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			sessionIDs = append(sessionIDs, sessionReq.SessionID)
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
	require.NotNil(t, payload.Collect)
	require.Equal(t, "uploaded", payload.Collect.SessionStatus)
	require.Equal(t, 1, payload.Collect.SessionUploaded)
	require.Equal(t, []string{"codex-session-2"}, sessionIDs)
}

func TestRunSetupReusesSavedLoginWithoutPromptingForCLIToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	repoPath := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))

	codexHome := filepath.Join(root, ".codex")
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "12", "latest.jsonl"), time.Date(2026, 3, 12, 8, 0, 0, 0, time.UTC), []string{
		`{"timestamp":"2026-03-12T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-setup","timestamp":"2026-03-12T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-12T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nReuse the saved login state."}}`,
	})

	sessionIDs := make([]string, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/cli/login":
			t.Fatalf("setup should reuse the saved device login instead of calling /auth/cli/login")
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/register":
			require.Equal(t, "saved-access", r.Header.Get("X-Crux-Token"))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ProjectRegistrationResp{
					ProjectID:   "project-1",
					Status:      "connected",
					ConnectedAt: time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config-snapshots":
			require.Equal(t, "saved-access", r.Header.Get("X-Crux-Token"))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotListResp{}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/config-snapshots":
			require.Equal(t, "saved-access", r.Header.Get("X-Crux-Token"))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotResp{
					SnapshotID: "snapshot-1",
					ProjectID:  "project-1",
					CapturedAt: time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			require.Equal(t, "saved-access", r.Header.Get("X-Crux-Token"))
			var sessionReq request.SessionSummaryReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&sessionReq))
			sessionIDs = append(sessionIDs, sessionReq.SessionID)
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
		ServerURL:    server.URL,
		AccessToken:  "saved-access",
		RefreshToken: "saved-refresh",
		TokenType:    "Bearer",
		OrgID:        "org-1",
		UserID:       "user-1",
		AgentID:      "agent-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, run([]string{
			"setup",
			"--repo-path", repoPath,
			"--codex-home", codexHome,
			"--background=false",
		}))
	})

	var payload setupResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "reused", payload.Login.Status)
	require.Equal(t, "project-1", payload.WorkspaceID)
	require.Equal(t, []string{"codex-session-setup"}, sessionIDs)
}

func TestRunSetupRejectsExplicitServerMismatchWithoutCLIToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	require.NoError(t, saveState(state{
		ServerURL:    "https://saved.example.com",
		AccessToken:  "saved-access",
		RefreshToken: "saved-refresh",
		TokenType:    "Bearer",
		OrgID:        "org-1",
		UserID:       "user-1",
		AgentID:      "agent-1",
	}))

	err := run([]string{
		"setup",
		"--server", "https://other.example.com",
		"--background=false",
	})
	require.EqualError(t, err, "saved cli state is for https://saved.example.com, but setup requested https://other.example.com; run `crux login --server https://other.example.com --token <CLI_TOKEN_FROM_DASHBOARD>` or `crux reset` first")
}

func TestRunSetupEnablesBackgroundWhenInstalledBinaryAndLaunchctlAreAvailable(t *testing.T) {
	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(homeDir, 0o755))
	t.Setenv("HOME", homeDir)
	t.Setenv("CRUX_HOME", filepath.Join(root, "crux-home"))

	binRoot, err := os.MkdirTemp(".", ".crux-installed-*")
	require.NoError(t, err)
	binRoot, err = filepath.Abs(binRoot)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(binRoot)
	})
	binDir := filepath.Join(binRoot, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	cruxPath := filepath.Join(binDir, "crux")
	require.NoError(t, os.WriteFile(cruxPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	launchctlLog := filepath.Join(root, "launchctl.log")
	launchctlPath := filepath.Join(root, "launchctl")
	require.NoError(t, os.WriteFile(launchctlPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$LAUNCHCTL_LOG\"\nexit 0\n"), 0o755))
	t.Setenv("LAUNCHCTL_LOG", launchctlLog)
	t.Setenv("CRUX_LAUNCHCTL_BIN", launchctlPath)

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+originalPath)

	repoPath := filepath.Join(root, "workspace")
	require.NoError(t, os.MkdirAll(repoPath, 0o755))

	var loginReq request.CLILoginReq
	var projectReq request.RegisterProjectReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/cli/login":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&loginReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, testCLILoginResp("device-1", "org-1", "user-1")),
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
	require.Contains(t, plistText, cruxPath)
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
	t.Setenv("CRUX_HOME", t.TempDir())

	output := captureStdout(t, func() {
		require.NoError(t, run(nil))
	})

	require.Contains(t, output, "Crux is not set up yet.")
	require.Contains(t, output, "crux setup")
}

func TestHelpHighlightsSetup(t *testing.T) {
	output := captureStdout(t, func() {
		require.NoError(t, run([]string{"help"}))
	})

	require.Contains(t, output, "Crux quickstart:")
	require.Contains(t, output, "setup             register this device")
	require.Contains(t, output, "crux setup")
}

func TestRunWithoutArgsUsesSavedServerHintWhenWorkspaceMissing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	require.NoError(t, saveState(state{
		ServerURL: "https://crux.example.com",
		APIToken:  "token",
		OrgID:     "org-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, run(nil))
	})

	require.Contains(t, output, "crux setup --server https://crux.example.com")
}

func TestRunWithoutArgsShowsStatusWhenConfigured(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/dashboard/overview":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.DashboardOverviewResp{
					OrgID: "org-1",
					ActiveImportJob: &response.SessionImportJobResp{
						SchemaVersion:     reportAPISchemaVersion,
						JobID:             "import-1",
						ProjectID:         "project-1",
						Status:            "running",
						TotalSessions:     12,
						ReceivedSessions:  12,
						ProcessedSessions: 5,
					},
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
		LastUploadedSessionCursor: &sessionUploadCursor{
			TailPath:    filepath.Join(root, ".codex", "sessions", "2026", "03", "14", "latest.jsonl"),
			TailModTime: time.Date(2026, 3, 14, 8, 0, 0, 0, time.UTC),
			TailSize:    321,
			SessionID:   "session-123",
		},
	}))

	output := captureStdout(t, func() {
		require.NoError(t, run(nil))
	})

	var payload struct {
		WorkspaceID               string                         `json:"workspace_id"`
		WorkspaceName             string                         `json:"workspace_name"`
		LastUploadedSessionCursor *sessionUploadCursor           `json:"last_uploaded_session_cursor"`
		Overview                  response.DashboardOverviewResp `json:"overview"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "project-1", payload.WorkspaceID)
	require.Equal(t, sharedWorkspaceName, payload.WorkspaceName)
	require.NotNil(t, payload.LastUploadedSessionCursor)
	require.Equal(t, "session-123", payload.LastUploadedSessionCursor.SessionID)
	require.Equal(t, int64(321), payload.LastUploadedSessionCursor.TailSize)
	require.NotNil(t, payload.Overview.ActiveImportJob)
	require.Equal(t, "import-1", payload.Overview.ActiveImportJob.JobID)
	require.Equal(t, "running", payload.Overview.ActiveImportJob.Status)
}

func TestRunImportsListsWorkspaceScopedImportJobs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session-import-jobs":
			require.Equal(t, "project-1", r.URL.Query().Get("project_id"))
			require.Equal(t, "running", r.URL.Query().Get("status"))
			require.Equal(t, "5", r.URL.Query().Get("limit"))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobListResp{
					Items: []response.SessionImportJobResp{{
						SchemaVersion:     reportAPISchemaVersion,
						JobID:             "import-1",
						ProjectID:         "project-1",
						Status:            "running",
						TotalSessions:     12,
						ReceivedSessions:  12,
						ProcessedSessions: 7,
						UploadedSessions:  7,
						CreatedAt:         time.Now().UTC(),
					}},
				}),
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
		require.NoError(t, run([]string{"imports", "--status", "running", "--limit", "5"}))
	})

	var payload struct {
		WorkspaceID   string                          `json:"workspace_id"`
		WorkspaceName string                          `json:"workspace_name"`
		Items         []response.SessionImportJobResp `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "project-1", payload.WorkspaceID)
	require.Equal(t, sharedWorkspaceName, payload.WorkspaceName)
	require.Len(t, payload.Items, 1)
	require.Equal(t, "import-1", payload.Items[0].JobID)
	require.Equal(t, "running", payload.Items[0].Status)
	require.Equal(t, 12, payload.Items[0].TotalSessions)
}

func TestRunImportsSupportsCursorFailedOnlyProjectAndAgentFilters(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session-import-jobs":
			require.Equal(t, "project-2", r.URL.Query().Get("project_id"))
			require.Equal(t, "agent-2", r.URL.Query().Get("agent_id"))
			require.Equal(t, "true", r.URL.Query().Get("failed_only"))
			require.Equal(t, "import-cursor-1", r.URL.Query().Get("cursor"))
			require.Equal(t, "1", r.URL.Query().Get("limit"))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobListResp{
					Items: []response.SessionImportJobResp{{
						SchemaVersion:     reportAPISchemaVersion,
						JobID:             "import-2",
						ProjectID:         "project-2",
						Status:            "failed",
						TotalSessions:     8,
						ReceivedSessions:  8,
						ProcessedSessions: 5,
						UploadedSessions:  3,
						FailedSessions:    2,
						CreatedAt:         time.Now().UTC(),
					}},
					NextCursor: "import-cursor-2",
				}),
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
		require.NoError(t, run([]string{"imports", "--project-id", "project-2", "--agent-id", "agent-2", "--failed-only", "--cursor", "import-cursor-1", "--limit", "1"}))
	})

	var payload struct {
		WorkspaceID   string                          `json:"workspace_id"`
		WorkspaceName string                          `json:"workspace_name"`
		NextCursor    string                          `json:"next_cursor"`
		Items         []response.SessionImportJobResp `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "project-2", payload.WorkspaceID)
	require.Equal(t, sharedWorkspaceName, payload.WorkspaceName)
	require.Equal(t, "import-cursor-2", payload.NextCursor)
	require.Len(t, payload.Items, 1)
	require.Equal(t, "import-2", payload.Items[0].JobID)
	require.Equal(t, "failed", payload.Items[0].Status)
}

func TestRunImportsCancelCancelsImportJob(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-import-jobs/import-9/cancel":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionImportJobResp{
					SchemaVersion: reportAPISchemaVersion,
					JobID:         "import-9",
					ProjectID:     "project-1",
					Status:        "canceled",
					TotalSessions: 12,
					CreatedAt:     time.Now().UTC(),
					LastError:     "session import job canceled by operator",
				}),
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
		require.NoError(t, run([]string{"imports", "cancel", "import-9"}))
	})

	var payload struct {
		WorkspaceID   string                        `json:"workspace_id"`
		WorkspaceName string                        `json:"workspace_name"`
		Job           response.SessionImportJobResp `json:"job"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "project-1", payload.WorkspaceID)
	require.Equal(t, sharedWorkspaceName, payload.WorkspaceName)
	require.Equal(t, "import-9", payload.Job.JobID)
	require.Equal(t, "canceled", payload.Job.Status)
	require.Contains(t, payload.Job.LastError, "canceled")
}

func TestRunWorkspaceIncludesLastUploadedSessionCursor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ProjectListResp{
					Items: []response.ProjectResp{{
						ID:          "project-1",
						Name:        sharedWorkspaceName,
						RepoHash:    "repo-1",
						RepoPath:    filepath.Join(root, "workspace"),
						DefaultTool: "codex",
					}},
				}),
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
		LastUploadedSessionCursor: &sessionUploadCursor{
			TailPath:    filepath.Join(root, ".codex", "sessions", "2026", "03", "14", "latest.jsonl"),
			TailModTime: time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC),
			TailSize:    456,
			SessionID:   "session-456",
		},
	}))

	output := captureStdout(t, func() {
		require.NoError(t, run([]string{"workspace"}))
	})

	var payload struct {
		WorkspaceID               string                 `json:"workspace_id"`
		WorkspaceName             string                 `json:"workspace_name"`
		LastUploadedSessionCursor *sessionUploadCursor   `json:"last_uploaded_session_cursor"`
		Items                     []response.ProjectResp `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "project-1", payload.WorkspaceID)
	require.Equal(t, sharedWorkspaceName, payload.WorkspaceName)
	require.NotNil(t, payload.LastUploadedSessionCursor)
	require.Equal(t, "session-456", payload.LastUploadedSessionCursor.SessionID)
	require.Len(t, payload.Items, 1)
	require.Equal(t, "project-1", payload.Items[0].ID)
}

func TestRunSessionsIncludesLastUploadedSessionCursor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveNoopSkillSetBundleRequest(t, w, r) {
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/session-summaries":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionSummaryListResp{
					Items: []response.SessionSummaryItem{{
						ID:        "session-1",
						ProjectID: "project-1",
						Tool:      "codex",
						Timestamp: time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC),
					}},
				}),
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
		LastUploadedSessionCursor: &sessionUploadCursor{
			TailPath:    filepath.Join(root, ".codex", "sessions", "2026", "03", "14", "latest.jsonl"),
			TailModTime: time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC),
			TailSize:    789,
			SessionID:   "session-789",
		},
	}))

	output := captureStdout(t, func() {
		require.NoError(t, run([]string{"sessions", "--limit", "1"}))
	})

	var payload struct {
		WorkspaceID               string                        `json:"workspace_id"`
		WorkspaceName             string                        `json:"workspace_name"`
		LastUploadedSessionCursor *sessionUploadCursor          `json:"last_uploaded_session_cursor"`
		Items                     []response.SessionSummaryItem `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "project-1", payload.WorkspaceID)
	require.Equal(t, sharedWorkspaceName, payload.WorkspaceName)
	require.NotNil(t, payload.LastUploadedSessionCursor)
	require.Equal(t, "session-789", payload.LastUploadedSessionCursor.SessionID)
	require.Len(t, payload.Items, 1)
	require.Equal(t, "session-1", payload.Items[0].ID)
}
