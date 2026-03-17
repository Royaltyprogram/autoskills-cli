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

func TestRunCollectAutoSyncsManagedSkillBundle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "14", "latest.jsonl"), time.Date(2026, 3, 14, 8, 0, 0, 0, time.UTC), []string{
		`{"timestamp":"2026-03-14T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-skills","timestamp":"2026-03-14T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-14T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nClarify the request before changing the collector."}}`,
	})

	var snapshotReq request.ConfigSnapshotReq
	var sessionReq request.SessionSummaryReq
	var skillClientReq request.SkillSetClientStateUpsertReq
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/skill-sets/latest":
			require.Equal(t, "project-1", r.URL.Query().Get("project_id"))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, testSkillSetBundleResp("project-1", "v-sync-1", "hash-sync-1", "clarify before building")),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/skill-sets/client-state":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&skillClientReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SkillSetClientStateResp{
					ProjectID:      skillClientReq.ProjectID,
					AgentID:        "agent-1",
					BundleName:     skillClientReq.BundleName,
					Mode:           skillClientReq.Mode,
					SyncStatus:     skillClientReq.SyncStatus,
					AppliedVersion: skillClientReq.AppliedVersion,
					AppliedHash:    skillClientReq.AppliedHash,
					LastSyncedAt:   skillClientReq.LastSyncedAt,
					PausedAt:       skillClientReq.PausedAt,
					LastError:      skillClientReq.LastError,
					UpdatedAt:      time.Now().UTC(),
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		AccessToken: "access-token",
		TokenType:   "Bearer",
		OrgID:       "org-1",
		UserID:      "user-1",
		AgentID:     "agent-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.NotNil(t, payload.SkillSet)
	require.Equal(t, "synced", payload.SkillSet.Status)
	require.Equal(t, "v-sync-1", payload.SkillSet.AppliedVersion)
	require.Equal(t, "hash-sync-1", payload.SkillSet.CompiledHash)
	require.Equal(t, "project-1", skillClientReq.ProjectID)
	require.Equal(t, "autopilot", skillClientReq.Mode)
	require.Equal(t, "synced", skillClientReq.SyncStatus)
	require.Equal(t, "v-sync-1", skillClientReq.AppliedVersion)

	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)
	skillIndex, err := os.ReadFile(filepath.Join(liveBundlePath, "SKILL.md"))
	require.NoError(t, err)
	require.Contains(t, string(skillIndex), "name: crux-personal-skillset")
	require.Contains(t, string(skillIndex), "Crux Personal Skill Set")
	openAIYAML, err := os.ReadFile(filepath.Join(liveBundlePath, "agents", "openai.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(openAIYAML), "allow_implicit_invocation: true")

	currentState, _, err := loadSkillSetState()
	require.NoError(t, err)
	require.Equal(t, "v-sync-1", currentState.AppliedVersion)
	require.Equal(t, "hash-sync-1", currentState.AppliedHash)
	require.Equal(t, "synced", currentState.LastSyncStatus)
}

func TestRunSkillsPauseResumeAndRollback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	require.NoError(t, writeTestSkillBundle(liveBundlePath, testSkillSetBundleResp("project-1", "v-current", "hash-current", "current bundle")))
	currentHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)
	backupPath, err := skillSetBackupVersionPath("v-prev")
	require.NoError(t, err)
	require.NoError(t, writeTestSkillBundle(backupPath, testSkillSetBundleResp("project-1", "v-prev", "hash-prev", "previous bundle")))
	previousHash, err := hashManagedSkillBundle(backupPath)
	require.NoError(t, err)

	now := time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)
	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-current",
		AppliedHash:    currentHash,
		LastSyncAt:     &now,
		LastSyncStatus: "synced",
	}))

	pauseOutput := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"pause", "--codex-home", codexHome}))
	})
	require.Contains(t, pauseOutput, `"mode": "frozen"`)

	resumeOutput := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"resume", "--codex-home", codexHome}))
	})
	require.Contains(t, resumeOutput, `"mode": "autopilot"`)

	rollbackOutput := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"rollback", "--version", "v-prev", "--codex-home", codexHome}))
	})
	require.Contains(t, rollbackOutput, `"status": "rolled_back"`)
	require.Contains(t, rollbackOutput, `"applied_version": "v-prev"`)

	skillIndex, err := os.ReadFile(filepath.Join(liveBundlePath, "SKILL.md"))
	require.NoError(t, err)
	require.Contains(t, string(skillIndex), "previous bundle")
	openAIYAML, err := os.ReadFile(filepath.Join(liveBundlePath, "agents", "openai.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(openAIYAML), "display_name: \"Crux Personal Skill Set\"")

	currentState, _, err := loadSkillSetState()
	require.NoError(t, err)
	require.Equal(t, "v-prev", currentState.AppliedVersion)
	require.Equal(t, previousHash, currentState.AppliedHash)
	require.Equal(t, "rolled_back", currentState.LastSyncStatus)
}

