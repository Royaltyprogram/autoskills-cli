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
	"strconv"
	"strings"
	"time"

	"github.com/Royaltyprogram/aiops/dto/request"
)

type codexSessionLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMetaPayload struct {
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	ModelProvider string `json:"model_provider"`
}

type codexTurnContextPayload struct {
	Model string `json:"model"`
}

type codexTokenUsage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
	TotalTokens           int `json:"total_tokens"`
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
	CallID  string                 `json:"call_id"`
	Name    string                 `json:"name"`
	Content []codexResponseContent `json:"content"`
	Summary []codexResponseContent `json:"summary"`
	Output  any                    `json:"output"`
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
	seenModels := map[string]struct{}{}
	seenResponses := map[string]struct{}{}
	seenReasoningSummaries := map[string]struct{}{}
	toolCalls := make(map[string]int)
	toolErrors := make(map[string]int)
	toolWallTimesMS := make(map[string]int)
	callToolByID := make(map[string]string)
	var earliestTimestamp time.Time
	var latestTimestamp time.Time
	var firstMeaningfulUserAt time.Time
	var firstAssistantResponseAt time.Time
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
		lineTimestamp, hasLineTimestamp := parseCodexTimestamp(item.Timestamp)
		if hasLineTimestamp && (earliestTimestamp.IsZero() || lineTimestamp.Before(earliestTimestamp)) {
			earliestTimestamp = lineTimestamp
		}
		if hasLineTimestamp && lineTimestamp.After(latestTimestamp) {
			latestTimestamp = lineTimestamp
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
			if req.ModelProvider == "" {
				req.ModelProvider = strings.TrimSpace(payload.ModelProvider)
			}
			if ts, ok := parseCodexTimestamp(payload.Timestamp); ok && (earliestTimestamp.IsZero() || ts.Before(earliestTimestamp)) {
				earliestTimestamp = ts
			}
			if ts, ok := parseCodexTimestamp(payload.Timestamp); ok && ts.After(latestTimestamp) {
				latestTimestamp = ts
			}
		case "turn_context":
			var payload codexTurnContextPayload
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				continue
			}
			appendUniqueSessionText(seenModels, &req.Models, payload.Model)
		case "event_msg":
			var payload codexEventMsgPayload
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				continue
			}
			switch payload.Type {
			case "user_message":
				if appendRawQuery(seenQueries, &req.RawQueries, payload.Message) {
					captureFirstCodexTimestamp(&firstMeaningfulUserAt, lineTimestamp, hasLineTimestamp)
				}
			case "agent_message":
				if appendAssistantResponse(seenResponses, &req.AssistantResponses, payload.Message) {
					captureFirstCodexTimestamp(&firstAssistantResponseAt, lineTimestamp, hasLineTimestamp)
				}
			case "token_count":
				if payload.Info != nil && payload.Info.TotalTokenUsage != nil {
					req.TokenIn = maxInt(req.TokenIn, payload.Info.TotalTokenUsage.InputTokens)
					req.CachedInputTokens = maxInt(req.CachedInputTokens, payload.Info.TotalTokenUsage.CachedInputTokens)
					req.TokenOut = maxInt(req.TokenOut, payload.Info.TotalTokenUsage.OutputTokens)
					req.ReasoningOutputTokens = maxInt(req.ReasoningOutputTokens, payload.Info.TotalTokenUsage.ReasoningOutputTokens)
				}
			}
		case "response_item":
			var payload codexResponseItemPayload
			if err := json.Unmarshal(item.Payload, &payload); err != nil {
				continue
			}
			switch payload.Type {
			case "function_call":
				req.FunctionCallCount++
				if toolName := strings.TrimSpace(payload.Name); toolName != "" {
					toolCalls[toolName]++
					if callID := strings.TrimSpace(payload.CallID); callID != "" {
						callToolByID[callID] = toolName
					}
				}
			case "function_call_output":
				wallTimeMS := codexFunctionCallOutputWallTimeMS(payload.Output)
				toolName := strings.TrimSpace(callToolByID[strings.TrimSpace(payload.CallID)])
				if toolName == "" && wallTimeMS > 0 {
					toolName = "unknown"
				}
				if toolName != "" && wallTimeMS > 0 {
					toolWallTimesMS[toolName] += wallTimeMS
					req.ToolWallTimeMS += wallTimeMS
				}
				if codexFunctionCallOutputHasError(payload.Output) {
					req.ToolErrorCount++
					if toolName == "" {
						toolName = "unknown"
					}
					toolErrors[toolName]++
				}
			case "message":
				switch payload.Role {
				case "user":
					for _, content := range payload.Content {
						if content.Type != "input_text" {
							continue
						}
						if appendRawQuery(seenQueries, &req.RawQueries, content.Text) {
							captureFirstCodexTimestamp(&firstMeaningfulUserAt, lineTimestamp, hasLineTimestamp)
						}
					}
				case "assistant":
					for _, content := range payload.Content {
						if content.Type != "output_text" {
							continue
						}
						if appendAssistantResponse(seenResponses, &req.AssistantResponses, content.Text) {
							captureFirstCodexTimestamp(&firstAssistantResponseAt, lineTimestamp, hasLineTimestamp)
						}
					}
				}
			case "reasoning":
				for _, summary := range codexResponseItemReasoningSummaries(payload) {
					appendReasoningSummary(seenReasoningSummaries, &req.ReasoningSummaries, summary)
				}
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
	if !firstMeaningfulUserAt.IsZero() && !firstAssistantResponseAt.IsZero() && firstAssistantResponseAt.After(firstMeaningfulUserAt) {
		req.FirstResponseLatencyMS = int(firstAssistantResponseAt.Sub(firstMeaningfulUserAt) / time.Millisecond)
	}
	if !earliestTimestamp.IsZero() && !latestTimestamp.IsZero() && !latestTimestamp.Before(earliestTimestamp) {
		req.SessionDurationMS = int(latestTimestamp.Sub(earliestTimestamp) / time.Millisecond)
	}
	req.ToolCalls = cloneToolCalls(toolCalls)
	req.ToolErrors = cloneToolCalls(toolErrors)
	req.ToolWallTimesMS = cloneToolCalls(toolWallTimesMS)

	if len(req.RawQueries) == 0 {
		return request.SessionSummaryReq{}, fmt.Errorf("no raw user queries found in Codex session %s", path)
	}
	return req, nil
}

