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

	"github.com/liushuangls/go-server-template/dto/request"
	"github.com/liushuangls/go-server-template/dto/response"
)

func TestReadOptionalJSONMapMissingFile(t *testing.T) {
	var out map[string]any
	exists, err := readOptionalJSONMap(filepath.Join(t.TempDir(), "missing.json"), &out)
	require.NoError(t, err)
	require.False(t, exists)
	require.Empty(t, out)
}

func TestApplyBackupRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	backup := applyBackup{
		ApplyID:   "apply-1",
		ProjectID: "project-1",
		Files: []applyFileBackup{{
			FilePath:       "/tmp/config.json",
			FileKind:       "json_merge",
			OriginalExists: true,
			OriginalJSON: map[string]any{
				"baseline": true,
			},
		}},
	}

	require.NoError(t, saveApplyBackup(backup))

	loaded, err := loadApplyBackup("apply-1")
	require.NoError(t, err)
	require.Equal(t, backup.ApplyID, loaded.ApplyID)
	require.Len(t, loaded.Files, 1)
	require.Equal(t, backup.Files[0].FilePath, loaded.Files[0].FilePath)
	require.Equal(t, backup.Files[0].OriginalJSON["baseline"], loaded.Files[0].OriginalJSON["baseline"])

	require.NoError(t, deleteApplyBackup("apply-1"))

	_, err = loadApplyBackup("apply-1")
	require.Error(t, err)

	_, statErr := os.Stat(filepath.Join(root, "applies", "apply-1.json"))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}

func TestAPIClientAddsTokenHeader(t *testing.T) {
	client := newAPIClient("http://example.com", "test-token")
	require.Equal(t, "test-token", client.token)
}

func TestRunSessionUploadsTokenUsageAndRawQueries(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	sessionFile := filepath.Join(root, "session.json")
	err := os.WriteFile(sessionFile, []byte(`{
  "token_in": 1440,
  "token_out": 320,
  "raw_queries": [
    "Inspect the auth middleware and summarize the current token validation flow.",
    "Recommend the smallest patch that fixes the failing analytics test."
  ]
}`), 0o644)
	require.NoError(t, err)

	require.NoError(t, saveState(state{
		ServerURL: "http://127.0.0.1:8082",
		APIToken:  "token-session",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	}))

	var uploaded request.SessionSummaryReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/session-summaries", r.URL.Path)
		require.Equal(t, "token-session", r.Header.Get("X-AgentOpt-Token"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&uploaded))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.SessionIngestResp{
				SessionID:           "session-1",
				ProjectID:           "project-1",
				RecommendationCount: 0,
			}),
		}))
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL: server.URL,
		APIToken:  "token-session",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	}))

	err = runSession([]string{"--file", sessionFile, "--tool", "codex"})
	require.NoError(t, err)

	require.Equal(t, "project-1", uploaded.ProjectID)
	require.Equal(t, "codex", uploaded.Tool)
	require.Equal(t, 1440, uploaded.TokenIn)
	require.Equal(t, 320, uploaded.TokenOut)
	require.Len(t, uploaded.RawQueries, 2)
	require.False(t, uploaded.Timestamp.IsZero())
}

