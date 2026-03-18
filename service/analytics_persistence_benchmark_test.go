package service

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Royaltyprogram/aiops/configs"
)

func BenchmarkAnalyticsPersistenceAccessTokenSeen(b *testing.B) {
	runPersistenceBenchmark(b, "full_rebuild", true, seedAccessTokenSeenMutation)
	runPersistenceBenchmark(b, "targeted", false, seedAccessTokenSeenMutation)
}

func BenchmarkAnalyticsPersistenceUploadSessionSummary(b *testing.B) {
	runPersistenceBenchmark(b, "full_rebuild", true, seedUploadSessionSummaryMutation)
	runPersistenceBenchmark(b, "targeted", false, seedUploadSessionSummaryMutation)
}

type persistenceBenchmarkMutation struct {
	targetRows      int
	targetPayload   int
	baselineRows    int
	baselinePayload int
	persist         func(tx *sql.Tx) error
}

func runPersistenceBenchmark(
	b *testing.B,
	name string,
	fullRebuild bool,
	mutate func(*testing.B, *AnalyticsStore) persistenceBenchmarkMutation,
) {
	b.Run(name, func(b *testing.B) {
		var rowsWritten int
		var payloadBytes int

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			store := newAnalyticsPersistenceBenchmarkStore(b)
			store.mu.Lock()
			mutation := mutate(b, store)
			if fullRebuild {
				rowsWritten = mutation.baselineRows
				payloadBytes = mutation.baselinePayload
			} else {
				rowsWritten = mutation.targetRows
				payloadBytes = mutation.targetPayload
			}
			b.StartTimer()

			var err error
			if fullRebuild {
				err = store.persistLocked()
			} else {
				err = store.withTxLocked(mutation.persist)
			}
			if err != nil {
				b.Fatal(err)
			}

			b.StopTimer()
			store.mu.Unlock()
			if err := store.Close(); err != nil {
				b.Fatal(err)
			}
		}

		b.ReportMetric(float64(rowsWritten), "rows/op")
		b.ReportMetric(float64(payloadBytes), "payload-bytes/op")
	})
}

func seedAccessTokenSeenMutation(b *testing.B, store *AnalyticsStore) persistenceBenchmarkMutation {
	b.Helper()

	tokenID := "token-0000"
	record := store.accessTokens[tokenID]
	if record == nil {
		b.Fatalf("missing token %s", tokenID)
	}

	seenAt := time.Unix(1_700_000_000, 0).UTC().Add(5 * time.Minute)
	record.LastSeenAt = cloneTime(&seenAt)

	allRecords, err := store.recordsForPersistence()
	if err != nil {
		b.Fatal(err)
	}
	payloadBytes, err := analyticsPayloadBytes(record)
	if err != nil {
		b.Fatal(err)
	}

	return persistenceBenchmarkMutation{
		targetRows:      1,
		targetPayload:   payloadBytes,
		baselineRows:    len(allRecords),
		baselinePayload: sumAnalyticsRecordPayloadBytes(b, allRecords),
		persist: func(tx *sql.Tx) error {
			return store.persistAccessTokenLocked(tx, tokenID)
		},
	}
}

func seedUploadSessionSummaryMutation(b *testing.B, store *AnalyticsStore) persistenceBenchmarkMutation {
	b.Helper()

	projectID := "project-0000"
	recordedAt := time.Unix(1_700_000_000, 0).UTC().Add(10 * time.Minute)

	project := store.projects[projectID]
	if project == nil {
		b.Fatalf("missing project %s", projectID)
	}

	session := &SessionSummary{
		ID:                     "session-bench-new",
		ProjectID:              projectID,
		Tool:                   "codex",
		TokenIn:                1200,
		TokenOut:               420,
		CachedInputTokens:      300,
		ReasoningOutputTokens:  80,
		FunctionCallCount:      4,
		ToolErrorCount:         0,
		SessionDurationMS:      125000,
		ToolWallTimeMS:         42000,
		ToolCalls:              map[string]int{"exec": 2, "rg": 2},
		ToolErrors:             map[string]int{},
		ToolWallTimesMS:        map[string]int{"exec": 22000, "rg": 20000},
		RawQueries:             []string{"measure targeted persistence"},
		Models:                 []string{"gpt-5.4"},
		ModelProvider:          "openai",
		FirstResponseLatencyMS: 1800,
		AssistantResponses:     []string{"measured"},
		ReasoningSummaries:     []string{"benchmark persistence path"},
		Timestamp:              recordedAt,
	}
	store.sessionSummaries[projectID] = append(store.sessionSummaries[projectID], session)
	project.LastIngestedAt = cloneTime(&recordedAt)

	research := store.reportResearch[projectID]
	if research == nil {
		b.Fatalf("missing report research for %s", projectID)
	}
	research.SessionCount++
	research.RawQueryCount += len(session.RawQueries)
	research.TriggerSessionID = session.ID

	audit := &AuditEvent{
		ID:          "audit-bench-new",
		OrgID:       project.OrgID,
		ProjectID:   projectID,
		Type:        "session.ingested",
		Message:     "session summary uploaded from local collector",
		Result:      "success",
		Reason:      "collector uploaded a session summary",
		ActorUserID: project.AgentID,
		CreatedAt:   recordedAt,
	}
	store.audits = append(store.audits, audit)

	allRecords, err := store.recordsForPersistence()
	if err != nil {
		b.Fatal(err)
	}

	payloadBytes := 0
	for _, payload := range []any{session, project, normalizeReportResearchStatus(research), audit} {
		itemBytes, err := analyticsPayloadBytes(payload)
		if err != nil {
			b.Fatal(err)
		}
		payloadBytes += itemBytes
	}

	return persistenceBenchmarkMutation{
		targetRows:      4,
		targetPayload:   payloadBytes,
		baselineRows:    len(allRecords),
		baselinePayload: sumAnalyticsRecordPayloadBytes(b, allRecords),
		persist: func(tx *sql.Tx) error {
			if err := store.persistSessionSummaryLocked(tx, projectID, session.ID); err != nil {
				return err
			}
			if err := store.persistProjectLocked(tx, projectID); err != nil {
				return err
			}
			if err := store.persistReportResearchLocked(tx, projectID); err != nil {
				return err
			}
			return store.persistAuditLocked(tx, audit.ID)
		},
	}
}

