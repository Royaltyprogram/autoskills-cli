package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

type realDataDuplicatePair struct {
	left  codexParsedSession
	right codexParsedSession
	gap   time.Duration
}

type realDataFunctionCallOutputStats struct {
	totalOutputs            int
	wallRecognized          int
	explicitExitCodes       int
	explicitErrors          int
	heuristicErrors         int
	neutralOutputs          int
	unknownOutputs          int
	neutralPatternCounts    map[string]int
	neutralPatternSamples   map[string]string
	remainingPatternCounts  map[string]int
	remainingPatternSamples map[string]string
}

func TestAnalyzeRealCodexSessions(t *testing.T) {
	if os.Getenv("CRUX_REALDATA_ANALYZE") != "1" {
		t.Skip("set CRUX_REALDATA_ANALYZE=1 to scan the real local Codex session archive")
	}

	files, err := listCodexSessionFiles("")
	if err != nil {
		t.Fatal(err)
	}

	primary := make([]codexParsedSession, 0, len(files))
	classificationCounts := map[codexSessionClassification]int{}
	skipped := 0
	for _, file := range files {
		parsed, err := collectCodexParsedSession(file.path, "codex")
		if err != nil {
			if isCodexSkippableSessionError(err) {
				skipped++
				continue
			}
			t.Fatalf("parse %s: %v", file.path, err)
		}
		classificationCounts[parsed.classification]++
		if parsed.classification == codexSessionClassificationPrimary {
			primary = append(primary, parsed)
		}
	}

	coalesced := coalesceCodexParsedSessions(primary)
	t.Logf("files=%d primary=%d coalesced=%d skipped=%d utility_title=%d utility_plan=%d merged=%d",
		len(files),
		len(primary),
		len(coalesced),
		skipped,
		classificationCounts[codexSessionClassificationUtilityTitle],
		classificationCounts[codexSessionClassificationUtilityLocalPlan],
		len(primary)-len(coalesced),
	)

	duplicatesBefore := adjacentDuplicatePairs(primary, 2*time.Minute)
	duplicatesAfter := adjacentDuplicatePairs(coalesced, 2*time.Minute)
	t.Logf("adjacent_duplicates_before=%d adjacent_duplicates_after=%d", len(duplicatesBefore), len(duplicatesAfter))

	for idx, pair := range headPairs(duplicatesAfter, 12) {
		t.Logf("remaining_duplicate_%02d gap=%s left=%s right=%s query=%q",
			idx+1,
			pair.gap,
			pair.left.path,
			pair.right.path,
			pair.left.firstQuery,
		)
	}

	outputStats, err := analyzeRealFunctionCallOutputs(files)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("function_call_output_coverage total=%d wall_recognized=%d explicit_exit_codes=%d explicit_errors=%d heuristic_errors=%d neutral=%d unknown=%d",
		outputStats.totalOutputs,
		outputStats.wallRecognized,
		outputStats.explicitExitCodes,
		outputStats.explicitErrors,
		outputStats.heuristicErrors,
		outputStats.neutralOutputs,
		outputStats.unknownOutputs,
	)

	for idx, pattern := range headStringIntPairs(sortStringIntPairs(outputStats.neutralPatternCounts), 8) {
		t.Logf("neutral_output_pattern_%02d count=%d sample=%q path=%s",
			idx+1,
			pattern.value,
			pattern.key,
			outputStats.neutralPatternSamples[pattern.key],
		)
	}

	for idx, pattern := range headStringIntPairs(sortStringIntPairs(outputStats.remainingPatternCounts), 12) {
		t.Logf("remaining_output_pattern_%02d count=%d sample=%q path=%s",
			idx+1,
			pattern.value,
			pattern.key,
			outputStats.remainingPatternSamples[pattern.key],
		)
	}
	if outputStats.unknownOutputs != 0 {
		t.Fatalf("unrecognized function_call_output patterns remain: %d", outputStats.unknownOutputs)
	}
}