func TestRunSessionCollectsLatestCodexSessionFromLocalFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	oldSession := filepath.Join(codexHome, "sessions", "2026", "03", "08", "old.jsonl")
	newSession := filepath.Join(codexHome, "sessions", "2026", "03", "09", "new.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(oldSession), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(newSession), 0o755))

	require.NoError(t, os.WriteFile(oldSession, []byte("{\"timestamp\":\"2026-03-08T09:00:00Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"user_message\",\"message\":\"obsolete prompt\"}}\n"), 0o644))
	require.NoError(t, os.WriteFile(newSession, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-09T09:00:00Z","type":"session_meta","payload":{"id":"codex-session-123","timestamp":"2026-03-09T09:00:00Z"}}`,
		`{"timestamp":"2026-03-09T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/tmp/demo</cwd>\n</environment_context>"}]}}`,
		`{"timestamp":"2026-03-09T09:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"# Context from my IDE setup:\n\n## My request for Codex:\nInspect the analytics route and summarize the current control flow.\n"}}`,
		`{"timestamp":"2026-03-09T09:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2100,"output_tokens":480,"total_tokens":2580}}}}`,
		`{"timestamp":"2026-03-09T09:00:04Z","type":"event_msg","payload":{"type":"user_message","message":"List the exact tests to run after the patch."}}`,
	}, "\n")+"\n"), 0o644))

	oldTime := time.Date(2026, 3, 8, 9, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 3, 9, 9, 0, 4, 0, time.UTC)
	require.NoError(t, os.Chtimes(oldSession, oldTime, oldTime))
	require.NoError(t, os.Chtimes(newSession, newTime, newTime))

	var uploaded request.SessionSummaryReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/session-summaries", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&uploaded))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.SessionIngestResp{
				SessionID:           "session-collector",
				ProjectID:           "project-1",
				RecommendationCount: 1,
			}),
		}))
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL: server.URL,
		APIToken:  "token-session",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	}))

	err := runSession([]string{"--tool", "codex", "--codex-home", codexHome})
	require.NoError(t, err)

	require.Equal(t, "project-1", uploaded.ProjectID)
	require.Equal(t, "codex-session-123", uploaded.SessionID)
	require.Equal(t, "codex", uploaded.Tool)
	require.Equal(t, 2100, uploaded.TokenIn)
	require.Equal(t, 480, uploaded.TokenOut)
	require.Equal(t, []string{
		"Inspect the analytics route and summarize the current control flow.",
		"List the exact tests to run after the patch.",
	}, uploaded.RawQueries)
	require.Equal(t, newTime, uploaded.Timestamp)
}

func TestRunSessionUploadsRecentLocalCodexSessions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	sessionOld := filepath.Join(codexHome, "sessions", "2026", "03", "07", "old.jsonl")
	sessionMid := filepath.Join(codexHome, "sessions", "2026", "03", "08", "mid.jsonl")
	sessionNew := filepath.Join(codexHome, "sessions", "2026", "03", "09", "new.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sessionOld), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(sessionMid), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(sessionNew), 0o755))

	require.NoError(t, os.WriteFile(sessionOld, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-07T09:00:00Z","type":"session_meta","payload":{"id":"codex-session-old","timestamp":"2026-03-07T09:00:00Z"}}`,
		`{"timestamp":"2026-03-07T09:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"obsolete prompt"}}`,
		`{"timestamp":"2026-03-07T09:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}}`,
	}, "\n")+"\n"), 0o644))
	require.NoError(t, os.WriteFile(sessionMid, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-08T09:00:00Z","type":"session_meta","payload":{"id":"codex-session-mid","timestamp":"2026-03-08T09:00:00Z"}}`,
		`{"timestamp":"2026-03-08T09:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Inspect the analytics route before editing."}}`,
		`{"timestamp":"2026-03-08T09:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":800,"output_tokens":160,"total_tokens":960}}}}`,
	}, "\n")+"\n"), 0o644))
	require.NoError(t, os.WriteFile(sessionNew, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-09T09:00:00Z","type":"session_meta","payload":{"id":"codex-session-new","timestamp":"2026-03-09T09:00:00Z"}}`,
		`{"timestamp":"2026-03-09T09:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"List the exact tests to run after the patch."}}`,
		`{"timestamp":"2026-03-09T09:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1200,"output_tokens":240,"total_tokens":1440}}}}`,
	}, "\n")+"\n"), 0o644))

	oldTime := time.Date(2026, 3, 7, 9, 0, 2, 0, time.UTC)
	midTime := time.Date(2026, 3, 8, 9, 0, 2, 0, time.UTC)
	newTime := time.Date(2026, 3, 9, 9, 0, 2, 0, time.UTC)
	require.NoError(t, os.Chtimes(sessionOld, oldTime, oldTime))
	require.NoError(t, os.Chtimes(sessionMid, midTime, midTime))
	require.NoError(t, os.Chtimes(sessionNew, newTime, newTime))

	uploaded := make([]request.SessionSummaryReq, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/session-summaries", r.URL.Path)

		var req request.SessionSummaryReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		uploaded = append(uploaded, req)

		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.SessionIngestResp{
				SessionID:           req.SessionID,
				ProjectID:           req.ProjectID,
				RecommendationCount: len(uploaded),
			}),
		}))
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL: server.URL,
		APIToken:  "token-session",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	}))

	err := runSession([]string{"--tool", "codex", "--codex-home", codexHome, "--recent", "2"})
	require.NoError(t, err)

	require.Len(t, uploaded, 2)
	require.Equal(t, "codex-session-mid", uploaded[0].SessionID)
	require.Equal(t, "codex-session-new", uploaded[1].SessionID)
	require.Equal(t, "project-1", uploaded[0].ProjectID)
	require.Equal(t, "project-1", uploaded[1].ProjectID)
	require.Equal(t, []string{"Inspect the analytics route before editing."}, uploaded[0].RawQueries)
	require.Equal(t, []string{"List the exact tests to run after the patch."}, uploaded[1].RawQueries)
	require.Equal(t, 800, uploaded[0].TokenIn)
	require.Equal(t, 1200, uploaded[1].TokenIn)
}