func appendRawQuery(seen map[string]struct{}, dst *[]string, raw string) bool {
	query := normalizeCodexUserMessage(raw)
	if query == "" {
		return false
	}
	appendUniqueString(seen, dst, query)
	return true
}

func appendAssistantResponse(seen map[string]struct{}, dst *[]string, raw string) bool {
	text := normalizeCodexSessionText(raw)
	if text == "" {
		return false
	}
	appendUniqueString(seen, dst, text)
	return true
}

func appendReasoningSummary(seen map[string]struct{}, dst *[]string, raw string) bool {
	text := normalizeCodexReasoningSummary(raw)
	if text == "" {
		return false
	}
	appendUniqueString(seen, dst, text)
	return true
}

func appendUniqueSessionText(seen map[string]struct{}, dst *[]string, raw string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return
	}
	appendUniqueString(seen, dst, text)
}

func appendUniqueString(seen map[string]struct{}, dst *[]string, text string) {
	if _, ok := seen[text]; ok {
		return
	}
	seen[text] = struct{}{}
	*dst = append(*dst, text)
}

func captureFirstCodexTimestamp(dst *time.Time, ts time.Time, ok bool) {
	if !ok || ts.IsZero() {
		return
	}
	if dst.IsZero() || ts.Before(*dst) {
		*dst = ts
	}
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

	raw = stripCodexTaggedBlock(raw, "<environment_context>", "</environment_context>")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	cleaned := make([]string, 0, len(lines))
	skipInstructions := false
	skipOpenTabs := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# AGENTS.md instructions"):
			skipInstructions = true
			continue
		case skipInstructions:
			if strings.EqualFold(line, "</INSTRUCTIONS>") {
				skipInstructions = false
			}
			continue
		case strings.EqualFold(line, "# Context from my IDE setup:"),
			strings.EqualFold(line, "# Context from my IDE setup"):
			continue
		case strings.EqualFold(line, "## Open tabs:"),
			strings.EqualFold(line, "## Open tabs"):
			skipOpenTabs = true
			continue
		case strings.HasPrefix(line, "## My request for Codex"):
			skipOpenTabs = false
			continue
		case skipOpenTabs:
			if strings.HasPrefix(line, "## ") {
				skipOpenTabs = false
			} else {
				continue
			}
		}
		if strings.EqualFold(line, "<image>") || strings.EqualFold(line, "</image>") {
			continue
		}
		cleaned = append(cleaned, line)
	}

	raw = strings.Join(cleaned, "\n")
	for strings.Contains(raw, "\n\n") {
		raw = strings.ReplaceAll(raw, "\n\n", "\n")
	}
	return strings.TrimSpace(raw)
}