func newAnalyticsPersistenceBenchmarkStore(b *testing.B) *AnalyticsStore {
	b.Helper()

	conf := &configs.Config{}
	conf.App.StorePath = filepath.Join(b.TempDir(), fmt.Sprintf("analytics-bench-%d.json", time.Now().UnixNano()))

	store, err := NewAnalyticsStore(conf)
	if err != nil {
		b.Fatal(err)
	}

	now := time.Unix(1_700_000_000, 0).UTC()

	store.mu.Lock()
	store.seq = 200_000
	store.organizations["org-1"] = &Organization{ID: "org-1", Name: "Benchmark Org"}

	for i := 0; i < 20; i++ {
		userID := fmt.Sprintf("user-%04d", i)
		agentID := fmt.Sprintf("agent-%04d", i)
		projectID := fmt.Sprintf("project-%04d", i)

		store.users[userID] = &User{
			ID:        userID,
			OrgID:     "org-1",
			Email:     fmt.Sprintf("%s@example.com", userID),
			Name:      fmt.Sprintf("User %d", i),
			Role:      userRoleAdmin,
			Status:    userStatusActive,
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		}
		store.agents[agentID] = &Agent{
			ID:            agentID,
			OrgID:         "org-1",
			UserID:        userID,
			DeviceName:    fmt.Sprintf("Device %d", i),
			Hostname:      fmt.Sprintf("host-%d", i),
			Platform:      "darwin",
			CLIVersion:    "1.0.0",
			Tools:         []string{"codex"},
			ConsentScopes: []string{"config_snapshot", "session_summary"},
			RegisteredAt:  now.Add(time.Duration(i) * time.Minute),
		}
		store.projects[projectID] = &Project{
			ID:          projectID,
			OrgID:       "org-1",
			AgentID:     agentID,
			Name:        fmt.Sprintf("Workspace %d", i),
			DefaultTool: "codex",
			ConnectedAt: now.Add(time.Duration(i) * time.Minute),
		}
		store.projectReports[projectID] = []string{
			fmt.Sprintf("report-%04d-0", i),
			fmt.Sprintf("report-%04d-1", i),
		}
		store.reportResearch[projectID] = &ReportResearchStatus{
			ProjectID:       projectID,
			State:           "waiting_for_min_sessions",
			MinimumSessions: 20,
			SessionCount:    50,
			RawQueryCount:   100,
			ReportCount:     2,
		}
		store.skillSetClients[projectID] = &SkillSetClientState{
			ProjectID:  projectID,
			OrgID:      "org-1",
			AgentID:    agentID,
			BundleName: managedSkillBundleName,
			Mode:       "managed",
			SyncStatus: "synced",
			UpdatedAt:  now.Add(time.Duration(i) * time.Minute),
		}

		for j := 0; j < 5; j++ {
			store.configSnapshots[projectID] = append(store.configSnapshots[projectID], &ConfigSnapshot{
				ID:                fmt.Sprintf("snapshot-%04d-%02d", i, j),
				ProjectID:         projectID,
				Tool:              "codex",
				ProfileID:         fmt.Sprintf("profile-%d", j),
				EnabledMCPCount:   3,
				HooksEnabled:      true,
				InstructionFiles:  []string{"AGENTS.md"},
				ConfigFingerprint: fmt.Sprintf("cfg-%04d-%02d", i, j),
				CapturedAt:        now.Add(time.Duration(i*10+j) * time.Minute),
			})
		}

		for j := 0; j < 50; j++ {
			store.sessionSummaries[projectID] = append(store.sessionSummaries[projectID], &SessionSummary{
				ID:                     fmt.Sprintf("session-%04d-%03d", i, j),
				ProjectID:              projectID,
				Tool:                   "codex",
				TokenIn:                1000 + j,
				TokenOut:               400 + j,
				CachedInputTokens:      200,
				ReasoningOutputTokens:  40,
				FunctionCallCount:      3,
				ToolErrorCount:         0,
				SessionDurationMS:      90000,
				ToolWallTimeMS:         30000,
				ToolCalls:              map[string]int{"exec": 1, "rg": 2},
				ToolErrors:             map[string]int{},
				ToolWallTimesMS:        map[string]int{"exec": 10000, "rg": 20000},
				RawQueries:             []string{fmt.Sprintf("query-%d-%d", i, j)},
				Models:                 []string{"gpt-5.4"},
				ModelProvider:          "openai",
				FirstResponseLatencyMS: 1500,
				AssistantResponses:     []string{"ok"},
				ReasoningSummaries:     []string{"bench"},
				Timestamp:              now.Add(time.Duration(i*50+j) * time.Minute),
			})
		}

		for j := 0; j < 2; j++ {
			reportID := fmt.Sprintf("report-%04d-%d", i, j)
			store.reports[reportID] = &Report{
				ID:        reportID,
				ProjectID: projectID,
				Kind:      "feedback",
				Title:     fmt.Sprintf("Report %d-%d", i, j),
				Status:    "active",
				CreatedAt: now.Add(time.Duration(i*2+j) * time.Hour),
			}
		}

		for j := 0; j < 2; j++ {
			jobID := fmt.Sprintf("import-%04d-%d", i, j)
			store.sessionImportJobs[jobID] = &SessionImportJob{
				ID:        jobID,
				ProjectID: projectID,
				OrgID:     "org-1",
				AgentID:   agentID,
				Status:    sessionImportJobStatusSucceeded,
				CreatedAt: now.Add(-time.Duration(j+1) * time.Hour),
				UpdatedAt: now.Add(-time.Duration(j) * time.Hour),
				Failures:  []SessionImportJobFailure{},
			}
		}

		for j := 0; j < 3; j++ {
			store.skillSetDeployments[projectID] = append(store.skillSetDeployments[projectID], &SkillSetDeploymentEvent{
				ID:             fmt.Sprintf("skdep-%04d-%d", i, j),
				ProjectID:      projectID,
				OrgID:          "org-1",
				AgentID:        agentID,
				BundleName:     managedSkillBundleName,
				EventType:      "applied",
				Summary:        "benchmark deployment",
				Mode:           "managed",
				SyncStatus:     "synced",
				AppliedVersion: fmt.Sprintf("v%d", j),
				AppliedHash:    fmt.Sprintf("hash-%d-%d", i, j),
				OccurredAt:     now.Add(time.Duration(i*3+j) * time.Hour),
			})
			store.skillSetVersions[projectID] = append(store.skillSetVersions[projectID], &SkillSetVersion{
				ID:           fmt.Sprintf("skver-%04d-%d", i, j),
				ProjectID:    projectID,
				OrgID:        "org-1",
				BundleName:   managedSkillBundleName,
				Version:      fmt.Sprintf("v%d", j),
				CompiledHash: fmt.Sprintf("compiled-%d-%d", i, j),
				CreatedAt:    now.Add(time.Duration(i*3+j) * time.Hour),
				GeneratedAt:  now.Add(time.Duration(i*3+j) * time.Hour),
			})
		}

		for j := 0; j < 4; j++ {
			store.audits = append(store.audits, &AuditEvent{
				ID:          fmt.Sprintf("audit-%04d-%d", i, j),
				OrgID:       "org-1",
				ProjectID:   projectID,
				Type:        "session.ingested",
				Message:     "benchmark audit",
				Result:      "success",
				Reason:      "seed benchmark data",
				CreatedAt:   now.Add(time.Duration(i*4+j) * time.Minute),
				ActorUserID: userID,
			})
		}
	}

	for i := 0; i < 200; i++ {
		tokenID := fmt.Sprintf("token-%04d", i)
		userIndex := i % 20
		userID := fmt.Sprintf("user-%04d", userIndex)
		agentID := fmt.Sprintf("agent-%04d", userIndex)
		store.accessTokens[tokenID] = &AccessToken{
			ID:          tokenID,
			OrgID:       "org-1",
			UserID:      userID,
			AgentID:     agentID,
			Label:       fmt.Sprintf("Token %d", i),
			Kind:        TokenKindCLI,
			TokenHash:   fmt.Sprintf("hash-%04d", i),
			TokenPrefix: fmt.Sprintf("tok_%04d", i),
			CreatedAt:   now.Add(-time.Duration(i) * time.Minute),
		}
	}

	if err := store.persistLocked(); err != nil {
		store.mu.Unlock()
		_ = store.Close()
		b.Fatal(err)
	}
	store.mu.Unlock()

	return store
}

func sumAnalyticsRecordPayloadBytes(b *testing.B, records []analyticsDBRecord) int {
	b.Helper()

	total := 0
	for _, record := range records {
		total += len(record.payload)
	}
	return total
}

func analyticsPayloadBytes(payload any) (int, error) {
	data, err := marshalAnalyticsRecordPayload(payload)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}