func TestRunCollectSyncsManagedSkillBundleRegardlessOfShadowDecision(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "14", "latest.jsonl"), time.Date(2026, 3, 14, 8, 0, 0, 0, time.UTC), []string{
		`{"timestamp":"2026-03-14T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-shadow","timestamp":"2026-03-14T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-14T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nValidate the candidate before touching the repo."}}`,
	})

	var skillClientReq request.SkillSetClientStateUpsertReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config-snapshots":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotListResp{}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/config-snapshots":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotResp{
					SnapshotID:        "snapshot-1",
					ProjectID:         "project-1",
					ProfileID:         "default",
					ConfigFingerprint: "fingerprint-1",
					CapturedAt:        time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/session-summaries":
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SessionIngestResp{
					SessionID:   "session-1",
					ProjectID:   "project-1",
					ReportCount: 1,
					RecordedAt:  time.Now().UTC(),
				}),
			}))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/skill-sets/latest":
			bundle := testSkillSetBundleResp("project-1", "v-shadow-1", "hash-shadow-1", "candidate bundle")
			now := time.Now().UTC()
			bundle.VersionHistory = []response.SkillSetVersionResp{{
				ID:                 "skver-1",
				ProjectID:          "project-1",
				BundleName:         managedSkillBundleName,
				Version:            "v-shadow-1",
				CompiledHash:       "hash-shadow-1",
				CreatedAt:          now,
				GeneratedAt:        now,
				DeploymentDecision: "shadow",
				DecisionReason:     "Shadow evaluation passed with score 0.42 and is waiting for the connected CLI to sync.",
			}}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, bundle),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/skill-sets/client-state":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&skillClientReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SkillSetClientStateResp{
					ProjectID:      skillClientReq.ProjectID,
					AgentID:        "agent-1",
					BundleName:     skillClientReq.BundleName,
					Mode:           skillClientReq.Mode,
					SyncStatus:     skillClientReq.SyncStatus,
					AppliedVersion: skillClientReq.AppliedVersion,
					AppliedHash:    skillClientReq.AppliedHash,
					LastSyncedAt:   skillClientReq.LastSyncedAt,
					PausedAt:       skillClientReq.PausedAt,
					LastError:      skillClientReq.LastError,
					UpdatedAt:      time.Now().UTC(),
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		AccessToken: "access-token",
		TokenType:   "Bearer",
		OrgID:       "org-1",
		UserID:      "user-1",
		AgentID:     "agent-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runCollect([]string{"--codex-home", codexHome}))
	})

	var payload collectRunResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.NotNil(t, payload.SkillSet)
	require.Equal(t, "synced", payload.SkillSet.Status)
	require.Equal(t, "project-1", skillClientReq.ProjectID)
	require.Equal(t, "synced", skillClientReq.SyncStatus)
	require.Equal(t, "v-shadow-1", skillClientReq.AppliedVersion)

	skillIndex, err := os.ReadFile(filepath.Join(codexHome, "skills", managedSkillBundleName, "SKILL.md"))
	require.NoError(t, err)
	require.Contains(t, string(skillIndex), "candidate bundle")

	currentState, _, err := loadSkillSetState()
	require.NoError(t, err)
	require.Equal(t, "synced", currentState.LastSyncStatus)
	require.Empty(t, currentState.LastError)
	require.Equal(t, "v-shadow-1", currentState.AppliedVersion)
}

