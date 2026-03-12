package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

func TestRunHarnessExecutesRepoLocalSpecs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".agentopt", "harness"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/harnessdemo\n\ngo 1.22\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample_test.go"), []byte(`package harnessdemo

import "testing"

func TestHarnessSamplePass(t *testing.T) {}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".agentopt", "harness", "basic.json"), []byte(`{
  "version": 1,
  "name": "basic",
  "goal": "run the repo-local smoke test",
  "test_commands": [
    "go test ./... -run TestHarnessSamplePass -count=1"
  ],
  "assertions": [
    {"kind": "exit_code", "equals": 0}
  ]
}
`), 0o644))

	output := captureStdout(t, func() {
		require.NoError(t, runHarness([]string{"run", "--dir", root, "--timeout", "2m"}))
	})

	var resp harnessRunResponse
	require.NoError(t, json.Unmarshal([]byte(output), &resp))
	require.True(t, resp.Passed)
	require.Equal(t, canonicalizePath(root), canonicalizePath(resp.Root))
	require.Len(t, resp.Results, 1)
	require.True(t, resp.Results[0].Passed)
	require.Equal(t, "basic", resp.Results[0].Name)
	require.Len(t, resp.Results[0].Commands, 1)
	require.Equal(t, "test", resp.Results[0].Commands[0].Phase)
}

func TestRunHarnessCommandRejectsDisallowedPrograms(t *testing.T) {
	result := runHarnessCommand(t.TempDir(), "test", "bash -lc echo nope", time.Minute)
	require.False(t, result.Passed)
	require.Contains(t, result.Error, "outside the harness allowlist")
}

func TestRunHarnessUploadsResultsWhenConnected(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".agentopt", "harness"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/harnessdemo\n\ngo 1.22\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sample_test.go"), []byte(`package harnessdemo

import "testing"

func TestHarnessUploadPass(t *testing.T) {}
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".agentopt", "harness", "basic.json"), []byte(`{
  "version": 1,
  "name": "basic",
  "goal": "run the repo-local upload smoke test",
  "test_commands": [
    "go test ./... -run TestHarnessUploadPass -count=1"
  ]
}
`), 0o644))

	var uploaded request.HarnessRunReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/harness-runs", r.URL.Path)
		require.Equal(t, "test-token", r.Header.Get("X-AgentOpt-Token"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&uploaded))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"msg":  "ok",
			"data": response.HarnessRunResp{
				ID:          "harness_1",
				ProjectID:   uploaded.ProjectID,
				SpecFile:    uploaded.SpecFile,
				Name:        uploaded.Name,
				Status:      "passed",
				Passed:      true,
				Commands:    []response.HarnessCommandResultResp{},
				StartedAt:   uploaded.StartedAt,
				CompletedAt: uploaded.CompletedAt,
				CreatedAt:   time.Now().UTC(),
			},
		}))
	}))
	defer server.Close()

	t.Setenv("AGENTOPT_HOME", t.TempDir())
	st := state{
		ServerURL: server.URL,
		APIToken:  "test-token",
		UserID:    "user-upload",
	}
	st.rememberWorkspace(connectedWorkspace{
		ID:       "project-upload",
		Name:     "upload-demo",
		RepoPath: root,
	})
	require.NoError(t, saveState(st))

	output := captureStdout(t, func() {
		require.NoError(t, runHarness([]string{"run", "--dir", root, "--timeout", "2m"}))
	})

	var resp harnessRunResponse
	require.NoError(t, json.Unmarshal([]byte(output), &resp))
	require.True(t, resp.Passed)
	require.Len(t, resp.Uploads, 1)
	require.Equal(t, "project-upload", uploaded.ProjectID)
	require.Equal(t, ".agentopt/harness/basic.json", uploaded.SpecFile)
	require.Equal(t, "basic", uploaded.Name)
	require.Equal(t, "user-upload", uploaded.TriggeredBy)
	require.Len(t, uploaded.Commands, 1)
	require.Equal(t, "test", uploaded.Commands[0].Phase)
}

func TestPreflightAllowsRepoLocalHarnessSpecs(t *testing.T) {
	root := t.TempDir()
	result, err := preflightLocalApply(state{RepoPath: root}, "apply-harness", []response.PatchPreviewItem{{
		FilePath:       ".agentopt/harness/agentopt-default.json",
		Operation:      "text_replace",
		ContentPreview: "{\n  \"version\": 1\n}\n",
	}}, "")
	require.NoError(t, err)
	require.True(t, result.Allowed)
	require.Len(t, result.Steps, 1)
	require.True(t, result.Steps[0].Allowed)
	require.Equal(t, canonicalizePath(filepath.Join(root, ".agentopt", "harness", "agentopt-default.json")), canonicalizePath(result.Steps[0].TargetFile))
}
