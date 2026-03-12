package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
)

func TestAnalyticsStorePersistenceRoundTrip(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	now := time.Now().UTC().Round(time.Second)

	store.mu.Lock()
	store.seq = 12
	store.organizations["demo-org"] = &Organization{ID: "demo-org", Name: "Demo Org"}
	store.projects["project_1"] = &Project{
		ID:             "project_1",
		OrgID:          "demo-org",
		Name:           "demo",
		DefaultTool:    "codex",
		LastIngestedAt: &now,
	}
	store.sessionSummaries["project_1"] = []*SessionSummary{
		{
			ID:        "session_1",
			ProjectID: "project_1",
			Tool:      "codex",
			TokenIn:   480,
			TokenOut:  120,
			RawQueries: []string{
				"Inspect the controller before editing it.",
			},
			Timestamp: now,
		},
	}
	store.recommendations["rec_1"] = &Recommendation{
		ID:        "rec_1",
		ProjectID: "project_1",
		Kind:      "harness-seed",
		Title:     "Install harness",
		Summary:   "Persist harness metadata.",
		Status:    "active",
		HarnessSpec: &HarnessSpec{
			Version:      1,
			Name:         "approval-regression",
			Goal:         "approval flow should stay green",
			TestCommands: []string{"go test ./... -run TestApproval -count=1"},
			Assertions: []HarnessAssertion{{
				Kind:   "exit_code",
				Equals: 0,
			}},
		},
		CreatedAt: now,
	}
	store.harnessRuns["harness_1"] = &HarnessRun{
		ID:          "harness_1",
		ProjectID:   "project_1",
		SpecFile:    ".agentopt/harness/agentopt-default.json",
		Name:        "approval-regression",
		Status:      "passed",
		Passed:      true,
		DurationMS:  2400,
		StartedAt:   now.Add(-3 * time.Second),
		CompletedAt: now,
		CreatedAt:   now,
	}
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	loaded, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	loaded.mu.RLock()
	defer loaded.mu.RUnlock()

	require.Equal(t, uint64(12), loaded.seq)
	require.Contains(t, loaded.organizations, "demo-org")
	require.Contains(t, loaded.projects, "project_1")
	require.Len(t, loaded.sessionSummaries["project_1"], 1)
	require.Equal(t, "session_1", loaded.sessionSummaries["project_1"][0].ID)
	require.NotNil(t, loaded.projects["project_1"].LastIngestedAt)
	require.NotNil(t, loaded.recommendations["rec_1"].HarnessSpec)
	require.Equal(t, "approval-regression", loaded.recommendations["rec_1"].HarnessSpec.Name)
	require.Contains(t, loaded.harnessRuns, "harness_1")
	require.Equal(t, "passed", loaded.harnessRuns["harness_1"].Status)
}

func TestAnalyticsStoreDedupesProjectsForTheSameRepoOnLoad(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "agentopt-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	older := time.Now().UTC().Add(-time.Hour).Round(time.Second)
	newer := time.Now().UTC().Round(time.Second)

	store.mu.Lock()
	store.organizations["org-1"] = &Organization{ID: "org-1", Name: "Org 1"}
	store.projects["project_1"] = &Project{
		ID:          "project_1",
		OrgID:       "org-1",
		Name:        "repo-a",
		RepoHash:    "repo-hash",
		RepoPath:    "/tmp/demo-repo",
		ConnectedAt: older,
	}
	store.projects["project_2"] = &Project{
		ID:          "project_2",
		OrgID:       "org-1",
		Name:        "repo-b",
		RepoHash:    "repo-hash",
		RepoPath:    "/tmp/demo-repo",
		ConnectedAt: newer,
	}
	store.sessionSummaries["project_1"] = []*SessionSummary{{
		ID:        "session_old",
		ProjectID: "project_1",
		Tool:      "codex",
		Timestamp: older,
	}}
	store.sessionSummaries["project_2"] = []*SessionSummary{{
		ID:        "session_new",
		ProjectID: "project_2",
		Tool:      "codex",
		Timestamp: newer,
	}}
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	loaded, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	loaded.mu.RLock()
	defer loaded.mu.RUnlock()

	require.Len(t, loaded.projects, 1)
	workspace := loaded.projects["project_2"]
	require.NotNil(t, workspace)
	require.Equal(t, "repo-b", workspace.Name)
	require.Len(t, loaded.sessionSummaries["project_2"], 2)
	require.Empty(t, loaded.sessionSummaries["project_1"])
}