func testSkillSetBundleResp(projectID, version, compiledHash, marker string) response.SkillSetBundleResp {
	skillIndex := "---\n" +
		"name: " + managedSkillBundleName + "\n" +
		"description: Use as the default operating skill set for this user across coding sessions to preserve recurring clarification, planning, validation, and collaboration rules learned from prior sessions.\n" +
		"---\n\n" +
		"# Crux Personal Skill Set\n\n" + marker + "\n"
	openAIYAML := "interface:\n" +
		"  display_name: \"Crux Personal Skill Set\"\n" +
		"  short_description: \"Auto-synced personal operating rules from Crux\"\n" +
		"  default_prompt: \"Use $crux-personal-skillset as the default operating skill set for this user, then follow the relevant category documents before responding.\"\n\n" +
		"policy:\n" +
		"  allow_implicit_invocation: true\n"
	return response.SkillSetBundleResp{
		SchemaVersion: skillSetBundleSchemaVersion,
		ProjectID:     projectID,
		Status:        "ready",
		BundleName:    managedSkillBundleName,
		Version:       version,
		CompiledHash:  compiledHash,
		Summary:       []string{"clarify before implementation"},
		Files: []response.SkillSetFileResp{
			{
				Path:    "SKILL.md",
				Content: skillIndex,
				Bytes:   len(skillIndex),
			},
			{
				Path:    "01-clarification.md",
				Content: "# Clarify Before Building\n\n- " + marker + "\n",
				Bytes:   len("# Clarify Before Building\n\n- " + marker + "\n"),
			},
			{
				Path:    "agents/openai.yaml",
				Content: openAIYAML,
				Bytes:   len(openAIYAML),
			},
			{
				Path:    "00-manifest.json",
				Content: testSkillSetManifest(version, compiledHash),
				Bytes:   len(testSkillSetManifest(version, compiledHash)),
			},
		},
	}
}

func testSkillSetManifest(version, compiledHash string) string {
	return "{\n" +
		"  \"schema_version\": \"" + skillSetBundleSchemaVersion + "\",\n" +
		"  \"bundle_name\": \"" + managedSkillBundleName + "\",\n" +
		"  \"version\": \"" + version + "\",\n" +
		"  \"compiled_hash\": \"" + compiledHash + "\"\n" +
		"}\n"
}

func TestEnsureAgentsMDSkillSetSection_CreatesNewFile(t *testing.T) {
	codexRoot := filepath.Join(t.TempDir(), ".codex")
	require.NoError(t, os.MkdirAll(codexRoot, 0o755))

	require.NoError(t, ensureAgentsMDSkillSetSection(codexRoot))

	data, err := os.ReadFile(filepath.Join(codexRoot, "AGENTS.md"))
	require.NoError(t, err)
	content := string(data)
	require.Contains(t, content, cruxSkillSetSectionStart)
	require.Contains(t, content, cruxSkillSetSectionEnd)
	require.Contains(t, content, managedSkillBundleName)
	require.Contains(t, content, "SKILL.md")
}

func TestEnsureAgentsMDSkillSetSection_AppendsToExisting(t *testing.T) {
	codexRoot := filepath.Join(t.TempDir(), ".codex")
	require.NoError(t, os.MkdirAll(codexRoot, 0o755))

	existing := "# My Custom Instructions\n\nAlways use Go.\n"
	require.NoError(t, os.WriteFile(filepath.Join(codexRoot, "AGENTS.md"), []byte(existing), 0o644))

	require.NoError(t, ensureAgentsMDSkillSetSection(codexRoot))

	data, err := os.ReadFile(filepath.Join(codexRoot, "AGENTS.md"))
	require.NoError(t, err)
	content := string(data)
	require.True(t, strings.HasPrefix(content, existing), "existing content must be preserved")
	require.Contains(t, content, cruxSkillSetSectionStart)
}

func TestEnsureAgentsMDSkillSetSection_Idempotent(t *testing.T) {
	codexRoot := filepath.Join(t.TempDir(), ".codex")
	require.NoError(t, os.MkdirAll(codexRoot, 0o755))

	require.NoError(t, ensureAgentsMDSkillSetSection(codexRoot))
	first, err := os.ReadFile(filepath.Join(codexRoot, "AGENTS.md"))
	require.NoError(t, err)

	require.NoError(t, ensureAgentsMDSkillSetSection(codexRoot))
	second, err := os.ReadFile(filepath.Join(codexRoot, "AGENTS.md"))
	require.NoError(t, err)

	require.Equal(t, string(first), string(second), "calling twice must not duplicate the section")
}

