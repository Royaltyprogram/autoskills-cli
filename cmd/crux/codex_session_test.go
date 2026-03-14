package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCollectCodexSessionSummaryNormalizesReasoningSummaries(t *testing.T) {
	root := t.TempDir()
	sessionPath := filepath.Join(root, "session.jsonl")
	require.NoError(t, os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-reasoning","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nInspect the route and keep the patch small."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"**Checking route flow before proposing the minimal patch**"}]}}`,
		`{"timestamp":"2026-03-10T08:00:03Z","type":"event_msg","payload":{"type":"agent_message","message":"I will inspect the route flow first."}}`,
	}, "\n")+"\n"), 0o644))

	req, err := collectCodexSessionSummary(sessionPath, "codex")
	require.NoError(t, err)
	require.Equal(t, []string{"Checking route flow before proposing the minimal patch"}, req.ReasoningSummaries)
}

func TestCollectCodexSessionSummaryRejectsTitleGenerationRollout(t *testing.T) {
	root := t.TempDir()
	sessionPath := filepath.Join(root, "title.jsonl")
	require.NoError(t, os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-title","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai","source":"exec"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"Generate a concise UI title (18-36 characters) for this task.\nReturn only the title. No quotes or trailing punctuation.\nIf the task includes a ticket reference (e.g. ABC-123), include it verbatim.\n\nGenerate a clear, informative task title based solely on the prompt provided.\n\nUser prompt:\nFix the session parser to skip internal utility rollouts."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Skip internal utility rollouts"}}`,
	}, "\n")+"\n"), 0o644))

	_, err := collectCodexSessionSummary(sessionPath, "codex")
	require.Error(t, err)

	var utilityErr *codexSessionUtilityError
	require.True(t, errors.As(err, &utilityErr))
	require.Equal(t, codexSessionClassificationUtilityTitle, utilityErr.Classification)
}

func TestCollectCodexSessionSummaryRejectsApprovedLocalPlanRollout(t *testing.T) {
	root := t.TempDir()
	sessionPath := filepath.Join(root, "plan.jsonl")
	require.NoError(t, os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"codex-session-plan","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai","source":"exec","originator":"codex_sdk_ts"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"You are applying an approved local change plan.\nModify only the approved files listed below.\nDo not create, edit, rename, or delete any file outside that list.\nIf the request cannot be completed exactly within those files, do not guess. Return status=blocked.\nKeep changes minimal and aligned with the approved plan.\n\nApproved files:\n- /tmp/notes.txt\n\nApproved steps:\n1. target_file=/tmp/notes.txt\n   operation=text_append\n   summary=Append one line.\n\nAfter applying the changes, respond strictly as JSON matching {\"status\":\"applied|blocked\",\"summary\":\"...\"}."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-1"}}`,
	}, "\n")+"\n"), 0o644))

	_, err := collectCodexSessionSummary(sessionPath, "codex")
	require.Error(t, err)

	var utilityErr *codexSessionUtilityError
	require.True(t, errors.As(err, &utilityErr))
	require.Equal(t, codexSessionClassificationUtilityLocalPlan, utilityErr.Classification)
}

func TestCollectCodexSessionSummaryParsesLegacyRolloutFormat(t *testing.T) {
	root := t.TempDir()
	sessionPath := filepath.Join(root, "legacy.jsonl")
	require.NoError(t, os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"id":"legacy-session-1","timestamp":"2025-08-08T15:34:46.015Z","model_provider":"openai","cwd":"/repo"}`,
		`{"record_type":"state"}`,
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"## My request for Codex:\nInspect the parser and keep the patch small."}]}`,
		`{"type":"reasoning","summary":[{"type":"summary_text","text":"**Inspecting parser flow**"}]}`,
		`{"type":"function_call","name":"shell","call_id":"call-1"}`,
		`{"type":"function_call_output","call_id":"call-1","output":"Exit code: 0\nWall time: 0.1 seconds\nOutput:\nok\n"}`,
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I inspected the parser and kept the patch small."}]}`,
	}, "\n")+"\n"), 0o644))

	req, err := collectCodexSessionSummary(sessionPath, "codex")
	require.NoError(t, err)
	require.Equal(t, "legacy-session-1", req.SessionID)
	require.Equal(t, []string{"Inspect the parser and keep the patch small."}, req.RawQueries)
	require.Equal(t, []string{"I inspected the parser and kept the patch small."}, req.AssistantResponses)
	require.Equal(t, []string{"Inspecting parser flow"}, req.ReasoningSummaries)
	require.Equal(t, 1, req.FunctionCallCount)
	require.Equal(t, 100, req.ToolWallTimeMS)
	require.Equal(t, map[string]int{"shell": 1}, req.ToolCalls)
}