func normalizeCodexSessionText(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	for strings.Contains(raw, "\n\n") {
		raw = strings.ReplaceAll(raw, "\n\n", "\n")
	}
	return strings.TrimSpace(raw)
}

func normalizeCodexReasoningSummary(raw string) string {
	text := normalizeCodexSessionText(raw)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "*`_ ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

func codexResponseItemReasoningSummaries(payload codexResponseItemPayload) []string {
	items := make([]string, 0, len(payload.Summary)+len(payload.Content))
	appendReasoningText := func(contents []codexResponseContent) {
		for _, content := range contents {
			switch strings.TrimSpace(content.Type) {
			case "summary_text", "reasoning_text", "output_text":
				if text := strings.TrimSpace(content.Text); text != "" {
					items = append(items, text)
				}
			}
		}
	}
	appendReasoningText(payload.Summary)
	appendReasoningText(payload.Content)
	return items
}

func stripCodexTaggedBlock(raw, openTag, closeTag string) string {
	for {
		start := strings.Index(raw, openTag)
		if start < 0 {
			return raw
		}
		end := strings.Index(raw[start+len(openTag):], closeTag)
		if end < 0 {
			return strings.TrimSpace(raw[:start])
		}
		end += start + len(openTag) + len(closeTag)
		raw = raw[:start] + raw[end:]
	}
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

func codexFunctionCallOutputHasError(raw any) bool {
	text := strings.TrimSpace(fmt.Sprint(raw))
	if text == "" || text == "<nil>" {
		return false
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Exit code:") {
			continue
		}
		code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Exit code:")))
		if err != nil {
			return false
		}
		return code != 0
	}
	return false
}

func codexFunctionCallOutputWallTimeMS(raw any) int {
	text := strings.TrimSpace(fmt.Sprint(raw))
	if text == "" || text == "<nil>" {
		return 0
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Wall time:") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "Wall time:")))
		if len(fields) == 0 {
			return 0
		}
		value, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return 0
		}
		unit := "seconds"
		if len(fields) > 1 {
			unit = strings.ToLower(fields[1])
		}
		switch {
		case strings.HasPrefix(unit, "ms"):
			return int(value)
		case strings.HasPrefix(unit, "s"), strings.HasPrefix(unit, "second"):
			return int(value * 1000)
		default:
			return int(value * 1000)
		}
	}
	return 0
}

func cloneToolCalls(input map[string]int) map[string]int {
	if len(input) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(input))
	for k, v := range input {
		if strings.TrimSpace(k) == "" || v <= 0 {
			continue
		}
		out[k] = v
	}
	return out
}