func TestDiffManagedSkillBundle_DetectsModifiedFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-diff-1", "hash-diff-1", "original content")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	// Create a backup of the original version.
	backupPath, err := skillSetBackupVersionPath("v-diff-1")
	require.NoError(t, err)
	require.NoError(t, writeTestSkillBundle(backupPath, bundle))

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-diff-1",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	// No conflict yet.
	diff, err := diffManagedSkillBundle(codexHome)
	require.NoError(t, err)
	require.False(t, diff.HasConflict)

	// Modify a file locally.
	clarifyPath := filepath.Join(liveBundlePath, "01-clarification.md")
	require.NoError(t, os.WriteFile(clarifyPath, []byte("# Modified\n\n- new rule added\n- another rule\n"), 0o644))

	diff, err = diffManagedSkillBundle(codexHome)
	require.NoError(t, err)
	require.True(t, diff.HasConflict)
	require.Len(t, diff.ModifiedFiles, 1)
	require.Equal(t, "01-clarification.md", diff.ModifiedFiles[0].Path)
	require.Greater(t, diff.ModifiedFiles[0].AddedLines, 0)
}

func TestDiffManagedSkillBundle_DetectsAddedAndRemovedFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-diff-2", "hash-diff-2", "original")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	backupPath, err := skillSetBackupVersionPath("v-diff-2")
	require.NoError(t, err)
	require.NoError(t, writeTestSkillBundle(backupPath, bundle))

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-diff-2",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	// Add a new file locally.
	require.NoError(t, os.WriteFile(filepath.Join(liveBundlePath, "04-custom.md"), []byte("# Custom\n"), 0o644))
	// Remove an existing file.
	require.NoError(t, os.Remove(filepath.Join(liveBundlePath, "01-clarification.md")))

	diff, err := diffManagedSkillBundle(codexHome)
	require.NoError(t, err)
	require.True(t, diff.HasConflict)
	require.Contains(t, diff.AddedFiles, "04-custom.md")
	require.Contains(t, diff.RemovedFiles, "01-clarification.md")
}

func TestRunSkillsResolveKeepLocal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-resolve-1", "hash-resolve-1", "original")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-resolve-1",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	// Modify a file to create a conflict.
	require.NoError(t, os.WriteFile(filepath.Join(liveBundlePath, "01-clarification.md"), []byte("# Modified locally\n"), 0o644))

	output := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"resolve", "--action", "keep-local", "--codex-home", codexHome}))
	})
	require.Contains(t, output, `"action": "keep-local"`)
	require.Contains(t, output, `"status": "resolved"`)

	st, _, err := loadSkillSetState()
	require.NoError(t, err)
	require.Equal(t, skillSetModeFrozen, st.Mode)
	require.Equal(t, "resolved_keep_local", st.LastSyncStatus)
	require.Empty(t, st.LastError)
}

func TestRunSkillsResolveAcceptRemote(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-resolve-2", "hash-resolve-2", "original content")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	// Create a backup for the applied version.
	backupPath, err := skillSetBackupVersionPath("v-resolve-2")
	require.NoError(t, err)
	require.NoError(t, writeTestSkillBundle(backupPath, bundle))

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-resolve-2",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	// Modify a file locally.
	require.NoError(t, os.WriteFile(filepath.Join(liveBundlePath, "01-clarification.md"), []byte("# Modified locally\n"), 0o644))

	output := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"resolve", "--action", "accept-remote", "--codex-home", codexHome}))
	})
	require.Contains(t, output, `"action": "accept-remote"`)
	require.Contains(t, output, `"status": "resolved"`)

	// Verify the file was restored to original.
	clarifyContent, err := os.ReadFile(filepath.Join(liveBundlePath, "01-clarification.md"))
	require.NoError(t, err)
	require.Contains(t, string(clarifyContent), "original content")

	st, _, err := loadSkillSetState()
	require.NoError(t, err)
	require.Equal(t, "resolved_accept_remote", st.LastSyncStatus)
}

func TestRunSkillsResolveNoConflict(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-noconflict", "hash-noconflict", "clean")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-noconflict",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"resolve", "--action", "keep-local", "--codex-home", codexHome}))
	})
	require.Contains(t, output, `"status": "no_conflict"`)
}

func TestRunSkillsResolveShowsDiffWhenNoAction(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-showdiff", "hash-showdiff", "original")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	backupPath, err := skillSetBackupVersionPath("v-showdiff")
	require.NoError(t, err)
	require.NoError(t, writeTestSkillBundle(backupPath, bundle))

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-showdiff",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	// Modify a file.
	require.NoError(t, os.WriteFile(filepath.Join(liveBundlePath, "01-clarification.md"), []byte("# Changed\n"), 0o644))

	output := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"resolve", "--codex-home", codexHome}))
	})
	require.Contains(t, output, `"status": "conflict"`)
	require.Contains(t, output, `"available_actions"`)
	require.Contains(t, output, "keep-local")
	require.Contains(t, output, "accept-remote")
	require.Contains(t, output, "backup-and-sync")
	require.Contains(t, output, `"has_conflict": true`)
}

