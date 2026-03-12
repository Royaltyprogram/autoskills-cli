package main

import (
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/Royaltyprogram/aiops/pkg/buildinfo"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()

	fn()

	require.NoError(t, writer.Close())
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	return strings.TrimSpace(string(output))
}

func useStubCodexRunner(t *testing.T, mode string) string {
	t.Helper()

	scriptPath := filepath.Join(t.TempDir(), "codex-runner-stub.mjs")
	script := `#!/usr/bin/env node
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import path from "node:path";

const requestPath = process.argv[2];
const request = JSON.parse(await readFile(requestPath, "utf8"));
const mode = process.env.AGENTOPT_STUB_MODE || "apply";
const changed = [];

if (process.env.AGENTOPT_STUB_REQUIRE_RESUME === "1" && !request.resume_thread_id) {
  process.stderr.write("missing resume_thread_id\n");
  process.exit(1);
}

for (const step of request.steps || []) {
  if (step.operation === "delete_file") {
    await rm(step.target_file, { force: true });
    changed.push(step.target_file);
    continue;
  }

  await mkdir(path.dirname(step.target_file), { recursive: true });
  if (step.operation === "append_block" || step.operation === "text_append") {
    let original = "";
    try {
      original = await readFile(step.target_file, "utf8");
    } catch {}
    let next = original;
    if (!next.includes(step.content_preview || "")) {
      next += step.content_preview || "";
    }
    await writeFile(step.target_file, next, "utf8");
  } else if (step.operation === "text_replace") {
    await writeFile(step.target_file, step.content_preview || "", "utf8");
  } else {
    let current = {};
    try {
      current = JSON.parse(await readFile(step.target_file, "utf8"));
    } catch {}
    Object.assign(current, step.settings_updates || {});
    await writeFile(step.target_file, JSON.stringify(current, null, 2) + "\n", "utf8");
  }
  changed.push(step.target_file);
}

if (mode === "extra_change") {
  const extra = path.join(request.working_directory, "UNAPPROVED.txt");
  await writeFile(extra, "unexpected\n", "utf8");
  changed.push(extra);
}

process.stdout.write(JSON.stringify({
  thread_id: request.resume_thread_id || ("thread-" + mode),
  status: mode === "blocked" ? "blocked" : "applied",
  summary: mode === "blocked" ? "stub blocked request" : "stub applied request",
  changed_files: changed,
  executed_commands: []
}));
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	t.Setenv("AGENTOPT_CODEX_RUNNER", scriptPath)
	t.Setenv("AGENTOPT_STUB_MODE", mode)
	return scriptPath
}

func useSleepingCodexRunner(t *testing.T, sleep time.Duration) string {
	t.Helper()

	scriptPath := filepath.Join(t.TempDir(), "codex-runner-sleep.mjs")
	script := `#!/usr/bin/env node
const sleepMs = Number(process.env.AGENTOPT_STUB_SLEEP_MS || "0");
await new Promise((resolve) => setTimeout(resolve, sleepMs));
process.stdout.write(JSON.stringify({
  status: "applied",
  summary: "slept",
  changed_files: [],
  executed_commands: []
}));
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	t.Setenv("AGENTOPT_CODEX_RUNNER", scriptPath)
	t.Setenv("AGENTOPT_STUB_SLEEP_MS", fmt.Sprintf("%d", sleep.Milliseconds()))
	return scriptPath
}

func TestReadOptionalJSONMapMissingFile(t *testing.T) {
	var out map[string]any
	exists, err := readOptionalJSONMap(filepath.Join(t.TempDir(), "missing.json"), &out)
	require.NoError(t, err)
	require.False(t, exists)
	require.Empty(t, out)
}

func TestResolveApplyTargetExpandsHomeInstructionPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	resolved, source, err := resolveApplyTarget("~/.codex/AGENTS.md", "")
	require.NoError(t, err)
	require.Equal(t, "preview", source)
	require.Equal(t, filepath.Join(home, ".codex", "AGENTS.md"), resolved)
}

func TestCodexApplyTimeoutUsesDefault(t *testing.T) {
	t.Setenv("AGENTOPT_CODEX_TIMEOUT", "")
	timeout, err := codexApplyTimeout()
	require.NoError(t, err)
	require.Equal(t, 10*time.Minute, timeout)
}

func TestCodexApplyTimeoutRejectsInvalidValue(t *testing.T) {
	t.Setenv("AGENTOPT_CODEX_TIMEOUT", "nope")
	_, err := codexApplyTimeout()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid AGENTOPT_CODEX_TIMEOUT")
}

func TestCodexApplyTimeoutRejectsNonPositiveValue(t *testing.T) {
	t.Setenv("AGENTOPT_CODEX_TIMEOUT", "0s")
	_, err := codexApplyTimeout()
	require.Error(t, err)
	require.Contains(t, err.Error(), "greater than zero")
}

func TestParseCodexReasoningEffortAllowsKnownValues(t *testing.T) {
	value, err := parseCodexReasoningEffort(" LOW ")
	require.NoError(t, err)
	require.Equal(t, "low", value)

	value, err = parseCodexReasoningEffort("")
	require.NoError(t, err)
	require.Empty(t, value)
}

func TestParseCodexReasoningEffortRejectsUnknownValues(t *testing.T) {
	_, err := parseCodexReasoningEffort("turbo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid Codex reasoning effort")
}

