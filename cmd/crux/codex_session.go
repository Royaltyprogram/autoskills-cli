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
	Source        string `json:"source"`
	Originator    string `json:"originator"`
	CWD           string `json:"cwd"`
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

type codexSessionFile struct {
	path    string
	modTime time.Time
	size    int64
}

type sessionUploadCursor struct {
	TailPath    string    `json:"tail_path,omitempty"`
	TailModTime time.Time `json:"tail_mod_time,omitempty"`
	TailSize    int64     `json:"tail_size,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
}

type codexSessionClassification string

const (
	codexSessionClassificationPrimary          codexSessionClassification = "primary"
	codexSessionClassificationUtilityTitle     codexSessionClassification = "utility_title_generation"
	codexSessionClassificationUtilityLocalPlan codexSessionClassification = "utility_local_change_plan"
)

type codexSessionUtilityError struct {
	Path           string
	Classification codexSessionClassification
}

type codexSessionSkipError struct {
	Path   string
	Reason string
}

type codexParsedSession struct {
	req               request.SessionSummaryReq
	path              string
	startedAt         time.Time
	completedAt       time.Time
	tailPath          string
	tailModTime       time.Time
	tailSize          int64
	cwd               string
	firstQuery        string
	classification    codexSessionClassification
	turnAborted       bool
	hasMeaningfulWork bool
}

func cloneSessionUploadCursor(cursor *sessionUploadCursor) *sessionUploadCursor {
	if cursor == nil {
		return nil
	}
	cloned := *cursor
	if !cloned.TailModTime.IsZero() {
		cloned.TailModTime = cloned.TailModTime.UTC()
	}
	if strings.TrimSpace(cloned.TailPath) == "" && cloned.TailModTime.IsZero() && cloned.TailSize == 0 && strings.TrimSpace(cloned.SessionID) == "" {
		return nil
	}
	return &cloned
}

func (e *codexSessionUtilityError) Error() string {
	return fmt.Sprintf("Codex session %s is an internal %s rollout and is skipped from analytics uploads", e.Path, e.Classification)
}

func (e *codexSessionSkipError) Error() string {
	return fmt.Sprintf("Codex session %s is skipped from analytics uploads: %s", e.Path, e.Reason)
}

func isCodexUtilitySessionError(err error) bool {
	var utilityErr *codexSessionUtilityError
	return errors.As(err, &utilityErr)
}

func isCodexSkippableSessionError(err error) bool {
	if isCodexUtilitySessionError(err) {
		return true
	}
	var skipErr *codexSessionSkipError
	return errors.As(err, &skipErr)
}

func listCodexSessionFiles(codexHome string) ([]codexSessionFile, error) {
	root, err := codexHomePath(codexHome)
	if err != nil {
		return nil, err
	}

	sessionsRoot := filepath.Join(root, "sessions")
	files := make([]codexSessionFile, 0, 64)

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
		files = append(files, codexSessionFile{path: path, modTime: info.ModTime()})
		files[len(files)-1].size = info.Size()
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
	return files, nil
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
	parsed, err := collectCodexParsedSession(path, tool)
	if err != nil {
		return request.SessionSummaryReq{}, err
	}
	if parsed.classification != codexSessionClassificationPrimary {
		return request.SessionSummaryReq{}, &codexSessionUtilityError{
			Path:           path,
			Classification: parsed.classification,
		}
	}
	return parsed.req, nil
}

func collectCodexParsedSession(path, tool string) (codexParsedSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return codexParsedSession{}, err
	}
	defer file.Close()

	req := request.SessionSummaryReq{Tool: tool}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	seenQueries := map[string]struct{}{}
	seenModels := map[string]struct{}{}
	seenResponses := map[string]struct{}{}
	seenReasoningSummaries := map[string]struct{}{}
	seenRawUserMessages := map[string]struct{}{}
	toolCalls := make(map[string]int)
	toolErrors := make(map[string]int)
	toolWallTimesMS := make(map[string]int)
	callToolByID := make(map[string]string)
	var earliestTimestamp time.Time
	var latestTimestamp time.Time
	var firstMeaningfulUserAt time.Time
	var firstAssistantResponseAt time.Time
	rawUserMessages := make([]string, 0, 4)
	meta := codexSessionMetaPayload{}
	turnAborted := false
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var item codexSessionLine
		if err := json.Unmarshal(line, &item); err != nil {
			return codexParsedSession{}, fmt.Errorf("parse Codex session line %d: %w", lineNo, err)
		}
		lineTimestamp, hasLineTimestamp := parseCodexTimestamp(item.Timestamp)
		if hasLineTimestamp && (earliestTimestamp.IsZero() || lineTimestamp.Before(earliestTimestamp)) {
			earliestTimestamp = lineTimestamp
		}
		if hasLineTimestamp && lineTimestamp.After(latestTimestamp) {
			latestTimestamp = lineTimestamp
		}

		switch item.Type {
		case "":
			var payload codexSessionMetaPayload
			if err := json.Unmarshal(line, &payload); err != nil || !looksLikeCodexLegacySessionMeta(payload) {
				continue
			}
			applyCodexSessionMeta(&req, &meta, payload, &earliestTimestamp, &latestTimestamp)
		case "session_meta":
			var payload codexSessionMetaPayload
			if err := json.Unmarshal(codexSessionPayload(line, item.Payload), &payload); err != nil {
				continue
			}
			applyCodexSessionMeta(&req, &meta, payload, &earliestTimestamp, &latestTimestamp)
		case "turn_context":
			var payload codexTurnContextPayload
			if err := json.Unmarshal(codexSessionPayload(line, item.Payload), &payload); err != nil {
				continue
			}
			appendUniqueSessionText(seenModels, &req.Models, payload.Model)
		case "event_msg", "user_message", "agent_message", "token_count", "turn_aborted":
			var payload codexEventMsgPayload
			if err := json.Unmarshal(codexSessionPayload(line, item.Payload), &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.Type) == "" {
				payload.Type = item.Type
			}
			switch payload.Type {
			case "user_message":
				appendUniqueSessionText(seenRawUserMessages, &rawUserMessages, payload.Message)
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
			case "turn_aborted":
				turnAborted = true
			}
		case "response_item", "message", "reasoning", "function_call", "function_call_output":
			var payload codexResponseItemPayload
			if err := json.Unmarshal(codexSessionPayload(line, item.Payload), &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.Type) == "" {
				payload.Type = item.Type
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
						appendUniqueSessionText(seenRawUserMessages, &rawUserMessages, content.Text)
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
		return codexParsedSession{}, err
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
		return codexParsedSession{}, &codexSessionSkipError{
			Path:   path,
			Reason: "no_raw_user_queries",
		}
	}
	classification := classifyCodexSession(rawUserMessages, req.RawQueries, meta)
	firstQuery := ""
	if len(req.RawQueries) > 0 {
		firstQuery = req.RawQueries[0]
	}
	return codexParsedSession{
		req:               req,
		path:              path,
		startedAt:         earliestTimestamp,
		completedAt:       latestTimestamp,
		cwd:               strings.TrimSpace(meta.CWD),
		firstQuery:        strings.TrimSpace(firstQuery),
		classification:    classification,
		turnAborted:       turnAborted,
		hasMeaningfulWork: req.FunctionCallCount > 0 || len(req.AssistantResponses) > 0 || len(req.ReasoningSummaries) > 0,
	}, nil
}

func codexSessionPayload(line, payload []byte) []byte {
	if len(bytes.TrimSpace(payload)) > 0 {
		return payload
	}
	return line
}

func looksLikeCodexLegacySessionMeta(payload codexSessionMetaPayload) bool {
	return strings.TrimSpace(payload.ID) != "" ||
		strings.TrimSpace(payload.Timestamp) != "" ||
		strings.TrimSpace(payload.ModelProvider) != "" ||
		strings.TrimSpace(payload.Source) != "" ||
		strings.TrimSpace(payload.Originator) != "" ||
		strings.TrimSpace(payload.CWD) != ""
}

func applyCodexSessionMeta(req *request.SessionSummaryReq, meta *codexSessionMetaPayload, payload codexSessionMetaPayload, earliestTimestamp, latestTimestamp *time.Time) {
	*meta = payload
	if req.SessionID == "" {
		req.SessionID = strings.TrimSpace(payload.ID)
	}
	if req.ModelProvider == "" {
		req.ModelProvider = strings.TrimSpace(payload.ModelProvider)
	}
	if ts, ok := parseCodexTimestamp(payload.Timestamp); ok && ((*earliestTimestamp).IsZero() || ts.Before(*earliestTimestamp)) {
		*earliestTimestamp = ts
	}
	if ts, ok := parseCodexTimestamp(payload.Timestamp); ok && ts.After(*latestTimestamp) {
		*latestTimestamp = ts
	}
}

func classifyCodexSession(rawUserMessages, normalizedQueries []string, meta codexSessionMetaPayload) codexSessionClassification {
	joinedRaw := strings.ToLower(strings.Join(rawUserMessages, "\n"))
	joinedQueries := strings.ToLower(strings.Join(normalizedQueries, "\n"))
	joined := strings.TrimSpace(joinedRaw + "\n" + joinedQueries)
	if joined == "" {
		return codexSessionClassificationPrimary
	}

	if isCodexTitleGenerationRollout(joined) {
		return codexSessionClassificationUtilityTitle
	}
	if isCodexLocalPlanRollout(joined, meta) {
		return codexSessionClassificationUtilityLocalPlan
	}
	return codexSessionClassificationPrimary
}

func isCodexTitleGenerationRollout(text string) bool {
	if !strings.Contains(text, "generate a concise ui title") || !strings.Contains(text, "return only the title") {
		return false
	}
	titleSignals := 0
	for _, marker := range []string{
		"your job is to provide a short title for a task",
		"task that will be created from that prompt",
		"if the task includes a ticket reference",
		"generate a clear, informative task title",
		"do not respond to the user, answer questions, or attempt to solve the problem; just write a title",
		"\nuser prompt:\n",
		"\ntask:\n",
	} {
		if strings.Contains(text, marker) {
			titleSignals++
		}
	}
	return titleSignals >= 2
}

func isCodexLocalPlanRollout(text string, meta codexSessionMetaPayload) bool {
	if !strings.Contains(text, "you are applying an approved local change plan.") {
		return false
	}
	if !strings.Contains(text, "approved files:") {
		return false
	}
	if !strings.Contains(text, "after applying the changes, respond strictly as json matching") {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(meta.Source))
	originator := strings.ToLower(strings.TrimSpace(meta.Originator))
	if source == "exec" || originator == "codex_sdk_ts" || originator == "codex_sdk_js" {
		return true
	}
	return true
}

func coalesceCodexParsedSessions(items []codexParsedSession) []codexParsedSession {
	if len(items) == 0 {
		return nil
	}
	merged := make([]codexParsedSession, 0, len(items))
	for _, item := range items {
		if len(merged) == 0 {
			merged = append(merged, item)
			continue
		}
		last := &merged[len(merged)-1]
		if shouldCoalesceCodexParsedSessions(*last, item) {
			*last = mergeCodexParsedSessions(*last, item)
			continue
		}
		merged = append(merged, item)
	}
	return merged
}

func shouldCoalesceCodexParsedSessions(left, right codexParsedSession) bool {
	if left.classification != codexSessionClassificationPrimary || right.classification != codexSessionClassificationPrimary {
		return false
	}
	if left.firstQuery == "" || right.firstQuery == "" || left.firstQuery != right.firstQuery {
		return false
	}
	if left.cwd == "" || right.cwd == "" || left.cwd != right.cwd {
		return false
	}
	leftEnd := firstNonZeroTime(left.completedAt, left.startedAt)
	rightStart := firstNonZeroTime(right.startedAt, right.completedAt)
	if leftEnd.IsZero() || rightStart.IsZero() {
		return false
	}
	gap := rightStart.Sub(leftEnd)
	if gap < 0 || gap > 2*time.Minute {
		return false
	}
	return left.turnAborted || right.turnAborted || !left.hasMeaningfulWork || !right.hasMeaningfulWork
}

func mergeCodexParsedSessions(left, right codexParsedSession) codexParsedSession {
	dominant := right
	if left.hasMeaningfulWork && !right.hasMeaningfulWork {
		dominant = left
	}
	req := dominant.req
	req.SessionID = mergedCodexSessionID(left.req.SessionID, right.req.SessionID, dominant.req.SessionID)
	req.RawQueries = mergeCodexStringLists(left.req.RawQueries, right.req.RawQueries)
	req.AssistantResponses = mergeCodexStringLists(left.req.AssistantResponses, right.req.AssistantResponses)
	req.ReasoningSummaries = mergeCodexStringLists(left.req.ReasoningSummaries, right.req.ReasoningSummaries)
	req.Models = mergeCodexStringLists(left.req.Models, right.req.Models)
	req.TokenIn = left.req.TokenIn + right.req.TokenIn
	req.TokenOut = left.req.TokenOut + right.req.TokenOut
	req.CachedInputTokens = left.req.CachedInputTokens + right.req.CachedInputTokens
	req.ReasoningOutputTokens = left.req.ReasoningOutputTokens + right.req.ReasoningOutputTokens
	req.FunctionCallCount = left.req.FunctionCallCount + right.req.FunctionCallCount
	req.ToolErrorCount = left.req.ToolErrorCount + right.req.ToolErrorCount
	req.ToolWallTimeMS = left.req.ToolWallTimeMS + right.req.ToolWallTimeMS
	req.ToolCalls = mergeCodexIntMaps(left.req.ToolCalls, right.req.ToolCalls)
	req.ToolErrors = mergeCodexIntMaps(left.req.ToolErrors, right.req.ToolErrors)
	req.ToolWallTimesMS = mergeCodexIntMaps(left.req.ToolWallTimesMS, right.req.ToolWallTimesMS)
	req.FirstResponseLatencyMS = mergeCodexLatencyMS(left.req.FirstResponseLatencyMS, right.req.FirstResponseLatencyMS)

	startedAt := minNonZeroTime(left.startedAt, right.startedAt)
	completedAt := maxTime(left.completedAt, right.completedAt)
	if startedAt.IsZero() {
		startedAt = minNonZeroTime(left.req.Timestamp, right.req.Timestamp)
	}
	if completedAt.IsZero() {
		completedAt = maxTime(left.req.Timestamp, right.req.Timestamp)
	}
	if !startedAt.IsZero() && !completedAt.IsZero() && !completedAt.Before(startedAt) {
		req.SessionDurationMS = int(completedAt.Sub(startedAt) / time.Millisecond)
	}
	req.Timestamp = maxTime(left.req.Timestamp, right.req.Timestamp)

	firstQuery := ""
	if len(req.RawQueries) > 0 {
		firstQuery = req.RawQueries[0]
	}
	return codexParsedSession{
		req:               req,
		path:              dominant.path,
		startedAt:         startedAt,
		completedAt:       completedAt,
		tailPath:          firstNonEmpty(strings.TrimSpace(right.tailPath), strings.TrimSpace(left.tailPath)),
		tailModTime:       firstNonZeroTime(right.tailModTime, left.tailModTime),
		tailSize:          firstNonZeroInt64(right.tailSize, left.tailSize),
		cwd:               firstNonEmpty(strings.TrimSpace(left.cwd), strings.TrimSpace(right.cwd)),
		firstQuery:        strings.TrimSpace(firstQuery),
		classification:    codexSessionClassificationPrimary,
		turnAborted:       left.turnAborted || right.turnAborted,
		hasMeaningfulWork: left.hasMeaningfulWork || right.hasMeaningfulWork,
	}
}

func mergeCodexStringLists(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	out := make([]string, 0, len(left)+len(right))
	for _, item := range append(append([]string{}, left...), right...) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func mergeCodexIntMaps(left, right map[string]int) map[string]int {
	if len(left) == 0 && len(right) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(left)+len(right))
	for key, value := range left {
		out[key] += value
	}
	for key, value := range right {
		out[key] += value
	}
	return out
}

func mergeCodexLatencyMS(left, right int) int {
	switch {
	case left <= 0:
		return right
	case right <= 0:
		return left
	case left < right:
		return left
	default:
		return right
	}
}

// Keep the earliest session ID stable so a later retry/coalesced upload can overwrite
// the already-uploaded logical session instead of creating a second session record.
func mergedCodexSessionID(leftID, rightID, dominantID string) string {
	leftID = strings.TrimSpace(leftID)
	rightID = strings.TrimSpace(rightID)
	dominantID = strings.TrimSpace(dominantID)
	return firstNonEmpty(leftID, rightID, dominantID)
}

func minNonZeroTime(left, right time.Time) time.Time {
	switch {
	case left.IsZero():
		return right
	case right.IsZero():
		return left
	case left.Before(right):
		return left
	default:
		return right
	}
}

func maxTime(left, right time.Time) time.Time {
	switch {
	case left.IsZero():
		return right
	case right.IsZero():
		return left
	case left.After(right):
		return left
	default:
		return right
	}
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func (s codexParsedSession) uploadCursor() *sessionUploadCursor {
	tailPath := strings.TrimSpace(s.tailPath)
	if tailPath == "" {
		return nil
	}
	return &sessionUploadCursor{
		TailPath:    tailPath,
		TailModTime: s.tailModTime.UTC(),
		TailSize:    s.tailSize,
		SessionID:   strings.TrimSpace(s.req.SessionID),
	}
}

func (s codexParsedSession) isAfterCursor(cursor *sessionUploadCursor) bool {
	cursor = cloneSessionUploadCursor(cursor)
	if cursor == nil {
		return true
	}

	sessionModTime := s.tailModTime.UTC()
	switch {
	case sessionModTime.After(cursor.TailModTime):
		return true
	case sessionModTime.Before(cursor.TailModTime):
		return false
	}

	sessionPath := strings.TrimSpace(s.tailPath)
	cursorPath := strings.TrimSpace(cursor.TailPath)
	switch {
	case sessionPath > cursorPath:
		return true
	case sessionPath < cursorPath:
		return false
	}

	switch {
	case s.tailSize > cursor.TailSize:
		return true
	case s.tailSize < cursor.TailSize:
		return false
	}

	return strings.TrimSpace(s.req.SessionID) != strings.TrimSpace(cursor.SessionID)
}

func selectCodexParsedSessionsAfterCursor(items []codexParsedSession, cursor *sessionUploadCursor) []codexParsedSession {
	cursor = cloneSessionUploadCursor(cursor)
	if cursor == nil {
		return append([]codexParsedSession(nil), items...)
	}
	selected := make([]codexParsedSession, 0, len(items))
	for _, item := range items {
		if item.isAfterCursor(cursor) {
			selected = append(selected, item)
		}
	}
	return selected
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
