package main

import (
	"os"
	"sort"
	"testing"
	"time"
)

type realDataDuplicatePair struct {
	left  codexParsedSession
	right codexParsedSession
	gap   time.Duration
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