func TestNewCodexApplyRequestIncludesReasoningEffort(t *testing.T) {
	root := t.TempDir()
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	req, err := newCodexApplyRequest("apply-reasoning", preflightResult{
		ApplyID: "apply-reasoning",
		Allowed: true,
		Steps: []preflightStep{{
			TargetFile: filepath.Join(root, "AGENTS.md"),
			Allowed:    true,
		}},
	}, []response.PatchPreviewItem{{
		FilePath:       "AGENTS.md",
		Operation:      "append_block",
		ContentPreview: "\n## AgentOpt\n",
	}}, "low")
	require.NoError(t, err)
	require.Equal(t, "low", req.ModelReasoningEffort)
}

func TestApplyBackupRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	backup := applyBackup{
		ApplyID:     "apply-1",
		WorkspaceID: "workspace-1",
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
	require.Equal(t, backup.WorkspaceID, loaded.WorkspaceID)
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

func TestLoadStateAcceptsLegacyProjectID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	require.NoError(t, os.WriteFile(filepath.Join(root, "state.json"), []byte(`{
  "server_url": "http://127.0.0.1:8082",
  "api_token": "token-1",
  "org_id": "org-1",
  "user_id": "user-1",
  "agent_id": "agent-1",
  "workspace_id": "",
  "project_id": "legacy-project-1"
}
`), 0o644))

	st, err := loadState()
	require.NoError(t, err)
	require.Equal(t, "legacy-project-1", st.WorkspaceID)
}

func TestLoadWorkspaceStateSelectsConnectedProjectByCurrentDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	repoA := filepath.Join(root, "repo-a")
	repoB := filepath.Join(root, "repo-b")
	require.NoError(t, os.MkdirAll(filepath.Join(repoA, "cmd"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repoB, "pkg"), 0o755))

	require.NoError(t, saveState(state{
		ServerURL:     "http://127.0.0.1:8082",
		APIToken:      "token-1",
		OrgID:         "org-1",
		UserID:        "user-1",
		AgentID:       "agent-1",
		WorkspaceID:   "project-a",
		WorkspaceName: "repo-a",
		RepoPath:      repoA,
		Workspaces: map[string]connectedWorkspace{
			"project-a": {ID: "project-a", Name: "repo-a", RepoPath: repoA},
			"project-b": {ID: "project-b", Name: "repo-b", RepoPath: repoB},
		},
	}))

	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(filepath.Join(repoB, "pkg")))
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, canonicalizePath(filepath.Join(repoB, "pkg")), canonicalizePath(cwd))

	loaded, err := loadState()
	require.NoError(t, err)
	matched, ok := loaded.matchWorkspaceByDir(cwd)
	require.True(t, ok)
	require.Equal(t, "project-b", matched.ID)

	st, err := loadWorkspaceState()
	require.NoError(t, err)
	require.Equal(t, "project-b", st.WorkspaceID)
	require.Equal(t, "repo-b", st.workspaceName())
	require.Equal(t, canonicalizePath(repoB), canonicalizePath(st.repoPath()))
}

func TestLoadApplyBackupAcceptsLegacyProjectID(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "applies"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "applies", "apply-legacy.json"), []byte(`{
  "apply_id": "apply-legacy",
  "project_id": "legacy-project-1",
  "files": [
    {
      "file_path": "/tmp/config.json",
      "file_kind": "json_merge",
      "original_exists": true,
      "original_json": {
        "baseline": true
      }
    }
  ]
}
`), 0o644))

	backup, err := loadApplyBackup("apply-legacy")
	require.NoError(t, err)
	require.Equal(t, "legacy-project-1", backup.WorkspaceID)
	require.Len(t, backup.Files, 1)
}

func TestAPIClientAddsTokenHeader(t *testing.T) {
	client := newAPIClient("http://example.com", "test-token")
	require.Equal(t, "test-token", client.token)
	require.Equal(t, defaultAPIClientTimeout, client.http.Timeout)
}

func TestRunVersionPrintsBuildMetadata(t *testing.T) {
	originalVersion := buildinfo.Version
	originalCommit := buildinfo.Commit
	originalDate := buildinfo.Date
	buildinfo.Version = "1.2.3-beta.1"
	buildinfo.Commit = "abc1234"
	buildinfo.Date = "2026-03-09T14:00:00Z"
	t.Cleanup(func() {
		buildinfo.Version = originalVersion
		buildinfo.Commit = originalCommit
		buildinfo.Date = originalDate
	})

	output := captureStdout(t, func() {
		require.NoError(t, run([]string{"version"}))
	})

	require.Equal(t, "agentopt 1.2.3-beta.1 abc1234 2026-03-09T14:00:00Z", output)
}

