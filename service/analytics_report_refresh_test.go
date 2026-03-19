package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReportRefreshCheckpointCountsBackfillsInTenSessionStepsUpToCap(t *testing.T) {
	require.Equal(t, []int{10, 20, 30, 40, 50, 60, 70, 80}, reportRefreshCheckpointCounts(0, 87, 10, 100))
	require.Equal(t, []int{100}, reportRefreshCheckpointCounts(95, 140, 10, 100))
	require.Nil(t, reportRefreshCheckpointCounts(100, 140, 10, 100))
}

func TestPrepareReportRefreshLockedBuildsChronologicalBackfillJobs(t *testing.T) {
	store := &AnalyticsStore{
		sessionSummaries:    make(map[string][]*SessionSummary),
		configSnapshots:     make(map[string][]*ConfigSnapshot),
		reports:             make(map[string]*Report),
		projectReports:      make(map[string][]string),
		reportResearch:      make(map[string]*ReportResearchStatus),
		skillSetClients:     make(map[string]*SkillSetClientState),
		skillSetDeployments: make(map[string][]*SkillSetDeploymentEvent),
		skillSetVersions:    make(map[string][]*SkillSetVersion),
	}
	svc := &AnalyticsService{
		Options: Options{
			AnalyticsStore: store,
		},
		researchAgent: &CloudResearchAgent{
			Provider: "openai",
			Model:    "gpt-5.4",
			apiKey:   "test-openai-key",
		},
		reportMinSessions: 10,
		reportRefreshLive: make(map[string]bool),
		reportRefreshNext: make(map[string][]*reportRefreshJob),
	}

	project := &Project{ID: "project-1", OrgID: "org-1", Name: "demo"}
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	sessions := make([]*SessionSummary, 0, 25)
	for idx := 25; idx >= 1; idx-- {
		sessions = append(sessions, &SessionSummary{
			ID:        fmt.Sprintf("session-%02d", idx),
			ProjectID: project.ID,
			Tool:      "codex",
			Timestamp: baseTime.Add(time.Duration(idx) * time.Minute),
			RawQueries: []string{
				fmt.Sprintf("review session %02d", idx),
			},
		})
	}
	store.sessionSummaries[project.ID] = sessions

	reports, jobs := svc.prepareReportRefreshLocked(project, "session-25", 0)
	require.Empty(t, reports)
	require.Len(t, jobs, 2)

	require.Len(t, jobs[0].sessions, 10)
	require.Equal(t, "session-01", jobs[0].sessions[0].ID)
	require.Equal(t, "session-10", jobs[0].sessions[9].ID)

	require.Len(t, jobs[1].sessions, 20)
	require.Equal(t, "session-01", jobs[1].sessions[0].ID)
	require.Equal(t, "session-20", jobs[1].sessions[19].ID)

	status := store.reportResearch[project.ID]
	require.NotNil(t, status)
	require.Equal(t, "running", status.State)
	require.Contains(t, status.Summary, "backfill feedback passes")
}

func TestPrepareReportRefreshLockedCapsBackfillAtFirstHundredSessions(t *testing.T) {
	store := &AnalyticsStore{
		sessionSummaries:    make(map[string][]*SessionSummary),
		configSnapshots:     make(map[string][]*ConfigSnapshot),
		reports:             make(map[string]*Report),
		projectReports:      make(map[string][]string),
		reportResearch:      make(map[string]*ReportResearchStatus),
		skillSetClients:     make(map[string]*SkillSetClientState),
		skillSetDeployments: make(map[string][]*SkillSetDeploymentEvent),
		skillSetVersions:    make(map[string][]*SkillSetVersion),
	}
	svc := &AnalyticsService{
		Options: Options{
			AnalyticsStore: store,
		},
		researchAgent: &CloudResearchAgent{
			Provider: "openai",
			Model:    "gpt-5.4",
			apiKey:   "test-openai-key",
		},
		reportMinSessions: 10,
		reportRefreshLive: make(map[string]bool),
		reportRefreshNext: make(map[string][]*reportRefreshJob),
	}

	project := &Project{ID: "project-cap", OrgID: "org-1", Name: "demo"}
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	sessions := make([]*SessionSummary, 0, 140)
	for idx := 1; idx <= 140; idx++ {
		sessions = append(sessions, &SessionSummary{
			ID:        fmt.Sprintf("session-%03d", idx),
			ProjectID: project.ID,
			Tool:      "codex",
			Timestamp: baseTime.Add(time.Duration(idx) * time.Minute),
			RawQueries: []string{
				fmt.Sprintf("review session %03d", idx),
			},
		})
	}
	store.sessionSummaries[project.ID] = sessions

	reports, jobs := svc.prepareReportRefreshLocked(project, "session-140", 0)
	require.Empty(t, reports)
	require.Len(t, jobs, 10)
	require.Len(t, jobs[0].sessions, 10)
	require.Len(t, jobs[9].sessions, 100)
	require.Equal(t, "session-001", jobs[9].sessions[0].ID)
	require.Equal(t, "session-100", jobs[9].sessions[99].ID)
}

func TestPrepareReportRefreshLockedStopsQueueingAfterCap(t *testing.T) {
	store := &AnalyticsStore{
		sessionSummaries:    make(map[string][]*SessionSummary),
		configSnapshots:     make(map[string][]*ConfigSnapshot),
		reports:             make(map[string]*Report),
		projectReports:      make(map[string][]string),
		reportResearch:      make(map[string]*ReportResearchStatus),
		skillSetClients:     make(map[string]*SkillSetClientState),
		skillSetDeployments: make(map[string][]*SkillSetDeploymentEvent),
		skillSetVersions:    make(map[string][]*SkillSetVersion),
	}
	svc := &AnalyticsService{
		Options: Options{
			AnalyticsStore: store,
		},
		researchAgent: &CloudResearchAgent{
			Provider: "openai",
			Model:    "gpt-5.4",
			apiKey:   "test-openai-key",
		},
		reportMinSessions: 10,
		reportRefreshLive: make(map[string]bool),
		reportRefreshNext: make(map[string][]*reportRefreshJob),
	}

	project := &Project{ID: "project-cap-stop", OrgID: "org-1", Name: "demo"}
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	sessions := make([]*SessionSummary, 0, 120)
	for idx := 1; idx <= 120; idx++ {
		sessions = append(sessions, &SessionSummary{
			ID:        fmt.Sprintf("session-%03d", idx),
			ProjectID: project.ID,
			Tool:      "codex",
			Timestamp: baseTime.Add(time.Duration(idx) * time.Minute),
			RawQueries: []string{
				fmt.Sprintf("review session %03d", idx),
			},
		})
	}
	store.sessionSummaries[project.ID] = sessions

	reports, jobs := svc.prepareReportRefreshLocked(project, "session-120", 100)
	require.Empty(t, reports)
	require.Nil(t, jobs)

	status := store.reportResearch[project.ID]
	require.NotNil(t, status)
	require.Equal(t, "capped_history_window", status.State)
	require.Equal(t, 120, status.SessionCount)
	require.Contains(t, status.Summary, "capped at the first 100 sessions")
}
