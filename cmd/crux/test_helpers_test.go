package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()

	fn()

	require.NoError(t, writer.Close())
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	return strings.TrimSpace(string(output))
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	os.Stderr = writer
	defer func() {
		os.Stderr = original
	}()

	fn()

	require.NoError(t, writer.Close())
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	return strings.TrimSpace(string(output))
}

func mustJSONRawMessage(t *testing.T, value any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)
	return json.RawMessage(data)
}

func uploadedSessionBatchItem(req request.SessionSummaryReq) response.SessionBatchIngestItemResp {
	recordedAt := req.Timestamp
	return response.SessionBatchIngestItemResp{
		SessionID:  req.SessionID,
		ProjectID:  req.ProjectID,
		Status:     "uploaded",
		RecordedAt: &recordedAt,
	}
}

func sessionBatchResp(projectID string, items ...response.SessionBatchIngestItemResp) response.SessionBatchIngestResp {
	resp := response.SessionBatchIngestResp{
		SchemaVersion: reportAPISchemaVersion,
		ProjectID:     projectID,
		Accepted:      len(items),
		Items:         append([]response.SessionBatchIngestItemResp(nil), items...),
	}
	for _, item := range items {
		switch strings.TrimSpace(item.Status) {
		case "failed":
			resp.Failed++
		case "updated":
			resp.Updated++
		default:
			resp.Uploaded++
		}
	}
	if len(items) > 0 {
		now := time.Now().UTC()
		resp.ResearchStatus = &response.ReportResearchStatusResp{
			SchemaVersion: reportAPISchemaVersion,
			State:         "waiting_for_next_batch",
			Summary:       "Collected sessions in batch.",
			TriggeredAt:   &now,
		}
	}
	return resp
}

func serveNoopSkillSetBundleRequest(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/skill-sets/latest":
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.SkillSetBundleResp{
				SchemaVersion: skillSetBundleSchemaVersion,
				ProjectID:     strings.TrimSpace(r.URL.Query().Get("project_id")),
				Status:        "no_reports",
				BundleName:    managedSkillBundleName,
			}),
		}))
		return true
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/skill-sets/client-state":
		var req request.SkillSetClientStateUpsertReq
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.NoError(t, json.NewEncoder(w).Encode(envelope{
			Code: 0,
			Data: mustJSONRawMessage(t, response.SkillSetClientStateResp{
				ProjectID:      req.ProjectID,
				AgentID:        "agent-1",
				BundleName:     firstNonEmpty(strings.TrimSpace(req.BundleName), managedSkillBundleName),
				Mode:           req.Mode,
				SyncStatus:     req.SyncStatus,
				AppliedVersion: req.AppliedVersion,
				AppliedHash:    req.AppliedHash,
				LastSyncedAt:   req.LastSyncedAt,
				PausedAt:       req.PausedAt,
				LastError:      req.LastError,
				UpdatedAt:      time.Now().UTC(),
			}),
		}))
		return true
	default:
		return false
	}
}