func TestCollectCodexSessionSummaryRejectsNoQueryMetadataOnlyRollout(t *testing.T) {
	root := t.TempDir()
	sessionPath := filepath.Join(root, "metadata-only.jsonl")
	require.NoError(t, os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"id":"metadata-only","timestamp":"2025-08-08T15:45:51.813Z","instructions":"# Repository Guidelines"}`,
	}, "\n")+"\n"), 0o644))

	_, err := collectCodexSessionSummary(sessionPath, "codex")
	require.Error(t, err)

	var skipErr *codexSessionSkipError
	require.True(t, errors.As(err, &skipErr))
	require.Equal(t, "no_raw_user_queries", skipErr.Reason)
}

func TestLoadSessionSummaryInputsSkipsUtilityRollouts(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "real-1.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"real-session-1","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nInspect the parser and keep the patch small."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"I will inspect the parser first."}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "title.jsonl"), baseTime.Add(1*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:01:00Z","type":"session_meta","payload":{"id":"utility-title","timestamp":"2026-03-10T08:01:00Z","model_provider":"openai","source":"exec"}}`,
		`{"timestamp":"2026-03-10T08:01:01Z","type":"event_msg","payload":{"type":"user_message","message":"Generate a concise UI title (18-36 characters) for this task.\nReturn only the title. No quotes or trailing punctuation.\nIf the task includes a ticket reference (e.g. ABC-123), include it verbatim.\n\nGenerate a clear, informative task title based solely on the prompt provided.\n\nTask:\nInspect the parser and keep the patch small."}}`,
		`{"timestamp":"2026-03-10T08:01:02Z","type":"event_msg","payload":{"type":"agent_message","message":"Inspect parser patch"}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "real-2.jsonl"), baseTime.Add(2*time.Minute), []string{
		`{"timestamp":"2026-03-10T08:02:00Z","type":"session_meta","payload":{"id":"real-session-2","timestamp":"2026-03-10T08:02:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:02:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nVerify recent session uploads after the parser change."}}`,
		`{"timestamp":"2026-03-10T08:02:02Z","type":"event_msg","payload":{"type":"agent_message","message":"I will verify recent session uploads after the parser change."}}`,
	})

	reqs, err := loadSessionSummaryInputs("", "codex", codexHome, 2)
	require.NoError(t, err)
	require.Len(t, reqs, 2)
	require.Equal(t, "real-session-1", reqs[0].SessionID)
	require.Equal(t, "real-session-2", reqs[1].SessionID)
}

func TestLoadSessionSummaryInputsSkipsNoQueryMetadataOnlyRollouts(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2025", "08", "08", "metadata-only.jsonl"), baseTime.Add(1*time.Minute), []string{
		`{"id":"metadata-only","timestamp":"2025-08-08T15:45:51.813Z","instructions":"# Repository Guidelines"}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "real.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"real-session","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nVerify automatic session collection still returns real work."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"I verified the automatic session collection path."}}`,
	})

	reqs, err := loadSessionSummaryInputs("", "codex", codexHome, 1)
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, "real-session", reqs[0].SessionID)
}

