package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
)

func TestCreateSessionImportJobCancelsExpiredActiveJob(t *testing.T) {
	svc, store, ctx, project := newSessionImportJobTestFixture(t)

	job, err := svc.CreateSessionImportJob(ctx, &request.SessionImportJobCreateReq{
		ProjectID:     project.ID,
		TotalSessions: 2,
	})
	require.NoError(t, err)

	_, err = svc.AppendSessionImportJobChunk(ctx, job.JobID, &request.SessionImportJobChunkReq{
		Sessions: []request.SessionSummaryReq{{
			Tool:      "codex",
			Timestamp: time.Now().UTC(),
		}},
	})
	require.NoError(t, err)

	store.mu.Lock()
	stale := store.sessionImportJobs[job.JobID]
	require.NotNil(t, stale)
	stale.UpdatedAt = time.Now().UTC().Add(-48 * time.Hour)
	store.mu.Unlock()

	replacement, err := svc.CreateSessionImportJob(ctx, &request.SessionImportJobCreateReq{
		ProjectID: project.ID,
	})
	require.NoError(t, err)
	require.NotEqual(t, job.JobID, replacement.JobID)
	require.False(t, replacement.Reused)

	store.mu.RLock()
	defer store.mu.RUnlock()
	expired := store.sessionImportJobs[job.JobID]
	require.NotNil(t, expired)
	require.Equal(t, sessionImportJobStatusCanceled, expired.Status)
	require.Contains(t, expired.LastError, "expired before completion")
	require.NotNil(t, expired.CompletedAt)
	require.Nil(t, expired.Sessions)
}

func TestCleanupExpiredSessionImportJobsDeletesOldTerminalJobs(t *testing.T) {
	svc, store, _, _ := newSessionImportJobTestFixture(t)

	now := time.Now().UTC()
	store.mu.Lock()
	store.sessionImportJobs["import-old"] = &SessionImportJob{
		ID:          "import-old",
		ProjectID:   "project-1",
		OrgID:       "org-1",
		AgentID:     "agent-1",
		Status:      sessionImportJobStatusSucceeded,
		CreatedAt:   now.Add(-10 * 24 * time.Hour),
		UpdatedAt:   now.Add(-9 * 24 * time.Hour),
		StartedAt:   cloneTime(ptrTime(now.Add(-9 * 24 * time.Hour))),
		CompletedAt: cloneTime(ptrTime(now.Add(-8 * 24 * time.Hour))),
	}
	require.NoError(t, svc.cleanupExpiredSessionImportJobsLocked(now))
	_, exists := store.sessionImportJobs["import-old"]
	store.mu.Unlock()
	require.False(t, exists)
}

func TestBuildSessionImportJobMetricsLocked(t *testing.T) {
	svc, store, _, _ := newSessionImportJobTestFixture(t)

	now := time.Now().UTC()
	job1Started := now.Add(-5 * time.Minute)
	job1Completed := now.Add(-4 * time.Minute)
	job2Started := now.Add(-3 * time.Minute)
	job2Completed := now.Add(-2 * time.Minute)

	store.mu.Lock()
	store.sessionImportJobs["import-1"] = &SessionImportJob{
		ID:                "import-1",
		ProjectID:         "project-1",
		OrgID:             "org-1",
		AgentID:           "agent-1",
		Status:            sessionImportJobStatusSucceeded,
		CreatedAt:         now.Add(-6 * time.Minute),
		UpdatedAt:         job1Completed,
		StartedAt:         cloneTime(&job1Started),
		CompletedAt:       cloneTime(&job1Completed),
		ProcessedSessions: 4,
		UploadedSessions:  4,
	}
	store.sessionImportJobs["import-2"] = &SessionImportJob{
		ID:                "import-2",
		ProjectID:         "project-1",
		OrgID:             "org-1",
		AgentID:           "agent-1",
		Status:            sessionImportJobStatusFailed,
		CreatedAt:         now.Add(-4 * time.Minute),
		UpdatedAt:         job2Completed,
		StartedAt:         cloneTime(&job2Started),
		CompletedAt:       cloneTime(&job2Completed),
		ProcessedSessions: 2,
		FailedSessions:    2,
	}
	store.sessionImportJobs["import-3"] = &SessionImportJob{
		ID:                "import-3",
		ProjectID:         "project-1",
		OrgID:             "org-1",
		AgentID:           "agent-1",
		Status:            sessionImportJobStatusRunning,
		CreatedAt:         now.Add(-time.Minute),
		UpdatedAt:         now,
		StartedAt:         cloneTime(ptrTime(now.Add(-time.Minute))),
		ProcessedSessions: 1,
	}

	metrics := svc.buildSessionImportJobMetricsLocked("org-1")
	store.mu.Unlock()

	require.NotNil(t, metrics)
	require.Equal(t, 3, metrics.CreatedJobs)
	require.Equal(t, 1, metrics.RunningJobs)
	require.Equal(t, 1, metrics.SucceededJobs)
	require.Equal(t, 1, metrics.FailedJobs)
	require.Equal(t, 7, metrics.ProcessedSessions)
	require.Equal(t, 4, metrics.UploadedSessions)
	require.Equal(t, 2, metrics.FailedSessions)
	require.InDelta(t, 0.29, metrics.FailureRate, 0.0001)
	require.Equal(t, 60_000, metrics.AvgDurationMS)
	require.InDelta(t, 3.0, metrics.ThroughputPerMinute, 0.0001)
	require.NotNil(t, metrics.LastCompletedAt)
	require.True(t, metrics.LastCompletedAt.Equal(job2Completed))
}