func TestRunLoginAuthenticatesWithIssuedCLIToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	var uploaded request.CLILoginReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/auth/cli/login", r.URL.Path)
		require.Equal(t, "issued-cli-token", r.Header.Get("X-AgentOpt-Token"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&uploaded))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.CLILoginResp{
				AgentID:      "device-123",
				DeviceID:     "device-123",
				OrgID:        "demo-org",
				OrgName:      "Demo Org",
				UserID:       "demo-user",
				UserName:     "Demo Operator",
				UserEmail:    "demo@example.com",
				Status:       "registered",
				RegisteredAt: time.Now().UTC(),
			}),
		}))
	}))
	defer server.Close()

	err := runLogin([]string{
		"--server", server.URL,
		"--token", "issued-cli-token",
		"--device", "work-mac",
		"--hostname", "work-mac.local",
		"--platform", "darwin/arm64",
		"--tools", "codex",
	})
	require.NoError(t, err)

	require.Equal(t, "work-mac", uploaded.DeviceName)
	require.Equal(t, "work-mac.local", uploaded.Hostname)
	require.Equal(t, "darwin/arm64", uploaded.Platform)
	require.Equal(t, []string{"codex"}, uploaded.Tools)

	st, err := loadState()
	require.NoError(t, err)
	require.Equal(t, server.URL, st.ServerURL)
	require.Equal(t, "issued-cli-token", st.APIToken)
	require.Equal(t, "demo-org", st.OrgID)
	require.Equal(t, "demo-user", st.UserID)
	require.Equal(t, "device-123", st.AgentID)
}

func TestRunLoginUsesBuildVersionByDefault(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	originalVersion := buildinfo.Version
	buildinfo.Version = "1.2.3-beta.2"
	t.Cleanup(func() {
		buildinfo.Version = originalVersion
	})

	var uploaded request.CLILoginReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/auth/cli/login", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&uploaded))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.CLILoginResp{
				AgentID:      "device-123",
				DeviceID:     "device-123",
				OrgID:        "demo-org",
				OrgName:      "Demo Org",
				UserID:       "demo-user",
				UserName:     "Demo Operator",
				UserEmail:    "demo@example.com",
				Status:       "registered",
				RegisteredAt: time.Now().UTC(),
			}),
		}))
	}))
	defer server.Close()

	err := runLogin([]string{
		"--server", server.URL,
		"--token", "issued-cli-token",
		"--device", "work-mac",
	})
	require.NoError(t, err)
	require.Equal(t, "1.2.3-beta.2", uploaded.CLIVersion)
}

func TestRunWorkspaceShowsSharedWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/projects", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.ProjectListResp{
				Items: []response.ProjectResp{
					{ID: "project-1", Name: "Shared workspace"},
				},
			}),
		}))
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-projects",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runWorkspace(nil))
	})

	var payload response.ProjectListResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Len(t, payload.Items, 1)
	require.Equal(t, "project-1", payload.Items[0].ID)
	require.Equal(t, "Shared workspace", payload.Items[0].Name)
}