func TestExecuteLocalApplyCreatesBackupAndWritesConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	target := filepath.Join(root, "config.json")
	err := os.WriteFile(target, []byte("{\"baseline\":true}\n"), 0o644)
	require.NoError(t, err)

	result, err := executeLocalApply(state{ProjectID: "project-1"}, "apply-1", []response.PatchPreviewItem{
		{
			FilePath: target,
			SettingsUpdates: map[string]any{
				"shell_profile": "safe",
			},
		},
	}, "")
	require.NoError(t, err)
	require.Equal(t, target, result.FilePath)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Contains(t, string(data), "\"baseline\": true")
	require.Contains(t, string(data), "\"shell_profile\": \"safe\"")

	backup, err := loadApplyBackup("apply-1")
	require.NoError(t, err)
	require.Len(t, backup.Files, 1)
	require.True(t, backup.Files[0].OriginalExists)
	require.Equal(t, true, backup.Files[0].OriginalJSON["baseline"])
}

func TestExecuteLocalApplyAppendsTextFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	target := filepath.Join(root, "AGENTS.md")
	err := os.WriteFile(target, []byte("# Existing\n"), 0o644)
	require.NoError(t, err)

	result, err := executeLocalApply(state{ProjectID: "project-1"}, "apply-text", []response.PatchPreviewItem{
		{
			FilePath:       "AGENTS.md",
			Operation:      "append_block",
			ContentPreview: "\n## AgentOpt\n- safe rollout\n",
		},
	}, target)
	require.NoError(t, err)
	require.Equal(t, target, result.FilePath)
	require.Contains(t, result.AppliedText, "AgentOpt")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Contains(t, string(data), "# Existing")
	require.Contains(t, string(data), "safe rollout")
}

func TestPreflightLocalApplyRejectsUnsafeTarget(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	result, err := preflightLocalApply(state{ProjectID: "project-1"}, "apply-unsafe", []response.PatchPreviewItem{
		{
			FilePath:        ".ssh/config",
			Operation:       "merge_patch",
			SettingsUpdates: map[string]any{"unsafe": true},
		},
	}, "")
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Len(t, result.Steps, 1)
	require.Equal(t, "file_scope", result.Steps[0].Guard)
}

func TestPreflightLocalApplyAllowsMultipleDistinctSteps(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	result, err := preflightLocalApply(state{ProjectID: "project-1"}, "apply-multi", []response.PatchPreviewItem{
		{FilePath: ".codex/config.json", Operation: "merge_patch"},
		{FilePath: "AGENTS.md", Operation: "append_block"},
	}, filepath.Join(root, "config.json")+","+filepath.Join(root, "AGENTS.md"))
	require.NoError(t, err)
	require.True(t, result.Allowed)
	require.Len(t, result.Steps, 2)
	require.True(t, result.Steps[0].Allowed)
	require.True(t, result.Steps[1].Allowed)
}

func TestPreflightLocalApplyRejectsDuplicateTargets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	result, err := preflightLocalApply(state{ProjectID: "project-1"}, "apply-dup", []response.PatchPreviewItem{
		{FilePath: ".codex/config.json", Operation: "merge_patch"},
		{FilePath: ".codex/config.json", Operation: "merge_patch"},
	}, "")
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Equal(t, "duplicate_target", result.Steps[1].Guard)
}

