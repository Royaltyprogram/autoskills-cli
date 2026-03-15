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
	"github.com/Royaltyprogram/aiops/service"
)

func TestSaveStateWritesSecureTokenSchema(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	accessExpiresAt := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	refreshExpiresAt := time.Date(2026, 4, 14, 9, 0, 0, 0, time.UTC)

	require.NoError(t, saveState(state{
		ServerURL:        "https://example.com",
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		TokenType:        "Bearer",
		AccessExpiresAt:  &accessExpiresAt,
		RefreshExpiresAt: &refreshExpiresAt,
		OrgID:            "org-1",
		UserID:           "user-1",
		AgentID:          "agent-1",
		WorkspaceID:      "project-1",
	}))

	statePath := filepath.Join(root, "state.json")
	info, err := os.Stat(statePath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	raw, err := os.ReadFile(statePath)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, "access-token", payload["access_token"])
	require.Equal(t, "refresh-token", payload["refresh_token"])
	require.Equal(t, "Bearer", payload["token_type"])
	require.NotContains(t, payload, "api_token")
}

func TestLoadStateReadsLegacyAPIToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	statePath := filepath.Join(root, "state.json")
	require.NoError(t, os.WriteFile(statePath, []byte(`{
  "server_url": "https://example.com",
  "api_token": "legacy-token",
  "org_id": "org-1",
  "user_id": "user-1",
  "agent_id": "agent-1"
}
`), 0o644))

	st, err := loadState()
	require.NoError(t, err)
	require.Equal(t, "legacy-token", st.accessToken())
	require.Equal(t, "legacy-token", st.AccessToken)

	info, err := os.Stat(statePath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestAPIClientRefreshesExpiredDeviceAccessToken(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CRUX_HOME", root)

	refreshCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/config-snapshots":
			if refreshCalls == 0 {
				require.Equal(t, "old-access", r.Header.Get("X-Crux-Token"))
				w.WriteHeader(http.StatusUnauthorized)
				require.NoError(t, json.NewEncoder(w).Encode(envelope{
					Code:    service.ErrCodeDeviceAccessTokenExpired,
					Message: "device access token expired",
				}))
				return
			}
			require.Equal(t, "new-access", r.Header.Get("X-Crux-Token"))
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.ConfigSnapshotListResp{}),
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/cli/refresh":
			var req request.CLIRefreshReq
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.Equal(t, "old-refresh", req.RefreshToken)
			refreshCalls++
			require.NoError(t, json.NewEncoder(w).Encode(envelope{
				Code: 0,
				Data: mustJSONRawMessage(t, response.CLIRefreshResp{
					AccessToken:  "new-access",
					RefreshToken: "new-refresh",
					TokenType:    "Bearer",
					AgentID:      "agent-1",
				}),
			}))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	st := state{
		ServerURL:    server.URL,
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		TokenType:    "Bearer",
		AgentID:      "agent-1",
	}
	require.NoError(t, saveState(st))

	client := newStateAPIClient(&st)
	var resp response.ConfigSnapshotListResp
	require.NoError(t, client.doJSON(http.MethodGet, "/api/v1/config-snapshots?project_id=project-1", nil, &resp))
	require.Equal(t, 1, refreshCalls)
	require.Equal(t, "new-access", st.AccessToken)
	require.Equal(t, "new-refresh", st.RefreshToken)

	loaded, err := loadState()
	require.NoError(t, err)
	require.Equal(t, "new-access", loaded.AccessToken)
	require.Equal(t, "new-refresh", loaded.RefreshToken)
}
