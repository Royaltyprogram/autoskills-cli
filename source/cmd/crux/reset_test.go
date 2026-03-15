package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunResetRemovesStateAndLogs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	require.NoError(t, saveState(state{
		ServerURL:   "https://example.com",
		AccessToken: "agt_dva_test",
		OrgID:       "org-1",
		UserID:      "user-1",
		AgentID:     "device-1",
		WorkspaceID: "project-1",
	}))

	logDir := filepath.Join(root, "logs")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "collector.stdout.log"), []byte("old log"), 0o644))

	output := captureStdout(t, func() {
		require.NoError(t, run([]string{"reset"}))
	})

	var payload resetResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.Equal(t, root, payload.HomeDir)
	require.Equal(t, filepath.Join(root, "state.json"), payload.StatePath)
	require.True(t, payload.StateRemoved)
	require.Equal(t, "removed", payload.Background.Status)
	require.True(t, payload.Background.LogsRemoved)

	_, err := os.Stat(filepath.Join(root, "state.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(logDir)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRunResetUnloadsAndRemovesBackgroundCollector(t *testing.T) {
	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	cruxHome := filepath.Join(root, "crux-home")
	launchAgentsDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(launchAgentsDir, 0o755))
	require.NoError(t, os.MkdirAll(cruxHome, 0o755))
	t.Setenv("HOME", homeDir)
	t.Setenv("CRUX_HOME", cruxHome)

	launchctlLog := filepath.Join(root, "launchctl.log")
	launchctlPath := filepath.Join(root, "launchctl")
	require.NoError(t, os.WriteFile(launchctlPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$LAUNCHCTL_LOG\"\nexit 0\n"), 0o755))
	t.Setenv("LAUNCHCTL_LOG", launchctlLog)
	t.Setenv("CRUX_LAUNCHCTL_BIN", launchctlPath)

	require.NoError(t, saveState(state{
		ServerURL:   "https://example.com",
		AccessToken: "agt_dva_test",
		OrgID:       "org-1",
		UserID:      "user-1",
		AgentID:     "device-1",
		WorkspaceID: "project-1",
	}))

	label := backgroundLaunchdLabel(cruxHome)
	plistPath := filepath.Join(launchAgentsDir, label+".plist")
	require.NoError(t, os.WriteFile(plistPath, []byte("<plist/>"), 0o644))

	logDir := filepath.Join(cruxHome, "logs")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "collector.stderr.log"), []byte("old log"), 0o644))

	output := captureStdout(t, func() {
		require.NoError(t, run([]string{"reset"}))
	})

	var payload resetResp
	require.NoError(t, json.Unmarshal([]byte(output), &payload))
	require.True(t, payload.StateRemoved)
	require.Equal(t, "removed", payload.Background.Status)
	require.True(t, payload.Background.UnloadAttempted)
	require.True(t, payload.Background.UnloadSucceeded)
	require.True(t, payload.Background.PlistRemoved)
	require.True(t, payload.Background.LogsRemoved)

	_, err := os.Stat(plistPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(logDir)
	require.ErrorIs(t, err, os.ErrNotExist)

	logData, err := os.ReadFile(launchctlLog)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(string(logData)), "unload "+plistPath)
}