func TestLoadSessionSummaryInputsCoalescesAbortedRetryRollouts(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "retry-a.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"retry-a","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai","cwd":"/repo"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpdate the parser and keep the config comments aligned."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":10,"reasoning_output_tokens":0,"total_tokens":60}}}}`,
		`{"timestamp":"2026-03-10T08:00:03Z","type":"event_msg","payload":{"type":"turn_aborted"}}`,
	})
	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "retry-b.jsonl"), baseTime.Add(12*time.Second), []string{
		`{"timestamp":"2026-03-10T08:00:12Z","type":"session_meta","payload":{"id":"retry-b","timestamp":"2026-03-10T08:00:12Z","model_provider":"openai","cwd":"/repo"}}`,
		`{"timestamp":"2026-03-10T08:00:13Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpdate the parser and keep the config comments aligned."}}`,
		`{"timestamp":"2026-03-10T08:00:14Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-1"}}`,
		`{"timestamp":"2026-03-10T08:00:15Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-1","output":"Exit code: 0\nWall time: 0.1 seconds\nOutput:\nok\n"}}`,
		`{"timestamp":"2026-03-10T08:00:16Z","type":"event_msg","payload":{"type":"agent_message","message":"I updated the parser and aligned the config comments."}}`,
		`{"timestamp":"2026-03-10T08:00:17Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":120,"cached_input_tokens":10,"output_tokens":40,"reasoning_output_tokens":5,"total_tokens":160}}}}`,
	})

	reqs, err := loadSessionSummaryInputs("", "codex", codexHome, 1)
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, "retry-a", reqs[0].SessionID)
	require.Equal(t, 170, reqs[0].TokenIn)
	require.Equal(t, 50, reqs[0].TokenOut)
	require.Equal(t, 10, reqs[0].CachedInputTokens)
	require.Equal(t, 5, reqs[0].ReasoningOutputTokens)
	require.Equal(t, 1, reqs[0].FunctionCallCount)
	require.Equal(t, []string{"Update the parser and keep the config comments aligned."}, reqs[0].RawQueries)
}

func TestSelectCodexParsedSessionsAfterCursorKeepsMergedRetryUploadable(t *testing.T) {
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "retry-a.jsonl"), baseTime, []string{
		`{"timestamp":"2026-03-10T08:00:00Z","type":"session_meta","payload":{"id":"retry-a","timestamp":"2026-03-10T08:00:00Z","model_provider":"openai","cwd":"/repo"}}`,
		`{"timestamp":"2026-03-10T08:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpdate the parser and keep the config comments aligned."}}`,
		`{"timestamp":"2026-03-10T08:00:02Z","type":"event_msg","payload":{"type":"turn_aborted"}}`,
	})

	initialSessions, err := loadCodexParsedSessions(codexHome, "codex")
	require.NoError(t, err)
	require.Len(t, initialSessions, 1)
	initialCursor := initialSessions[0].uploadCursor()
	require.NotNil(t, initialCursor)
	require.Equal(t, "retry-a", initialCursor.SessionID)

	writeCodexSessionFixture(t, filepath.Join(codexHome, "sessions", "2026", "03", "10", "retry-b.jsonl"), baseTime.Add(12*time.Second), []string{
		`{"timestamp":"2026-03-10T08:00:12Z","type":"session_meta","payload":{"id":"retry-b","timestamp":"2026-03-10T08:00:12Z","model_provider":"openai","cwd":"/repo"}}`,
		`{"timestamp":"2026-03-10T08:00:13Z","type":"event_msg","payload":{"type":"user_message","message":"## My request for Codex:\nUpdate the parser and keep the config comments aligned."}}`,
		`{"timestamp":"2026-03-10T08:00:14Z","type":"event_msg","payload":{"type":"agent_message","message":"I updated the parser and aligned the config comments."}}`,
	})

	mergedSessions, err := loadCodexParsedSessions(codexHome, "codex")
	require.NoError(t, err)
	require.Len(t, mergedSessions, 1)
	require.Equal(t, "retry-a", mergedSessions[0].req.SessionID)

	pending := selectCodexParsedSessionsAfterCursor(mergedSessions, initialCursor)
	require.Len(t, pending, 1)
	require.Equal(t, "retry-a", pending[0].req.SessionID)
}

func writeCodexSessionFixture(t *testing.T, path string, modTime time.Time, lines []string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	require.NoError(t, os.Chtimes(path, modTime, modTime))
}
