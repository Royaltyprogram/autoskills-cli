package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