func TestExecuteLocalApplySupportsMultipleSteps(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	configTarget := filepath.Join(root, "config.json")
	textTarget := filepath.Join(root, "AGENTS.md")
	err := os.WriteFile(configTarget, []byte("{\"baseline\":true}\n"), 0o644)
	require.NoError(t, err)

	result, err := executeLocalApply(state{ProjectID: "project-1"}, "apply-multi", []response.PatchPreviewItem{
		{
			FilePath:  ".codex/config.json",
			Operation: "merge_patch",
			SettingsUpdates: map[string]any{
				"shell_profile": "safe",
			},
		},
		{
			FilePath:       "AGENTS.md",
			Operation:      "append_block",
			ContentPreview: "\n## AgentOpt\n- rollout\n",
		},
	}, configTarget+","+textTarget)
	require.NoError(t, err)
	require.Len(t, result.FilePaths, 2)
	require.Contains(t, result.FilePath, configTarget)
	require.Contains(t, result.FilePath, textTarget)

	configData, err := os.ReadFile(configTarget)
	require.NoError(t, err)
	require.Contains(t, string(configData), "\"shell_profile\": \"safe\"")

	textData, err := os.ReadFile(textTarget)
	require.NoError(t, err)
	require.Contains(t, string(textData), "AgentOpt")

	backup, err := loadApplyBackup("apply-multi")
	require.NoError(t, err)
	require.Len(t, backup.Files, 2)
}