func TestRealDataOutputPatternKeyNormalizesKnownPatterns(t *testing.T) {
	requireCases := map[string]string{
		"Plan updated": "plan_updated",
		"Chunk ID: abc123\nProcess running with session ID 42\nOutput:\n": "process_running_with_session_id",
		"aborted by user after 4.2s":                                      "aborted_by_user",
	}
	for raw, want := range requireCases {
		if got := realDataOutputPatternKey(raw); got != want {
			t.Fatalf("realDataOutputPatternKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

func adjacentDuplicatePairs(items []codexParsedSession, maxGap time.Duration) []realDataDuplicatePair {
	if len(items) < 2 {
		return nil
	}
	pairs := make([]realDataDuplicatePair, 0)
	for idx := 0; idx < len(items)-1; idx++ {
		left := items[idx]
		right := items[idx+1]
		if left.classification != codexSessionClassificationPrimary || right.classification != codexSessionClassificationPrimary {
			continue
		}
		if left.cwd == "" || right.cwd == "" || left.cwd != right.cwd {
			continue
		}
		if left.firstQuery == "" || right.firstQuery == "" || left.firstQuery != right.firstQuery {
			continue
		}
		leftEnd := firstNonZeroTime(left.completedAt, left.startedAt, left.req.Timestamp)
		rightStart := firstNonZeroTime(right.startedAt, right.completedAt, right.req.Timestamp)
		if leftEnd.IsZero() || rightStart.IsZero() {
			continue
		}
		gap := rightStart.Sub(leftEnd)
		if gap < 0 || gap > maxGap {
			continue
		}
		pairs = append(pairs, realDataDuplicatePair{left: left, right: right, gap: gap})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].gap == pairs[j].gap {
			return pairs[i].left.path < pairs[j].left.path
		}
		return pairs[i].gap < pairs[j].gap
	})
	return pairs
}

func headPairs(items []realDataDuplicatePair, limit int) []realDataDuplicatePair {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}

type stringIntPair struct {
	key   string
	value int
}

func analyzeRealFunctionCallOutputs(files []codexSessionFile) (realDataFunctionCallOutputStats, error) {
	stats := realDataFunctionCallOutputStats{
		neutralPatternCounts:    map[string]int{},
		neutralPatternSamples:   map[string]string{},
		remainingPatternCounts:  map[string]int{},
		remainingPatternSamples: map[string]string{},
	}

	for _, file := range files {
		handle, err := os.Open(file.path)
		if err != nil {
			return realDataFunctionCallOutputStats{}, err
		}

		scanner := bufio.NewScanner(handle)
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}

			var item codexSessionLine
			if err := json.Unmarshal(line, &item); err != nil {
				continue
			}
			if item.Type != "response_item" && item.Type != "function_call_output" {
				continue
			}

			var payload codexResponseItemPayload
			if err := json.Unmarshal(codexSessionPayload(line, item.Payload), &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.Type) == "" {
				payload.Type = item.Type
			}
			if payload.Type != "function_call_output" {
				continue
			}

			stats.totalOutputs++
			info := codexFunctionCallOutputInfoFromRaw(payload.Output)
			if info.wallTimeMS > 0 {
				stats.wallRecognized++
			}
			if info.exitCode != nil {
				stats.explicitExitCodes++
				if *info.exitCode != 0 {
					stats.explicitErrors++
				}
			} else if info.hasError {
				stats.heuristicErrors++
			}
			if info.isNeutral {
				stats.neutralOutputs++
				pattern := realDataOutputPatternKey(info.text)
				stats.neutralPatternCounts[pattern]++
				if _, ok := stats.neutralPatternSamples[pattern]; !ok {
					stats.neutralPatternSamples[pattern] = file.path
				}
			}
			if info.recognized {
				continue
			}

			stats.unknownOutputs++
			pattern := realDataOutputPatternKey(info.text)
			stats.remainingPatternCounts[pattern]++
			if _, ok := stats.remainingPatternSamples[pattern]; !ok {
				stats.remainingPatternSamples[pattern] = file.path
			}
		}
		if err := scanner.Err(); err != nil {
			handle.Close()
			return realDataFunctionCallOutputStats{}, err
		}
		handle.Close()
	}

	return stats, nil
}

func realDataOutputPatternKey(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "<empty>"
	}

	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "process running with session id"):
		return "process_running_with_session_id"
	case strings.Contains(lower, "plan updated"):
		return "plan_updated"
	case strings.Contains(lower, "aborted by user"):
		return "aborted_by_user"
	case strings.Contains(lower, "rejected by user"):
		return "rejected_by_user"
	case strings.Contains(lower, "failed in sandbox"):
		return "failed_in_sandbox"
	case strings.Contains(lower, "missing required parameter"):
		return "missing_required_parameter"
	case strings.Contains(lower, "missing parameter"):
		return "missing_parameter"
	case strings.Contains(lower, "missing param"):
		return "missing_param"
	case strings.Contains(lower, "error: failed to read file to update"):
		return "failed_to_read_file_to_update"
	case strings.Contains(lower, "error: failed to find expected lines"):
		return "failed_to_find_expected_lines"
	case strings.Contains(lower, "error: invalid hunk at line"):
		return "invalid_hunk"
	}

	line := text
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	return line
}

func sortStringIntPairs(items map[string]int) []stringIntPair {
	if len(items) == 0 {
		return nil
	}
	pairs := make([]stringIntPair, 0, len(items))
	for key, value := range items {
		if strings.TrimSpace(key) == "" || value <= 0 {
			continue
		}
		pairs = append(pairs, stringIntPair{key: key, value: value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].value == pairs[j].value {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].value > pairs[j].value
	})
	return pairs
}

func headStringIntPairs(items []stringIntPair, limit int) []stringIntPair {
	if len(items) <= limit {
		return items
	}
	return items[:limit]
}