func TestRunPendingIncludesWorkspaceContext(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/applies/pending", r.URL.Path)
		require.Equal(t, "project-1", r.URL.Query().Get("project_id"))
		require.Equal(t, "user-1", r.URL.Query().Get("user_id"))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.PendingApplyResp{
				Items: []response.PendingApplyItem{{
					ApplyID: "apply-1",
					Status:  "approved_for_local_apply",
				}},
			}),
		}))
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		APIToken:    "token-projects",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runPending(nil))
	})

	var payload struct {
		WorkspaceID   string `json:"workspace_id"`
		WorkspaceName string `json:"workspace_name"`
		Items         []struct {
			ApplyID string `json:"apply_id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, "project-1", payload.WorkspaceID)
	require.Equal(t, defaultWorkspaceName, payload.WorkspaceName)
	require.Len(t, payload.Items, 1)
	require.Equal(t, "apply-1", payload.Items[0].ApplyID)
}

func TestRunSessionUploadsTokenUsageAndRawQueries(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	sessionFile := filepath.Join(root, "session.json")
	err := os.WriteFile(sessionFile, []byte(`{
  "token_in": 1440,
  "token_out": 320,
  "cached_input_tokens": 420,
  "reasoning_output_tokens": 75,
  "function_call_count": 2,
  "tool_error_count": 1,
  "session_duration_ms": 64000,
  "tool_wall_time_ms": 2400,
  "tool_calls": {
    "shell": 1,
    "read_file": 1
  },
  "tool_errors": {
    "shell": 1
  },
  "tool_wall_times_ms": {
    "shell": 1700,
    "read_file": 700
  },
  "raw_queries": [
    "Inspect the auth middleware and summarize the current token validation flow.",
    "Recommend the smallest patch that fixes the failing analytics test."
  ],
  "models": ["gpt-5.4"],
  "model_provider": "openai",
  "first_response_latency_ms": 1850,
  "assistant_responses": [
    "The auth middleware validates the shared API token before routing the request."
  ]
}`), 0o644)
	require.NoError(t, err)

	require.NoError(t, saveState(state{
		ServerURL:   "http://127.0.0.1:8082",
		APIToken:    "token-session",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
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
		ServerURL:   server.URL,
		APIToken:    "token-session",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	err = runSession([]string{"--file", sessionFile, "--tool", "codex"})
	require.NoError(t, err)

	require.Equal(t, "project-1", uploaded.ProjectID)
	require.Equal(t, "codex", uploaded.Tool)
	require.Equal(t, 1440, uploaded.TokenIn)
	require.Equal(t, 320, uploaded.TokenOut)
	require.Equal(t, 420, uploaded.CachedInputTokens)
	require.Equal(t, 75, uploaded.ReasoningOutputTokens)
	require.Equal(t, 2, uploaded.FunctionCallCount)
	require.Equal(t, 1, uploaded.ToolErrorCount)
	require.Equal(t, 64000, uploaded.SessionDurationMS)
	require.Equal(t, 2400, uploaded.ToolWallTimeMS)
	require.Equal(t, map[string]int{"shell": 1, "read_file": 1}, uploaded.ToolCalls)
	require.Equal(t, map[string]int{"shell": 1}, uploaded.ToolErrors)
	require.Equal(t, map[string]int{"shell": 1700, "read_file": 700}, uploaded.ToolWallTimesMS)
	require.Len(t, uploaded.RawQueries, 2)
	require.Equal(t, []string{"gpt-5.4"}, uploaded.Models)
	require.Equal(t, "openai", uploaded.ModelProvider)
	require.Equal(t, 1850, uploaded.FirstResponseLatencyMS)
	require.Equal(t, []string{"The auth middleware validates the shared API token before routing the request."}, uploaded.AssistantResponses)
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
		`{"timestamp":"2026-03-09T09:00:00Z","type":"session_meta","payload":{"id":"codex-session-123","timestamp":"2026-03-09T09:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-09T09:00:00Z","type":"turn_context","payload":{"model":"gpt-5.4"}}`,
		`{"timestamp":"2026-03-09T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /tmp/demo\n\n<INSTRUCTIONS>\nOnly follow safe steps.\n</INSTRUCTIONS>"}]}}`,
		`{"timestamp":"2026-03-09T09:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/tmp/demo</cwd>\n</environment_context>"}]}}`,
		`{"timestamp":"2026-03-09T09:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"# Context from my IDE setup:\n\n## My request for Codex:\nInspect the analytics route and summarize the current control flow.\n"}}`,
		`{"timestamp":"2026-03-09T09:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2100,"cached_input_tokens":700,"output_tokens":480,"reasoning_output_tokens":120,"total_tokens":2580}}}}`,
		`{"timestamp":"2026-03-09T09:00:03.500Z","type":"response_item","payload":{"type":"function_call","call_id":"call-analytics","name":"shell","arguments":"{\"cmd\":\"go test ./routes/controller\"}"}}`,
		`{"timestamp":"2026-03-09T09:00:03.550Z","type":"response_item","payload":{"type":"function_call","call_id":"call-read","name":"read_file","arguments":"{\"path\":\"routes/controller/analytics.go\"}"}}`,
		`{"timestamp":"2026-03-09T09:00:03.700Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-analytics","output":"Exit code: 1\nWall time: 0.3 seconds\nOutput:\nFAIL"}}`,
		`{"timestamp":"2026-03-09T09:00:04Z","type":"event_msg","payload":{"type":"agent_message","message":"The analytics route registers auth, ingestion, and dashboard handlers in one place."}}`,
		`{"timestamp":"2026-03-09T09:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The analytics route registers auth, ingestion, and dashboard handlers in one place."}]}}`,
		`{"timestamp":"2026-03-09T09:00:06Z","type":"event_msg","payload":{"type":"user_message","message":"List the exact tests to run after the patch."}}`,
	}, "\n")+"\n"), 0o644))

	oldTime := time.Date(2026, 3, 8, 9, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 3, 9, 9, 0, 6, 0, time.UTC)
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
		ServerURL:   server.URL,
		APIToken:    "token-session",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	err := runSession([]string{"--tool", "codex", "--codex-home", codexHome})
	require.NoError(t, err)

	require.Equal(t, "project-1", uploaded.ProjectID)
	require.Equal(t, "codex-session-123", uploaded.SessionID)
	require.Equal(t, "codex", uploaded.Tool)
	require.Equal(t, 2100, uploaded.TokenIn)
	require.Equal(t, 480, uploaded.TokenOut)
	require.Equal(t, 700, uploaded.CachedInputTokens)
	require.Equal(t, 120, uploaded.ReasoningOutputTokens)
	require.Equal(t, 2, uploaded.FunctionCallCount)
	require.Equal(t, 1, uploaded.ToolErrorCount)
	require.Equal(t, 6000, uploaded.SessionDurationMS)
	require.Equal(t, 300, uploaded.ToolWallTimeMS)
	require.Equal(t, map[string]int{"shell": 1, "read_file": 1}, uploaded.ToolCalls)
	require.Equal(t, map[string]int{"shell": 1}, uploaded.ToolErrors)
	require.Equal(t, map[string]int{"shell": 300}, uploaded.ToolWallTimesMS)
	require.Equal(t, []string{
		"Inspect the analytics route and summarize the current control flow.",
		"List the exact tests to run after the patch.",
	}, uploaded.RawQueries)
	require.Equal(t, []string{"gpt-5.4"}, uploaded.Models)
	require.Equal(t, "openai", uploaded.ModelProvider)
	require.Equal(t, 2000, uploaded.FirstResponseLatencyMS)
	require.Equal(t, []string{"The analytics route registers auth, ingestion, and dashboard handlers in one place."}, uploaded.AssistantResponses)
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
		ServerURL:   server.URL,
		APIToken:    "token-session",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
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
	useStubCodexRunner(t, "apply")

	target := filepath.Join(root, "config.json")
	err := os.WriteFile(target, []byte("{\"baseline\":true}\n"), 0o644)
	require.NoError(t, err)

	result, err := executeLocalApply(state{WorkspaceID: "project-1"}, "apply-1", []response.PatchPreviewItem{
		{
			FilePath: target,
			SettingsUpdates: map[string]any{
				"shell_profile": "safe",
			},
		},
	}, "", "")
	require.NoError(t, err)
	require.Equal(t, target, result.FilePath)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Contains(t, string(data), "\"baseline\": true")
	require.Contains(t, string(data), "\"shell_profile\": \"safe\"")

	backup, err := loadApplyBackup("apply-1")
	require.NoError(t, err)
	require.Len(t, backup.Files, 1)
	require.Equal(t, "thread-apply", backup.CodexThreadID)
	require.Equal(t, "stub applied request", backup.CodexSummary)
	require.True(t, backup.Files[0].OriginalExists)
	require.Equal(t, true, backup.Files[0].OriginalJSON["baseline"])
}

func TestExecuteLocalApplyAppendsTextFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	useStubCodexRunner(t, "apply")

	target := filepath.Join(root, "AGENTS.md")
	err := os.WriteFile(target, []byte("# Existing\n"), 0o644)
	require.NoError(t, err)

	result, err := executeLocalApply(state{WorkspaceID: "project-1"}, "apply-text", []response.PatchPreviewItem{
		{
			FilePath:       "AGENTS.md",
			Operation:      "append_block",
			ContentPreview: "\n## AgentOpt\n- safe rollout\n",
		},
	}, target, "")
	require.NoError(t, err)
	require.Equal(t, target, result.FilePath)
	require.Contains(t, result.AppliedText, "AgentOpt")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Contains(t, string(data), "# Existing")
	require.Contains(t, string(data), "safe rollout")
}

func TestExecuteLocalApplyInstallsAgentoptSkillFile(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	t.Setenv("HOME", home)
	useStubCodexRunner(t, "apply")

	skillTarget := filepath.Join(home, ".codex", "skills", "agentopt-repo-discovery", "SKILL.md")
	content := "---\nname: agentopt-repo-discovery\n---\n\n# Repo Discovery Baseline\n"

	result, err := executeLocalApply(state{WorkspaceID: "project-1"}, "apply-skill", []response.PatchPreviewItem{
		{
			FilePath:       "~/.codex/skills/agentopt-repo-discovery/SKILL.md",
			Operation:      "text_replace",
			ContentPreview: content,
		},
	}, "", "")
	require.NoError(t, err)
	require.Equal(t, skillTarget, result.FilePath)
	require.Contains(t, result.AppliedText, "Repo Discovery Baseline")

	data, err := os.ReadFile(skillTarget)
	require.NoError(t, err)
	require.Equal(t, content, string(data))

	backup, err := loadApplyBackup("apply-skill")
	require.NoError(t, err)
	require.Len(t, backup.Files, 1)
	require.Equal(t, "thread-apply", backup.CodexThreadID)
	require.Equal(t, "stub applied request", backup.CodexSummary)
	require.False(t, backup.Files[0].OriginalExists)
	require.Equal(t, "text_replace", backup.Files[0].FileKind)
}