func TestRunSyncRejectsInvalidIntervalInWatchMode(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	err := saveState(state{
		ServerURL: "http://127.0.0.1:8082",
		APIToken:  "token",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	})
	require.NoError(t, err)

	err = runSync([]string{"--watch", "--interval", "0s"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "greater than zero")
}

func TestRunSyncOnceAppliesPendingPlansAndReportsSuccess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	configTarget := filepath.Join(root, "config.json")
	textTarget := filepath.Join(root, "AGENTS.md")
	require.NoError(t, os.WriteFile(configTarget, []byte("{\"baseline\":true}\n"), 0o644))
	require.NoError(t, os.WriteFile(textTarget, []byte("# Existing\n"), 0o644))

	var reported request.ApplyResultReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-sync", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/applies/pending":
			require.Equal(t, "project-1", r.URL.Query().Get("project_id"))
			require.Equal(t, "user-1", r.URL.Query().Get("user_id"))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.PendingApplyResp{
					Items: []response.PendingApplyItem{{
						ApplyID: "apply-sync-1",
						PatchPreview: []response.PatchPreviewItem{
							{
								FilePath:  ".codex/config.json",
								Operation: "merge_patch",
								SettingsUpdates: map[string]any{
									"shell_profile": "safe",
								},
							},
							{
								FilePath:       "AGENTS.md",
								Operation:      "append_block",
								ContentPreview: "\n## AgentOpt\n- synced\n",
							},
						},
					}},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID: "apply-sync-1",
					Status:  "applied",
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	st := state{
		ServerURL: server.URL,
		APIToken:  "token-sync",
		UserID:    "user-1",
		ProjectID: "project-1",
	}

	err := runSyncOnce(st, newAPIClient(server.URL, "token-sync"), configTarget+","+textTarget)
	require.NoError(t, err)

	configData, err := os.ReadFile(configTarget)
	require.NoError(t, err)
	require.Contains(t, string(configData), "\"shell_profile\": \"safe\"")

	textData, err := os.ReadFile(textTarget)
	require.NoError(t, err)
	require.Contains(t, string(textData), "synced")

	require.Equal(t, "apply-sync-1", reported.ApplyID)
	require.True(t, reported.Success)
	require.Contains(t, reported.AppliedFile, configTarget)
	require.Contains(t, reported.AppliedFile, textTarget)

	backup, err := loadApplyBackup("apply-sync-1")
	require.NoError(t, err)
	require.Len(t, backup.Files, 2)
}

func TestRunSyncOnceReportsFailuresAndContinues(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	validTarget := filepath.Join(root, "config.json")
	require.NoError(t, os.WriteFile(validTarget, []byte("{\"baseline\":true}\n"), 0o644))

	reports := make([]request.ApplyResultReq, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-sync-fail", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/applies/pending":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.PendingApplyResp{
					Items: []response.PendingApplyItem{
						{
							ApplyID: "apply-sync-fail",
							PatchPreview: []response.PatchPreviewItem{{
								FilePath:  ".ssh/config",
								Operation: "merge_patch",
								SettingsUpdates: map[string]any{
									"unsafe": true,
								},
							}},
						},
						{
							ApplyID: "apply-sync-ok",
							PatchPreview: []response.PatchPreviewItem{{
								FilePath:  validTarget,
								Operation: "merge_patch",
								SettingsUpdates: map[string]any{
									"shell_profile": "safe",
								},
							}},
						},
					},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			var reported request.ApplyResultReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			reports = append(reports, reported)
			status := "failed"
			if reported.Success {
				status = "applied"
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID: reported.ApplyID,
					Status:  status,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	st := state{
		ServerURL: server.URL,
		APIToken:  "token-sync-fail",
		UserID:    "user-1",
		ProjectID: "project-1",
	}

	err := runSyncOnce(st, newAPIClient(server.URL, "token-sync-fail"), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "apply-sync-fail")

	require.Len(t, reports, 2)
	require.Equal(t, "apply-sync-fail", reports[0].ApplyID)
	require.False(t, reports[0].Success)
	require.Contains(t, reports[0].Note, "local apply failed during sync")

	require.Equal(t, "apply-sync-ok", reports[1].ApplyID)
	require.True(t, reports[1].Success)
	require.Equal(t, "safe", reports[1].AppliedSettings["shell_profile"])

	validData, err := os.ReadFile(validTarget)
	require.NoError(t, err)
	require.Contains(t, string(validData), "\"shell_profile\": \"safe\"")
}

func TestRunRollbackRestoresFilesDeletesCreatedOnesAndReportsResult(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	var reported request.ApplyResultReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/applies/result", r.URL.Path)
		require.Equal(t, "token-1", r.Header.Get("X-AgentOpt-Token"))

		require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.ApplyResultResp{
				ApplyID:    reported.ApplyID,
				Status:     "rollback_confirmed",
				RolledBack: true,
			}),
		}))
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL: server.URL,
		APIToken:  "token-1",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	}))

	configTarget := filepath.Join(root, "config.json")
	textTarget := filepath.Join(root, "AGENTS.md")
	createdTarget := filepath.Join(root, ".mcp.json")

	require.NoError(t, os.WriteFile(configTarget, []byte("{\"shell_profile\":\"safe\"}\n"), 0o644))
	require.NoError(t, os.WriteFile(textTarget, []byte("# Existing\n\n## AgentOpt\n- rollout\n"), 0o644))
	require.NoError(t, os.WriteFile(createdTarget, []byte("{\"new\":true}\n"), 0o644))

	require.NoError(t, saveApplyBackup(applyBackup{
		ApplyID:   "apply-rollback",
		ProjectID: "project-1",
		Files: []applyFileBackup{
			{
				FilePath:       configTarget,
				FileKind:       "json_merge",
				OriginalExists: true,
				OriginalJSON: map[string]any{
					"baseline": true,
				},
			},
			{
				FilePath:       textTarget,
				FileKind:       "text_append",
				OriginalExists: true,
				OriginalText:   "# Existing\n",
			},
			{
				FilePath:       createdTarget,
				FileKind:       "json_merge",
				OriginalExists: false,
			},
		},
	}))

	err := runRollback([]string{"--apply-id", "apply-rollback", "--note", "rollback from test"})
	require.NoError(t, err)

	configData, err := os.ReadFile(configTarget)
	require.NoError(t, err)
	require.Contains(t, string(configData), "\"baseline\": true")
	require.NotContains(t, string(configData), "\"shell_profile\"")

	textData, err := os.ReadFile(textTarget)
	require.NoError(t, err)
	require.Equal(t, "# Existing\n", string(textData))

	_, err = os.Stat(createdTarget)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))

	_, err = loadApplyBackup("apply-rollback")
	require.Error(t, err)

	require.Equal(t, "apply-rollback", reported.ApplyID)
	require.True(t, reported.Success)
	require.True(t, reported.RolledBack)
	require.Equal(t, "rollback from test", reported.Note)
	require.Contains(t, reported.AppliedFile, configTarget)
	require.Contains(t, reported.AppliedFile, textTarget)
	require.Contains(t, reported.AppliedFile, createdTarget)

	restoredConfig, ok := reported.AppliedSettings[configTarget].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, restoredConfig["baseline"])
}

