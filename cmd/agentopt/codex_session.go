package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liushuangls/go-server-template/dto/request"
)

type codexSessionLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMetaPayload struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

type codexTokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type codexTokenCountInfo struct {
	TotalTokenUsage *codexTokenUsage `json:"total_token_usage"`
}

type codexEventMsgPayload struct {
	Type    string               `json:"type"`
	Message string               `json:"message"`
	Info    *codexTokenCountInfo `json:"info"`
}

type codexResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexResponseItemPayload struct {
	Type    string                 `json:"type"`
	Role    string                 `json:"role"`
	Content []codexResponseContent `json:"content"`
}

func recentCodexSessionFiles(codexHome string, limit int) ([]string, error) {
	if limit < 1 {
		return nil, errors.New("recent Codex session limit must be at least 1")
	}

	root, err := codexHomePath(codexHome)
	if err != nil {
		return nil, err
	}

	sessionsRoot := filepath.Join(root, "sessions")
	type sessionFile struct {
		path    string
		modTime time.Time
	}
	files := make([]sessionFile, 0, limit)

	err = filepath.WalkDir(sessionsRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, sessionFile{path: path, modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no Codex sessions found under %s; pass --file or set --codex-home", sessionsRoot)
		}
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no Codex sessions found under %s; pass --file or set --codex-home", sessionsRoot)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].path < files[j].path
		}
		return files[i].modTime.Before(files[j].modTime)
	})

	if len(files) > limit {
		files = files[len(files)-limit:]
	}

	paths := make([]string, 0, len(files))
	for _, item := range files {
		paths = append(paths, item.path)
	}
	return paths, nil
}

func codexHomePath(override string) (string, error) {
	override = strings.TrimSpace(override)
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func collectCodexSessionSummary(path, tool string) (request.SessionSummaryReq, error) {
	file, err := os.Open(path)
	if err != nil {
		return request.SessionSummaryReq{}, err
	}
	defer file.Close()

	req := request.SessionSummaryReq{Tool: tool}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	seenQueries := map[string]struct{}{}
	var latestTimestamp time.Time
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var item codexSessionLine
		if err := json.Unmarshal(line, &item); err != nil {
			return request.SessionSummaryReq{}, fmt.Errorf("parse Codex session line %d: %w", lineNo, err)
		}
		if ts, ok := parseCodexTimestamp(item.Timestamp); ok && ts.After(latestTimestamp) {
			latestTimestamp = ts
		}

		switch item.Type {
		case "session_meta":
			var payload codexSessionMetaPayload
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				continue
			}
			if req.SessionID == "" {
				req.SessionID = strings.TrimSpace(payload.ID)
			}
			if ts, ok := parseCodexTimestamp(payload.Timestamp); ok && ts.After(latestTimestamp) {
				latestTimestamp = ts
			}
		case "event_msg":
			var payload codexEventMsgPayload
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				continue
			}
			switch payload.Type {
			case "user_message":
				appendRawQuery(seenQueries, &req.RawQueries, payload.Message)
			case "token_count":
				if payload.Info != nil && payload.Info.TotalTokenUsage != nil {
					req.TokenIn = maxInt(req.TokenIn, payload.Info.TotalTokenUsage.InputTokens)
					req.TokenOut = maxInt(req.TokenOut, payload.Info.TotalTokenUsage.OutputTokens)
				}
			}
		case "response_item":
			var payload codexResponseItemPayload
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				continue
			}
			if payload.Type != "message" || payload.Role != "user" {
				continue
			}
			for _, content := range payload.Content {
				if content.Type != "input_text" {
					continue
				}
				appendRawQuery(seenQueries, &req.RawQueries, content.Text)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return request.SessionSummaryReq{}, err
	}

	if latestTimestamp.IsZero() {
		latestTimestamp = time.Now().UTC()
	}
	req.Timestamp = latestTimestamp
	if req.SessionID == "" {
		req.SessionID = sanitizeID(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}

	if len(req.RawQueries) == 0 {
		return request.SessionSummaryReq{}, fmt.Errorf("no raw user queries found in Codex session %s", path)
	}
	return req, nil
}

func appendRawQuery(seen map[string]struct{}, dst *[]string, raw string) {
	query := normalizeCodexUserMessage(raw)
	if query == "" {
		return
	}
	if _, ok := seen[query]; ok {
		return
	}
	seen[query] = struct{}{}
	*dst = append(*dst, query)
}

func normalizeCodexUserMessage(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if marker := "## My request for Codex:"; strings.Contains(raw, marker) {
		raw = raw[strings.LastIndex(raw, marker)+len(marker):]
	} else if marker := "## My request for Codex"; strings.Contains(raw, marker) {
		raw = raw[strings.LastIndex(raw, marker)+len(marker):]
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "<environment_context>") {
		return ""
	}

	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n\n", "\n")
	raw = strings.TrimSpace(raw)
	return raw
}

func parseCodexTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}