func TestPreflightLocalApplyRejectsUnsafeTarget(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	result, err := preflightLocalApply(state{WorkspaceID: "project-1"}, "apply-unsafe", []response.PatchPreviewItem{
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

func TestPreflightLocalApplyAllowsAgentoptSkillTarget(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	t.Setenv("HOME", home)

	result, err := preflightLocalApply(state{WorkspaceID: "project-1"}, "apply-skill", []response.PatchPreviewItem{
		{
			FilePath:       "~/.codex/skills/agentopt-repo-discovery/SKILL.md",
			Operation:      "text_replace",
			ContentPreview: "---\nname: agentopt-repo-discovery\n",
		},
	}, "")
	require.NoError(t, err)
	require.True(t, result.Allowed)
	require.Len(t, result.Steps, 1)
	require.True(t, result.Steps[0].Allowed)
	require.Equal(t, filepath.Join(home, ".codex", "skills", "agentopt-repo-discovery", "SKILL.md"), result.Steps[0].TargetFile)
}

func TestPreflightLocalApplyRejectsNonAgentoptSkillTarget(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	t.Setenv("HOME", home)

	result, err := preflightLocalApply(state{WorkspaceID: "project-1"}, "apply-skill-unsafe", []response.PatchPreviewItem{
		{
			FilePath:       "~/.codex/skills/custom-repo/SKILL.md",
			Operation:      "text_replace",
			ContentPreview: "---\nname: custom-repo\n",
		},
	}, "")
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Equal(t, "file_scope", result.Steps[0].Guard)
}

func TestPreflightLocalApplyAllowsMultipleDistinctSteps(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	result, err := preflightLocalApply(state{WorkspaceID: "project-1"}, "apply-multi", []response.PatchPreviewItem{
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

	result, err := preflightLocalApply(state{WorkspaceID: "project-1"}, "apply-dup", []response.PatchPreviewItem{
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
	useStubCodexRunner(t, "apply")

	configTarget := filepath.Join(root, "config.json")
	textTarget := filepath.Join(root, "AGENTS.md")
	err := os.WriteFile(configTarget, []byte("{\"baseline\":true}\n"), 0o644)
	require.NoError(t, err)

	result, err := executeLocalApply(state{WorkspaceID: "project-1"}, "apply-multi", []response.PatchPreviewItem{
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
	}, configTarget+","+textTarget, "")
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

func TestValidateChangedFilesRejectsUnexpectedCodexFileChange(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "AGENTS.md")
	err := validateChangedFiles(codexApplyRequest{
		WorkingDirectory: root,
		AllowedFiles:     []string{target},
	}, []string{target, filepath.Join(root, "UNAPPROVED.txt")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected file")
}

func TestChooseApplyWorkspaceUsesCWDWhenAllTargetsAreInsideIt(t *testing.T) {
	root := t.TempDir()
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	workingDirectory, additionalDirectories, err := chooseApplyWorkspace([]preflightStep{
		{TargetFile: filepath.Join(root, ".codex", "config.json")},
		{TargetFile: filepath.Join(root, "AGENTS.md")},
	})
	require.NoError(t, err)
	require.Equal(t, root, workingDirectory)
	require.Empty(t, additionalDirectories)
}

func TestChooseApplyWorkspaceAvoidsBroadSharedParent(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	homeDir := filepath.Join(root, "home")

	workingDirectory, additionalDirectories, err := chooseApplyWorkspace([]preflightStep{
		{TargetFile: filepath.Join(repoDir, "AGENTS.md")},
		{TargetFile: filepath.Join(homeDir, ".codex", "config.json")},
	})
	require.NoError(t, err)
	require.Equal(t, repoDir, workingDirectory)
	require.Equal(t, []string{filepath.Join(homeDir, ".codex")}, additionalDirectories)
}

func TestRunCodexApplyTimesOut(t *testing.T) {
	useSleepingCodexRunner(t, 200*time.Millisecond)
	t.Setenv("AGENTOPT_CODEX_TIMEOUT", "10ms")

	_, err := runCodexApply(codexApplyRequest{
		ApplyID:          "apply-timeout",
		WorkingDirectory: t.TempDir(),
		AllowedFiles:     []string{},
		Steps:            []codexApplyStep{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
}

func TestRunSyncRejectsInvalidIntervalInWatchMode(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	err := saveState(state{
		ServerURL:   "http://127.0.0.1:8082",
		APIToken:    "token",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	})
	require.NoError(t, err)

	err = runSync([]string{"--watch", "--interval", "0s"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "greater than zero")
}

func TestRunSyncOnceAppliesPendingPlansAndReportsSuccess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	useStubCodexRunner(t, "apply")

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
		ServerURL:   server.URL,
		APIToken:    "token-sync",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}

	err := runSyncOnce(st, newAPIClient(server.URL, "token-sync"), configTarget+","+textTarget, "")
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

func TestRunSyncOnceRollsBackRequestedPlansAndDeletesBackup(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)

	configTarget := filepath.Join(root, "config.json")
	textTarget := filepath.Join(root, "AGENTS.md")
	require.NoError(t, os.WriteFile(configTarget, []byte("{\"shell_profile\":\"safe\"}\n"), 0o644))
	require.NoError(t, os.WriteFile(textTarget, []byte("# Existing\n\n## AgentOpt\n- rollout\n"), 0o644))

	require.NoError(t, saveApplyBackup(applyBackup{
		ApplyID:     "apply-sync-rollback",
		WorkspaceID: "project-1",
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
		},
	}))

	var reported request.ApplyResultReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-sync-rollback", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/applies/pending":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.PendingApplyResp{
					Items: []response.PendingApplyItem{{
						ApplyID: "apply-sync-rollback",
						Action:  "rollback",
						Status:  "rollback_requested",
						Note:    "token regression detected",
					}},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID:    reported.ApplyID,
					Status:     "rollback_confirmed",
					RolledBack: true,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	st := state{
		ServerURL:   server.URL,
		APIToken:    "token-sync-rollback",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}

	err := runSyncOnce(st, newAPIClient(server.URL, "token-sync-rollback"), "", "")
	require.NoError(t, err)

	configData, err := os.ReadFile(configTarget)
	require.NoError(t, err)
	require.Contains(t, string(configData), "\"baseline\": true")
	require.NotContains(t, string(configData), "\"shell_profile\"")

	textData, err := os.ReadFile(textTarget)
	require.NoError(t, err)
	require.Equal(t, "# Existing\n", string(textData))

	require.Equal(t, "apply-sync-rollback", reported.ApplyID)
	require.True(t, reported.Success)
	require.True(t, reported.RolledBack)
	require.Equal(t, "rolled back by agentopt sync", reported.Note)
	require.Contains(t, reported.AppliedFile, configTarget)
	require.Contains(t, reported.AppliedFile, textTarget)

	backupPath, err := applyBackupPath("apply-sync-rollback")
	require.NoError(t, err)
	_, err = os.Stat(backupPath)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestRunSyncOnceFallsBackToCodexRollbackThreadWhenLocalRestoreFails(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	useStubCodexRunner(t, "rollback")
	t.Setenv("AGENTOPT_STUB_REQUIRE_RESUME", "1")

	originalRollbackRestore := rollbackRestoreExecutor
	rollbackRestoreExecutor = func([]applyFileBackup) error {
		return fmt.Errorf("forced local restore failure")
	}
	t.Cleanup(func() {
		rollbackRestoreExecutor = originalRollbackRestore
	})

	configTarget := filepath.Join(root, "config.json")
	createdTarget := filepath.Join(root, ".mcp.json")
	require.NoError(t, os.WriteFile(configTarget, []byte("{\"shell_profile\":\"safe\"}\n"), 0o644))
	require.NoError(t, os.WriteFile(createdTarget, []byte("{\"new\":true}\n"), 0o644))

	require.NoError(t, saveApplyBackup(applyBackup{
		ApplyID:       "apply-sync-rollback-thread",
		WorkspaceID:   "project-1",
		CodexThreadID: "thread-apply-rollback",
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
				FilePath:       createdTarget,
				FileKind:       "json_merge",
				OriginalExists: false,
			},
		},
	}))

	var reported request.ApplyResultReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-sync-rollback-thread", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/applies/pending":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.PendingApplyResp{
					Items: []response.PendingApplyItem{{
						ApplyID: "apply-sync-rollback-thread",
						Action:  "rollback",
						Status:  "rollback_requested",
						Note:    "fallback rollback requested",
					}},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID:    reported.ApplyID,
					Status:     "rollback_confirmed",
					RolledBack: true,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	st := state{
		ServerURL:   server.URL,
		APIToken:    "token-sync-rollback-thread",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}

	err := runSyncOnce(st, newAPIClient(server.URL, "token-sync-rollback-thread"), "", "")
	require.NoError(t, err)

	configData, err := os.ReadFile(configTarget)
	require.NoError(t, err)
	require.Contains(t, string(configData), "\"baseline\": true")
	require.NotContains(t, string(configData), "\"shell_profile\"")

	_, err = os.Stat(createdTarget)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))

	require.Equal(t, "apply-sync-rollback-thread", reported.ApplyID)
	require.True(t, reported.Success)
	require.True(t, reported.RolledBack)
	require.Equal(t, "rolled back by agentopt sync", reported.Note)
	require.Contains(t, reported.AppliedFile, configTarget)
	require.Contains(t, reported.AppliedFile, createdTarget)

	backupPath, err := applyBackupPath("apply-sync-rollback-thread")
	require.NoError(t, err)
	_, err = os.Stat(backupPath)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestRunSyncOnceReportsFailuresAndContinues(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	useStubCodexRunner(t, "apply")

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
		ServerURL:   server.URL,
		APIToken:    "token-sync-fail",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}

	err := runSyncOnce(st, newAPIClient(server.URL, "token-sync-fail"), "", "")
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
		ServerURL:   server.URL,
		APIToken:    "token-1",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
	}))

	configTarget := filepath.Join(root, "config.json")
	textTarget := filepath.Join(root, "AGENTS.md")
	createdTarget := filepath.Join(root, ".mcp.json")

	require.NoError(t, os.WriteFile(configTarget, []byte("{\"shell_profile\":\"safe\"}\n"), 0o644))
	require.NoError(t, os.WriteFile(textTarget, []byte("# Existing\n\n## AgentOpt\n- rollout\n"), 0o644))
	require.NoError(t, os.WriteFile(createdTarget, []byte("{\"new\":true}\n"), 0o644))

	require.NoError(t, saveApplyBackup(applyBackup{
		ApplyID:     "apply-rollback",
		WorkspaceID: "project-1",
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
	useStubCodexRunner(t, "apply")

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
		ServerURL:   server.URL,
		APIToken:    "token-apply",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
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
	useStubCodexRunner(t, "apply")

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
		ServerURL:   server.URL,
		APIToken:    "token-auto",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
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

func TestRunApplyYesBlocksWhenPreHarnessFails(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	useStubCodexRunner(t, "apply")

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".agentopt", "harness"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/preharness\n\ngo 1.22\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "harness_test.go"), []byte(`package preharness

import "testing"

func TestAlwaysFail(t *testing.T) {
	t.Fatal("forced pre-harness failure")
}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".agentopt", "harness", "precheck.json"), []byte(`{
  "version": 1,
  "name": "always-fail",
  "goal": "block apply when baseline is red",
  "test_commands": [
    "go test ./... -run TestAlwaysFail -count=1"
  ]
}
`), 0o644))

	targetFile := filepath.Join(root, "AGENTS.md")
	require.NoError(t, os.WriteFile(targetFile, []byte("# Existing\n"), 0o644))

	var (
		reported       request.ApplyResultReq
		harnessUploads []request.HarnessRunReq
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-pre-harness", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/recommendations/apply":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyPlanResp{
					ApplyID:        "apply-pre-harness",
					Status:         "approved_for_local_apply",
					PolicyMode:     "auto_approved",
					ApprovalStatus: "approved",
					Decision:       "auto_approved",
					Recommendation: response.RecommendationResp{ID: "rec-pre-harness"},
					PatchPreview: []response.PatchPreviewItem{{
						FilePath:       "AGENTS.md",
						Operation:      "append_block",
						ContentPreview: "\nblocked\n",
					}},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/harness-runs":
			var uploaded request.HarnessRunReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&uploaded))
			harnessUploads = append(harnessUploads, uploaded)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.HarnessRunResp{
					ID:               fmt.Sprintf("harness-%d", len(harnessUploads)),
					ProjectID:        uploaded.ProjectID,
					RecommendationID: uploaded.RecommendationID,
					ApplyID:          uploaded.ApplyID,
					SpecFile:         uploaded.SpecFile,
					Name:             uploaded.Name,
					Status:           map[bool]string{true: "passed", false: "failed"}[uploaded.Passed],
					Passed:           uploaded.Passed,
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID: "apply-pre-harness",
					Status:  "failed",
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:     server.URL,
		APIToken:      "token-pre-harness",
		OrgID:         "org-1",
		UserID:        "user-1",
		WorkspaceID:   "project-1",
		WorkspaceName: "pre-harness",
		RepoPath:      root,
	}))

	err := runApply([]string{"--recommendation-id", "rec-pre-harness", "--yes"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "pre-apply harness failed")

	require.Len(t, harnessUploads, 1)
	require.Equal(t, "project-1", harnessUploads[0].ProjectID)
	require.Equal(t, "rec-pre-harness", harnessUploads[0].RecommendationID)
	require.Equal(t, "apply-pre-harness", harnessUploads[0].ApplyID)
	require.False(t, harnessUploads[0].Passed)
	require.Equal(t, ".agentopt/harness/precheck.json", harnessUploads[0].SpecFile)

	require.Equal(t, "apply-pre-harness", reported.ApplyID)
	require.False(t, reported.Success)
	require.Contains(t, reported.Note, "pre-apply harness failed")
	require.Contains(t, reported.Note, "always-fail")

	data, err := os.ReadFile(targetFile)
	require.NoError(t, err)
	require.Equal(t, "# Existing\n", string(data))
}

func TestRunApplyYesRollsBackWhenPostHarnessFails(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTOPT_HOME", root)
	useStubCodexRunner(t, "apply")

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".agentopt", "harness"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/postharness\n\ngo 1.22\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "harness_test.go"), []byte(`package postharness

import (
	"os"
	"strings"
	"testing"
)

func TestFailsWhenBlockedMarkerExists(t *testing.T) {
	data, err := os.ReadFile("AGENTS.md")
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if strings.Contains(string(data), "blocked") {
		t.Fatal("blocked marker detected")
	}
}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".agentopt", "harness", "postcheck.json"), []byte(`{
  "version": 1,
  "name": "post-check",
  "goal": "fail after the blocked marker lands",
  "test_commands": [
    "go test ./... -run TestFailsWhenBlockedMarkerExists -count=1"
  ]
}
`), 0o644))

	targetFile := filepath.Join(root, "AGENTS.md")
	require.NoError(t, os.WriteFile(targetFile, []byte("# Existing\n"), 0o644))

	var (
		reported       request.ApplyResultReq
		harnessUploads []request.HarnessRunReq
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token-post-harness", r.Header.Get("X-AgentOpt-Token"))
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/recommendations/apply":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyPlanResp{
					ApplyID:        "apply-post-harness",
					Status:         "approved_for_local_apply",
					PolicyMode:     "auto_approved",
					ApprovalStatus: "approved",
					Decision:       "auto_approved",
					Recommendation: response.RecommendationResp{ID: "rec-post-harness"},
					PatchPreview: []response.PatchPreviewItem{{
						FilePath:       "AGENTS.md",
						Operation:      "append_block",
						ContentPreview: "\nblocked\n",
					}},
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/harness-runs":
			var uploaded request.HarnessRunReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&uploaded))
			harnessUploads = append(harnessUploads, uploaded)
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.HarnessRunResp{
					ID:               fmt.Sprintf("harness-%d", len(harnessUploads)),
					ProjectID:        uploaded.ProjectID,
					RecommendationID: uploaded.RecommendationID,
					ApplyID:          uploaded.ApplyID,
					SpecFile:         uploaded.SpecFile,
					Name:             uploaded.Name,
					Status:           map[bool]string{true: "passed", false: "failed"}[uploaded.Passed],
					Passed:           uploaded.Passed,
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/applies/result":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&reported))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ApplyResultResp{
					ApplyID:    "apply-post-harness",
					Status:     "rollback_confirmed",
					RolledBack: true,
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:     server.URL,
		APIToken:      "token-post-harness",
		OrgID:         "org-1",
		UserID:        "user-1",
		WorkspaceID:   "project-1",
		WorkspaceName: "post-harness",
		RepoPath:      root,
	}))

	err := runApply([]string{"--recommendation-id", "rec-post-harness", "--yes"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "post-apply harness failed")

	require.Len(t, harnessUploads, 2)
	require.True(t, harnessUploads[0].Passed)
	require.False(t, harnessUploads[1].Passed)
	require.Equal(t, "apply-post-harness", harnessUploads[0].ApplyID)
	require.Equal(t, "apply-post-harness", harnessUploads[1].ApplyID)

	require.Equal(t, "apply-post-harness", reported.ApplyID)
	require.True(t, reported.Success)
	require.True(t, reported.RolledBack)
	require.Contains(t, reported.Note, "auto-rolled back")
	require.Contains(t, reported.Note, "post-apply harness failed")

	data, err := os.ReadFile(targetFile)
	require.NoError(t, err)
	require.Equal(t, "# Existing\n", string(data))

	backupPath, err := applyBackupPath("apply-post-harness")
	require.NoError(t, err)
	_, err = os.Stat(backupPath)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
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
		ServerURL:   server.URL,
		APIToken:    "token-fail",
		OrgID:       "org-1",
		UserID:      "user-1",
		WorkspaceID: "project-1",
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