func TestRunSkillsStatusShowsConflictDiff(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-status-conflict", "hash-status", "original")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	backupPath, err := skillSetBackupVersionPath("v-status-conflict")
	require.NoError(t, err)
	require.NoError(t, writeTestSkillBundle(backupPath, bundle))

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-status-conflict",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	// Modify a file to trigger conflict.
	require.NoError(t, os.WriteFile(filepath.Join(liveBundlePath, "01-clarification.md"), []byte("# Local edit\n"), 0o644))

	output := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"status", "--codex-home", codexHome}))
	})
	require.Contains(t, output, `"conflict_diff"`)
	require.Contains(t, output, `"has_conflict": true`)
	require.Contains(t, output, `"resolve_hint"`)
	require.Contains(t, output, "crux skills resolve")
}

func TestRunSkillsResolveBackupAndSync(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-backup-sync", "hash-backup-sync", "original content")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-backup-sync",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	// Modify locally.
	modifiedContent := "# My local customization\n\n- important local rule\n"
	require.NoError(t, os.WriteFile(filepath.Join(liveBundlePath, "01-clarification.md"), []byte(modifiedContent), 0o644))

	// Set up server for sync.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/skill-sets/latest":
			newBundle := testSkillSetBundleResp("project-1", "v-backup-sync-2", "hash-backup-sync-2", "new remote content")
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, newBundle),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/skill-sets/client-state":
			var req request.SkillSetClientStateUpsertReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SkillSetClientStateResp{
					ProjectID:      req.ProjectID,
					AgentID:        "agent-1",
					BundleName:     req.BundleName,
					Mode:           req.Mode,
					SyncStatus:     req.SyncStatus,
					AppliedVersion: req.AppliedVersion,
					AppliedHash:    req.AppliedHash,
					LastSyncedAt:   req.LastSyncedAt,
					PausedAt:       req.PausedAt,
					LastError:      req.LastError,
					UpdatedAt:      time.Now().UTC(),
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		AccessToken: "access-token",
		TokenType:   "Bearer",
		OrgID:       "org-1",
		UserID:      "user-1",
		AgentID:     "agent-1",
		WorkspaceID: "project-1",
	}))

	output := captureStdout(t, func() {
		require.NoError(t, runSkills([]string{"resolve", "--action", "backup-and-sync", "--codex-home", codexHome}))
	})
	require.Contains(t, output, `"action": "backup-and-sync"`)
	require.Contains(t, output, `"status": "resolved"`)
	require.Contains(t, output, `"backup_path"`)

	// Verify the local content was backed up.
	backups, err := listSkillSetBackups()
	require.NoError(t, err)
	foundLocalBackup := false
	for _, b := range backups {
		if strings.Contains(b, "-local-") {
			foundLocalBackup = true
			backupDir, berr := skillSetBackupVersionPath(b)
			require.NoError(t, berr)
			backedUpContent, berr := os.ReadFile(filepath.Join(backupDir, "01-clarification.md"))
			require.NoError(t, berr)
			require.Equal(t, modifiedContent, string(backedUpContent))
		}
	}
	require.True(t, foundLocalBackup, "local modifications should have been backed up")

	// Verify the live bundle now has the remote content.
	liveContent, err := os.ReadFile(filepath.Join(liveBundlePath, "01-clarification.md"))
	require.NoError(t, err)
	require.Contains(t, string(liveContent), "new remote content")
}

