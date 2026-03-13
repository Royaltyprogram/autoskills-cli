package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/service"
)

func TestStoreExportImportCommandsRoundTrip(t *testing.T) {
	originalWD, err := os.Getwd()
	require.NoError(t, err)
	repoRoot := filepath.Clean(filepath.Join(originalWD, "..", ".."))
	require.NoError(t, os.Chdir(repoRoot))
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	tempDir := t.TempDir()
	sourceDB := filepath.Join(tempDir, "source.db") + "?_fk=1"
	targetDB := filepath.Join(tempDir, "target.db") + "?_fk=1"
	backupPath := filepath.Join(tempDir, "runtime-store-backup.json")

	t.Setenv("APP_MODE", "local")
	t.Setenv("DB_DSN", sourceDB)
	t.Setenv("APP_STORE_PATH", filepath.Join(tempDir, "source-store.json"))

	conf, err := configs.InitConfig()
	require.NoError(t, err)

	sourceStore, err := service.NewAnalyticsStore(conf)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, sourceStore.Close())
	}()

	svc := service.NewAnalyticsService(service.Options{
		Config:            conf,
		AnalyticsStore:    sourceStore,
		ReportMinSessions: 1,
	})
	ctx := service.WithAuthIdentity(context.Background(), service.AuthIdentity{
		OrgID:     "demo-org",
		UserID:    "demo-user",
		TokenKind: service.TokenKindCLI,
	})

	agentResp, err := svc.RegisterAgent(ctx, &request.RegisterAgentReq{
		OrgID:      "demo-org",
		OrgName:    "Demo Org",
		UserID:     "demo-user",
		UserEmail:  "demo@example.com",
		DeviceName: "backup-agent",
		Hostname:   "backup.local",
		Platform:   "darwin/arm64",
		CLIVersion: "test",
		Tools:      []string{"codex"},
		AgentID:    "agent-backup",
	})
	require.NoError(t, err)

	projectResp, err := svc.RegisterProject(ctx, &request.RegisterProjectReq{
		OrgID:       "demo-org",
		AgentID:     agentResp.DeviceID,
		Name:        "backup-project",
		RepoHash:    "repo-backup",
		RepoPath:    filepath.Join(tempDir, "workspace"),
		LanguageMix: map[string]float64{"go": 1},
		DefaultTool: "codex",
	})
	require.NoError(t, err)

	_, err = svc.UploadSessionSummary(ctx, &request.SessionSummaryReq{
		ProjectID:  projectResp.ProjectID,
		SessionID:  "session-backup",
		Tool:       "codex",
		TokenIn:    210,
		TokenOut:   55,
		RawQueries: []string{"List the exact deployment risks that remain before beta."},
		Timestamp:  time.Now().UTC().Round(time.Second),
	})
	require.NoError(t, err)

	exportOutput := captureStdout(t, func() {
		require.NoError(t, run([]string{"store-export", "--output", backupPath}))
	})
	require.Contains(t, exportOutput, "\"status\": \"exported\"")
	require.FileExists(t, backupPath)

	t.Setenv("DB_DSN", targetDB)
	t.Setenv("APP_STORE_PATH", filepath.Join(tempDir, "target-store.json"))

	importOutput := captureStdout(t, func() {
		require.NoError(t, run([]string{"store-import", "--input", backupPath, "--yes"}))
	})
	require.Contains(t, importOutput, "\"status\": \"imported\"")

	targetConf, err := configs.InitConfig()
	require.NoError(t, err)
	targetStore, err := service.NewAnalyticsStore(targetConf)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, targetStore.Close())
	}()

	targetSvc := service.NewAnalyticsService(service.Options{
		Config:            targetConf,
		AnalyticsStore:    targetStore,
		ReportMinSessions: 1,
	})
	sessions, err := targetSvc.ListSessionSummaries(ctx, &request.SessionSummaryListReq{
		ProjectID: projectResp.ProjectID,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, sessions.Items, 1)
	require.Equal(t, "session-backup", sessions.Items[0].ID)
	require.Contains(t, sessions.Items[0].RawQueries[0], "deployment risks")
}
