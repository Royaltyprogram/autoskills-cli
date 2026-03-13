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
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

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
			AssistantResponses: []string{
				"I will inspect the controller before editing it.",
			},
			ReasoningSummaries: []string{
				"Checking controller flow before patching.",
			},
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
	require.Equal(t, []string{"Checking controller flow before patching."}, loaded.sessionSummaries["project_1"][0].ReasoningSummaries)
	require.NotNil(t, loaded.projects["project_1"].LastIngestedAt)
}

func TestAnalyticsStoreCollapsesProjectsIntoSharedWorkspaceOnLoad(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

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
		ConnectedAt: older,
	}
	store.projects["project_2"] = &Project{
		ID:          "project_2",
		OrgID:       "org-1",
		Name:        "repo-b",
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
	require.Equal(t, "Shared workspace", workspace.Name)
	require.Len(t, loaded.sessionSummaries["project_2"], 2)
	require.Empty(t, loaded.sessionSummaries["project_1"])
}

func TestAnalyticsStoreExportStateJSONUsesReportKeys(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	data, err := store.ExportStateJSON()
	require.NoError(t, err)

	text := string(data)
	require.Contains(t, text, `"schema_version": "analytics-store.v1"`)
	require.Contains(t, text, `"reports": {}`)
	require.Contains(t, text, `"project_reports": {}`)
	require.Contains(t, text, `"report_research": {}`)
}

func TestAnalyticsStoreImportStateJSONUsesReportKeys(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	current := []byte(`{
  "schema_version": "analytics-store.v1",
  "seq": 7,
  "reports": {
    "rep_1": {
      "ID": "rep_1",
      "ProjectID": "project_1",
      "Title": "Workflow feedback"
    }
  },
  "project_reports": {
    "project_1": ["rep_1"]
  },
  "report_research": {
    "project_1": {
      "ProjectID": "project_1",
      "State": "no_reports",
      "report_count": 1
    }
  }
}`)
	require.NoError(t, store.ImportStateJSON(current))

	store.mu.RLock()
	require.Contains(t, store.reports, "rep_1")
	require.Equal(t, []string{"rep_1"}, store.projectReports["project_1"])
	require.NotNil(t, store.reportResearch["project_1"])
	require.Equal(t, "no_reports", store.reportResearch["project_1"].State)
	require.Equal(t, 1, store.reportResearch["project_1"].ReportCount)
	store.mu.RUnlock()

	data, err := store.ExportStateJSON()
	require.NoError(t, err)
	text := string(data)
	require.Contains(t, text, `"schema_version": "analytics-store.v1"`)
	require.Contains(t, text, `"reports": {`)
	require.Contains(t, text, `"project_reports": {`)
	require.Contains(t, text, `"report_research": {`)
}

func TestAnalyticsStoreApplyLoadedRecordUsesReportRecordTypes(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	require.NoError(t, store.applyLoadedRecord("report", "", "rep_1", []byte(`{
  "ID": "rep_1",
  "ProjectID": "project_1",
  "Title": "Workflow feedback"
}`)))
	require.NoError(t, store.applyLoadedRecord("project_report", "", "project_1", []byte(`["rep_1"]`)))
	require.NoError(t, store.applyLoadedRecord("report_research", "", "project_1", []byte(`{
  "ProjectID": "project_1",
  "State": "succeeded",
  "report_count": 1
}`)))

	require.Contains(t, store.reports, "rep_1")
	require.Equal(t, []string{"rep_1"}, store.projectReports["project_1"])
	require.NotNil(t, store.reportResearch["project_1"])
	require.Equal(t, 1, store.reportResearch["project_1"].ReportCount)
}

func TestAnalyticsStoreImportStateJSONRejectsUnsupportedSchemaVersion(t *testing.T) {
	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	err = store.ImportStateJSON([]byte(`{
  "schema_version": "analytics-store.v999",
  "reports": {}
}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported analytics store schema_version")
}