func TestRunApplyYesReviewsPlanBeforeLocalApply(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	configTarget := filepath.Join(root, "config.json")
	require.NoError(t, os.WriteFile(configTarget, []byte("{\"baseline\":true}\n"), 0o644))

	var (
		reviewCalled bool
		reported     request.ApplyResultReq
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-apply", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/recommendations/apply":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request.ApplyRecommendationReq{}))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyPlanResp{
					ApplyID:        "apply-cli-review",
					Status:         "awaiting_review",
					PolicyMode:     "requires_review",
					ApprovalStatus: "awaiting_review",
					PatchPreview: []response.PatchPreviewItem{{
						FilePath:  ".codex/config.json",
						Operation: "merge_patch",
						SettingsUpdates: map[string]any{
							"shell_profile": "safe",
						},
					}},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/change-plans/review":
			reviewCalled = true
			var reviewReq request.ReviewChangePlanReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reviewReq))
			require.Equal(t, "apply-cli-review", reviewReq.ApplyID)
			require.Equal(t, "approve", reviewReq.Decision)
			require.Equal(t, "user-1", reviewReq.ReviewedBy)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ChangePlanReviewResp{
					ApplyID:        "apply-cli-review",
					Status:         "approved_for_local_apply",
					ApprovalStatus: "approved",
					Decision:       "approved",
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID: "apply-cli-review",
					Status:  "applied",
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL: server.URL,
		APIToken:  "token-apply",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	}))

	err := runApply([]string{"--recommendation-id", "rec-1", "--yes", "--target-config", configTarget})
	require.NoError(t, err)
	require.True(t, reviewCalled)

	configData, err := os.ReadFile(configTarget)
	require.NoError(t, err)
	require.Contains(t, string(configData), "\"shell_profile\": \"safe\"")

	require.Equal(t, "apply-cli-review", reported.ApplyID)
	require.True(t, reported.Success)
	require.Equal(t, configTarget, reported.AppliedFile)
	require.Equal(t, "safe", reported.AppliedSettings["shell_profile"])

	backup, err := loadApplyBackup("apply-cli-review")
	require.NoError(t, err)
	require.Len(t, backup.Files, 1)
}

func TestRunApplyYesSkipsReviewForAutoApprovedPlan(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	configTarget := filepath.Join(root, "config.json")
	require.NoError(t, os.WriteFile(configTarget, []byte("{\"baseline\":true}\n"), 0o644))

	var reported request.ApplyResultReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-auto", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/recommendations/apply":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyPlanResp{
					ApplyID:        "apply-cli-auto",
					Status:         "approved_for_local_apply",
					PolicyMode:     "auto_approved",
					ApprovalStatus: "approved",
					Decision:       "auto_approved",
					PatchPreview: []response.PatchPreviewItem{{
						FilePath:  ".codex/config.json",
						Operation: "merge_patch",
						SettingsUpdates: map[string]any{
							"shell_profile": "safe",
						},
					}},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/change-plans/review":
			t.Fatalf("review endpoint should not be called for auto-approved plans")
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID: "apply-cli-auto",
					Status:  "applied",
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL: server.URL,
		APIToken:  "token-auto",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	}))

	err := runApply([]string{"--recommendation-id", "rec-auto", "--yes", "--target-config", configTarget})
	require.NoError(t, err)

	configData, err := os.ReadFile(configTarget)
	require.NoError(t, err)
	require.Contains(t, string(configData), "\"shell_profile\": \"safe\"")

	require.Equal(t, "apply-cli-auto", reported.ApplyID)
	require.True(t, reported.Success)
	require.Equal(t, configTarget, reported.AppliedFile)
}

func TestRunApplyYesReportsFailureWhenLocalApplyFails(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	var reported request.ApplyResultReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-fail", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/recommendations/apply":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyPlanResp{
					ApplyID:        "apply-cli-fail",
					Status:         "approved_for_local_apply",
					PolicyMode:     "auto_approved",
					ApprovalStatus: "approved",
					Decision:       "auto_approved",
					PatchPreview: []response.PatchPreviewItem{{
						FilePath:  ".ssh/config",
						Operation: "merge_patch",
						SettingsUpdates: map[string]any{
							"unsafe": true,
						},
					}},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/change-plans/review":
			t.Fatalf("review endpoint should not be called for auto-approved failure case")
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID: "apply-cli-fail",
					Status:  "failed",
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL: server.URL,
		APIToken:  "token-fail",
		OrgID:     "org-1",
		UserID:    "user-1",
		ProjectID: "project-1",
	}))

	err := runApply([]string{"--recommendation-id", "rec-fail", "--yes"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "local guard rejected apply")

	require.Equal(t, "apply-cli-fail", reported.ApplyID)
	require.False(t, reported.Success)
	require.Contains(t, reported.Note, "local apply failed")
}

func mustJSONRawMessage(t *testing.T, value any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)
	return json.RawMessage(data)
}
