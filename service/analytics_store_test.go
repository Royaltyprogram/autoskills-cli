package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/liushuangls/go-server-template/configs"
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
			TaskType:  "bugfix",
			Timestamp: now,
		},
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
}