func TestListSessionImportJobsSupportsFailedOnlyAgentFilterAndCursor(t *testing.T) {
	svc, store, ctx, project := newSessionImportJobTestFixture(t)

	now := time.Now().UTC()
	store.mu.Lock()
	store.agents["agent-2"] = &Agent{
		ID:           "agent-2",
		OrgID:        "org-1",
		UserID:       "user-1",
		RegisteredAt: now,
	}
	store.sessionImportJobs["import-1"] = &SessionImportJob{
		ID:        "import-1",
		ProjectID: project.ID,
		OrgID:     "org-1",
		AgentID:   "agent-1",
		Status:    sessionImportJobStatusSucceeded,
		CreatedAt: now.Add(-4 * time.Minute),
		UpdatedAt: now.Add(-4 * time.Minute),
	}
	store.sessionImportJobs["import-2"] = &SessionImportJob{
		ID:             "import-2",
		ProjectID:      project.ID,
		OrgID:          "org-1",
		AgentID:        "agent-2",
		Status:         sessionImportJobStatusPartial,
		CreatedAt:      now.Add(-3 * time.Minute),
		UpdatedAt:      now.Add(-3 * time.Minute),
		FailedSessions: 1,
	}
	store.sessionImportJobs["import-3"] = &SessionImportJob{
		ID:             "import-3",
		ProjectID:      project.ID,
		OrgID:          "org-1",
		AgentID:        "agent-1",
		Status:         sessionImportJobStatusFailed,
		CreatedAt:      now.Add(-2 * time.Minute),
		UpdatedAt:      now.Add(-2 * time.Minute),
		FailedSessions: 2,
	}
	store.sessionImportJobs["import-4"] = &SessionImportJob{
		ID:        "import-4",
		ProjectID: project.ID,
		OrgID:     "org-1",
		AgentID:   "agent-1",
		Status:    sessionImportJobStatusRunning,
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now.Add(-time.Minute),
	}
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	firstPage, err := svc.ListSessionImportJobs(ctx, &request.SessionImportJobListReq{
		ProjectID:  project.ID,
		FailedOnly: true,
		Limit:      1,
	})
	require.NoError(t, err)
	require.Len(t, firstPage.Items, 1)
	require.Equal(t, "import-3", firstPage.Items[0].JobID)
	require.Equal(t, "import-3", firstPage.NextCursor)

	secondPage, err := svc.ListSessionImportJobs(ctx, &request.SessionImportJobListReq{
		ProjectID:  project.ID,
		FailedOnly: true,
		Cursor:     firstPage.NextCursor,
		Limit:      1,
	})
	require.NoError(t, err)
	require.Len(t, secondPage.Items, 1)
	require.Equal(t, "import-2", secondPage.Items[0].JobID)
	require.Empty(t, secondPage.NextCursor)

	agentFiltered, err := svc.ListSessionImportJobs(ctx, &request.SessionImportJobListReq{
		ProjectID:  project.ID,
		AgentID:    "agent-1",
		FailedOnly: true,
		Limit:      10,
	})
	require.NoError(t, err)
	require.Len(t, agentFiltered.Items, 1)
	require.Equal(t, "import-3", agentFiltered.Items[0].JobID)

	_, err = svc.ListSessionImportJobs(ctx, &request.SessionImportJobListReq{
		ProjectID: project.ID,
		Cursor:    "missing-job",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown session_import_job cursor")
}

func TestSessionImportJobBackfillsReportsInTenSessionStepsUpToHundred(t *testing.T) {
	type responsesRequest struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}

	var (
		requestsMu sync.Mutex
		requests   []responsesRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Equal(t, "Bearer test-openai-key", r.Header.Get("Authorization"))

		var req responsesRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		requestsMu.Lock()
		requests = append(requests, req)
		requestsMu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
 "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": "{\"schema_version\":\"report-feedback.v1\",\"reports\":[{\"kind\":\"repo-orientation-defaults\",\"title\":\"Reduce repeated repo orientation before work starts\",\"summary\":\"Recent sessions spend too many early turns on repo discovery and control-flow recap before the real task begins.\",\"user_intent\":\"The user wants a small, well-scoped fix with explicit verification before any broader changes.\",\"model_interpretation\":\"The model appears to read the request as a need to re-orient on repo structure and control flow before proposing a patch.\",\"reason\":\"The uploaded raw queries repeatedly ask for control-flow summaries, file discovery, and verification planning before any implementation work starts.\",\"explanation\":\"The workflow appears to require too much manual orientation on each task, so the user needs clearer repo-entry habits when starting a new task.\",\"expected_benefit\":\"Less repeated repo discovery and faster first useful responses.\",\"expected_impact\":\"Fewer exploratory turns and less prompt steering at the start of each task.\",\"confidence\":\"high\",\"strengths\":[\"asks for minimal patch scope\"],\"frictions\":[\"repeated control-flow recap\"],\"next_steps\":[\"start each task with the concrete files involved\"],\"score\":0.86,\"evidence\":[\"repeated control-flow recap\",\"repeated verification prompts\"]}]}"
        }
      ]
    }
  ]
}`))
		require.NoError(t, err)
	}))
	defer server.Close()

	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")
	conf.OpenAI.APIKey = "test-openai-key"
	conf.OpenAI.BaseURL = server.URL + "/v1"
	conf.OpenAI.ResponsesModel = "gpt-5.4"

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	now := time.Now().UTC()
	store.mu.Lock()
	store.organizations["org-1"] = &Organization{ID: "org-1", Name: "Org 1"}
	store.users["user-1"] = &User{
		ID:        "user-1",
		OrgID:     "org-1",
		Email:     "user-1@example.com",
		Name:      "User 1",
		Role:      userRoleAdmin,
		Status:    userStatusActive,
		CreatedAt: now,
	}
	store.agents["agent-1"] = &Agent{
		ID:           "agent-1",
		OrgID:        "org-1",
		UserID:       "user-1",
		RegisteredAt: now,
	}
	project := &Project{
		ID:          "project-1",
		OrgID:       "org-1",
		AgentID:     "agent-1",
		Name:        "Shared workspace",
		DefaultTool: "codex",
		ConnectedAt: now,
	}
	store.projects[project.ID] = project
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	svc := NewAnalyticsService(Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 10,
	})
	svc.refineAgent = nil

	ctx := WithAuthIdentity(context.Background(), AuthIdentity{
		TokenKind: TokenKindDeviceAccess,
		OrgID:     "org-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
	})

	job, err := svc.CreateSessionImportJob(ctx, &request.SessionImportJobCreateReq{
		ProjectID:     project.ID,
		TotalSessions: 103,
	})
	require.NoError(t, err)

	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
	for start := 1; start <= 103; start += 25 {
		end := start + 24
		if end > 103 {
			end = 103
		}
		chunk := make([]request.SessionSummaryReq, 0, end-start+1)
		for idx := start; idx <= end; idx++ {
			chunk = append(chunk, request.SessionSummaryReq{
				ProjectID:  project.ID,
				SessionID:  fmt.Sprintf("session-%03d", idx),
				Tool:       "codex",
				RawQueries: []string{fmt.Sprintf("inspect setup backfill session %03d", idx)},
				Timestamp:  baseTime.Add(time.Duration(idx) * time.Minute),
			})
		}
		_, err := svc.AppendSessionImportJobChunk(ctx, job.JobID, &request.SessionImportJobChunkReq{
			Sessions: chunk,
		})
		require.NoError(t, err)
	}

	_, err = svc.CompleteSessionImportJob(ctx, job.JobID, &request.SessionImportJobCompleteReq{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		resp, getErr := svc.GetSessionImportJob(ctx, job.JobID)
		if getErr != nil || resp == nil || resp.Status != sessionImportJobStatusSucceeded {
			return false
		}
		requestsMu.Lock()
		defer requestsMu.Unlock()
		return len(requests) == 10
	}, 5*time.Second, 50*time.Millisecond)

	requestsMu.Lock()
	recordedRequests := append([]responsesRequest(nil), requests...)
	requestsMu.Unlock()
	require.Len(t, recordedRequests, 10)
	for expected := 10; expected <= 100; expected += 10 {
		expectedNeedle := fmt.Sprintf("- sessions=%d", expected)
		require.Contains(t, recordedRequests[(expected/10)-1].Input, expectedNeedle)
	}
	for _, req := range recordedRequests {
		require.NotContains(t, req.Input, "- sessions=103")
		require.Contains(t, req.Input, "report-feedback.v1")
	}

	store.mu.RLock()
	require.Len(t, store.sessionSummaries[project.ID], 103)
	store.mu.RUnlock()

	uploadResp, err := svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:  project.ID,
		SessionID:  "session-104",
		Tool:       "codex",
		RawQueries: []string{"inspect post-cap behavior"},
		Timestamp:  baseTime.Add(104 * time.Minute),
	})
	require.NoError(t, err)
	require.NotNil(t, uploadResp.ResearchStatus)
	require.Equal(t, "capped_history_window", uploadResp.ResearchStatus.State)

	require.Never(t, func() bool {
		requestsMu.Lock()
		defer requestsMu.Unlock()
		return len(requests) > 10
	}, 500*time.Millisecond, 50*time.Millisecond)

	requestsMu.Lock()
	defer requestsMu.Unlock()
	require.Equal(t, 10, len(requests))
}

func newSessionImportJobTestFixture(t *testing.T) (*AnalyticsService, *AnalyticsStore, context.Context, *Project) {
	t.Helper()

	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(t.TempDir(), "crux-store.json")

	store, err := NewAnalyticsStore(conf)
	require.NoError(t, err)

	now := time.Now().UTC()
	store.mu.Lock()
	store.organizations["org-1"] = &Organization{
		ID:   "org-1",
		Name: "Org 1",
	}
	store.users["user-1"] = &User{
		ID:        "user-1",
		OrgID:     "org-1",
		Email:     "user-1@example.com",
		Name:      "User 1",
		Role:      userRoleAdmin,
		Status:    userStatusActive,
		CreatedAt: now,
	}
	store.agents["agent-1"] = &Agent{
		ID:           "agent-1",
		OrgID:        "org-1",
		UserID:       "user-1",
		RegisteredAt: now,
	}
	project := &Project{
		ID:          "project-1",
		OrgID:       "org-1",
		AgentID:     "agent-1",
		Name:        "Shared workspace",
		DefaultTool: "codex",
		ConnectedAt: now,
	}
	store.projects[project.ID] = project
	require.NoError(t, store.persistLocked())
	store.mu.Unlock()

	svc := NewAnalyticsService(Options{
		Config:            conf,
		AnalyticsStore:    store,
		ReportMinSessions: 10,
	})
	ctx := WithAuthIdentity(context.Background(), AuthIdentity{
		TokenKind: TokenKindDeviceAccess,
		OrgID:     "org-1",
		UserID:    "user-1",
		AgentID:   "agent-1",
	})
	return svc, store, ctx, project
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