func TestSyncExecutesResolveDirectiveFromServer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	codexHome := filepath.Join(root, ".codex")
	liveBundlePath := filepath.Join(codexHome, "skills", managedSkillBundleName)

	bundle := testSkillSetBundleResp("project-1", "v-directive-1", "hash-directive-1", "original content")
	require.NoError(t, writeTestSkillBundle(liveBundlePath, bundle))
	liveHash, err := hashManagedSkillBundle(liveBundlePath)
	require.NoError(t, err)

	// Create backup for accept-remote to use.
	backupPath, err := skillSetBackupVersionPath("v-directive-1")
	require.NoError(t, err)
	require.NoError(t, writeTestSkillBundle(backupPath, bundle))

	require.NoError(t, saveSkillSetState(skillSetLocalState{
		SchemaVersion:  skillSetLocalStateSchemaVersion,
		Mode:           skillSetModeAutopilot,
		BundleName:     managedSkillBundleName,
		AppliedVersion: "v-directive-1",
		AppliedHash:    liveHash,
		LastSyncStatus: "synced",
	}))

	// Modify a file locally to cause conflict.
	require.NoError(t, os.WriteFile(filepath.Join(liveBundlePath, "01-clarification.md"), []byte("# Modified locally\n"), 0o644))

	var skillClientReq request.SkillSetClientStateUpsertReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/skill-sets/latest":
			// Return bundle with a resolve directive in client state.
			bundleResp := testSkillSetBundleResp("project-1", "v-directive-2", "hash-directive-2", "new remote content")
			bundleResp.ClientState = &response.SkillSetClientStateResp{
				ProjectID:        "project-1",
				AgentID:          "agent-1",
				BundleName:       managedSkillBundleName,
				Mode:             "autopilot",
				SyncStatus:       "conflict",
				AppliedVersion:   "v-directive-1",
				AppliedHash:      liveHash,
				ResolveDirective: "accept-remote",
			}
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, bundleResp),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/skill-sets/client-state":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&skillClientReq))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.SkillSetClientStateResp{
					ProjectID:      skillClientReq.ProjectID,
					AgentID:        "agent-1",
					BundleName:     skillClientReq.BundleName,
					Mode:           skillClientReq.Mode,
					SyncStatus:     skillClientReq.SyncStatus,
					AppliedVersion: skillClientReq.AppliedVersion,
					AppliedHash:    skillClientReq.AppliedHash,
					UpdatedAt:      time.Now().UTC(),
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	require.NoError(t, saveState(state{
		ServerURL:   server.URL,
		AccessToken: "access-token",
		TokenType:   "Bearer",
		OrgID:       "org-1",
		UserID:      "user-1",
		AgentID:     "agent-1",
		WorkspaceID: "project-1",
	}))

	st, err := loadWorkspaceState()
	require.NoError(t, err)
	client := newStateAPIClient(&st)

	// Capture stderr (directive log) and stdout (result).
	stderrOutput := captureStderr(t, func() {
		resp, syncErr := syncLatestSkillSet(&st, client, codexHome)
		require.NoError(t, syncErr)
		require.Contains(t, resp.Status, "resolved_accept-remote")
	})
	require.Contains(t, stderrOutput, "accept-remote")

	// Verify the file was restored to the original (from backup).
	clarifyContent, err := os.ReadFile(filepath.Join(liveBundlePath, "01-clarification.md"))
	require.NoError(t, err)
	require.Contains(t, string(clarifyContent), "original content")

	// State should reflect the resolution.
	currentState, _, err := loadSkillSetState()
	require.NoError(t, err)
	require.Equal(t, "resolved_accept_remote", currentState.LastSyncStatus)
}

func TestResolveDirectiveFromBundle(t *testing.T) {
	// nil bundle returns empty.
	require.Empty(t, resolveDirectiveFromBundle(nil))

	// Bundle without client state returns empty.
	require.Empty(t, resolveDirectiveFromBundle(&response.SkillSetBundleResp{}))

	// Bundle with empty directive returns empty.
	require.Empty(t, resolveDirectiveFromBundle(&response.SkillSetBundleResp{
		ClientState: &response.SkillSetClientStateResp{},
	}))

	// Bundle with directive returns it.
	require.Equal(t, "accept-remote", resolveDirectiveFromBundle(&response.SkillSetBundleResp{
		ClientState: &response.SkillSetClientStateResp{
			ResolveDirective: "accept-remote",
		},
	}))
}

func TestCountLineDiffs(t *testing.T) {
	added, removed := countLineDiffs("line1\nline2\nline3\n", "line1\nmodified\nline3\nnewline\n")
	require.Equal(t, 2, added)   // "modified" and "newline"
	require.Equal(t, 1, removed) // "line2"
}

func writeTestSkillBundle(root string, bundle response.SkillSetBundleResp) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, file := range bundle.Files {
		targetPath := filepath.Join(root, filepath.FromSlash(strings.TrimSpace(file.Path)))
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(targetPath, []byte(file.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
