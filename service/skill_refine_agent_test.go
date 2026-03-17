package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
)

func TestParseSkillRefineResponseValidJSON(t *testing.T) {
	originals := []compiledSkillCategory{
		{
			Category:     "clarification",
			Title:        "Clarify Before Building",
			Rules:        []string{"Try asking for missing constraints before coding."},
			AntiPatterns: []string{"Starts implementation before confirming requirements."},
			Confidence:   0.85,
			ReportIDs:    []string{"rep-1"},
		},
		{
			Category:     "validation",
			Title:        "Validate Before Declaring Done",
			Rules:        []string{"Consider running tests after your changes."},
			AntiPatterns: []string{"Skips validation when under time pressure."},
			Confidence:   0.78,
			ReportIDs:    []string{"rep-2"},
		},
	}

	raw := `[
		{
			"category": "clarification",
			"rules": ["When requirements are incomplete, ask the user for the missing constraints before writing any code."],
			"anti_patterns": ["Do not begin implementation until all stated requirements have been confirmed with the user."]
		},
		{
			"category": "validation",
			"rules": ["After making changes, run the relevant test suite before reporting completion."],
			"anti_patterns": ["Never skip test validation regardless of time constraints."]
		}
	]`

	result, err := parseSkillRefineResponse(raw, originals)
	require.NoError(t, err)
	require.Len(t, result, 2)

	require.Equal(t, "clarification", result[0].Category)
	require.Equal(t, "Clarify Before Building", result[0].Title)
	require.Equal(t, 0.85, result[0].Confidence)
	require.Equal(t, []string{"rep-1"}, result[0].ReportIDs)
	require.Len(t, result[0].Rules, 1)
	require.Contains(t, result[0].Rules[0], "ask the user")

	require.Equal(t, "validation", result[1].Category)
	require.Len(t, result[1].AntiPatterns, 1)
	require.Contains(t, result[1].AntiPatterns[0], "Never skip")
}

func TestParseSkillRefineResponseStripsMarkdownFences(t *testing.T) {
	originals := []compiledSkillCategory{
		{
			Category:     "planning",
			Title:        "Plan Before Touching Code",
			Rules:        []string{"Outline a plan first."},
			AntiPatterns: []string{"Dives into code without a plan."},
		},
	}

	raw := "```json\n" + `[{"category":"planning","rules":["Before writing code, produce a step-by-step plan."],"anti_patterns":["Do not start editing files without an explicit plan."]}]` + "\n```"

	result, err := parseSkillRefineResponse(raw, originals)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "Before writing code, produce a step-by-step plan.", result[0].Rules[0])
}

func TestParseSkillRefineResponseCountMismatchReturnsError(t *testing.T) {
	originals := []compiledSkillCategory{
		{Category: "clarification", Rules: []string{"rule1"}, AntiPatterns: []string{"anti1"}},
		{Category: "validation", Rules: []string{"rule2"}, AntiPatterns: []string{"anti2"}},
	}

	raw := `[{"category":"clarification","rules":["rewritten"],"anti_patterns":["rewritten"]}]`

	_, err := parseSkillRefineResponse(raw, originals)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected 2")
}

func TestParseSkillRefineResponseInvalidJSONReturnsError(t *testing.T) {
	originals := []compiledSkillCategory{
		{Category: "clarification", Rules: []string{"rule1"}, AntiPatterns: []string{"anti1"}},
	}

	_, err := parseSkillRefineResponse("not json at all", originals)
	require.Error(t, err)
}

func TestParseSkillRefineResponseRuleCountMismatchKeepsOriginal(t *testing.T) {
	originals := []compiledSkillCategory{
		{
			Category:     "clarification",
			Rules:        []string{"rule1", "rule2"},
			AntiPatterns: []string{"anti1"},
		},
	}

	raw := `[{"category":"clarification","rules":["only one rewritten rule"],"anti_patterns":["rewritten anti"]}]`

	result, err := parseSkillRefineResponse(raw, originals)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, []string{"rule1", "rule2"}, result[0].Rules, "original rules preserved when count mismatches")
	require.Equal(t, []string{"rewritten anti"}, result[0].AntiPatterns, "anti-patterns updated when count matches")
}

func TestRefineCategoriesNilAgentReturnsOriginals(t *testing.T) {
	categories := []compiledSkillCategory{
		{Category: "clarification", Rules: []string{"ask first"}},
	}

	var agent *SkillRefineAgent
	result, err := agent.RefineCategories(context.Background(), categories)
	require.NoError(t, err)
	require.Equal(t, categories, result)
}

func TestRefineCategoriesEmptyAPIKeyReturnsOriginals(t *testing.T) {
	agent := &SkillRefineAgent{apiKey: ""}
	categories := []compiledSkillCategory{
		{Category: "clarification", Rules: []string{"ask first"}},
	}

	result, err := agent.RefineCategories(context.Background(), categories)
	require.NoError(t, err)
	require.Equal(t, categories, result)
}

func TestBuildSkillRefinePromptContainsCategoriesJSON(t *testing.T) {
	categories := []compiledSkillCategory{
		{
			Category:     "planning",
			Title:        "Plan Before Touching Code",
			Rules:        []string{"Outline steps before editing."},
			AntiPatterns: []string{"Jumps straight to code."},
		},
	}

	prompt, err := buildSkillRefinePrompt(categories)
	require.NoError(t, err)
	require.Contains(t, prompt, `"category": "planning"`)
	require.Contains(t, prompt, "Outline steps before editing.")
	require.Contains(t, prompt, "Jumps straight to code.")
	require.Contains(t, prompt, "polishing operating rules")
}

func TestSanitizeRefineLines(t *testing.T) {
	input := []string{"  valid rule  ", "", "  ", "another rule"}
	result := sanitizeRefineLines(input)
	require.Equal(t, []string{"valid rule", "another rule"}, result)
}

func TestDiffAndRefineCategoriesReusesUnchangedRules(t *testing.T) {
	// Simulate a previous version where "ask first" was refined to "Always ask first."
	prev := map[string]*previousCategoryRefineData{
		"clarification": {
			RuleMap:        map[string]string{"ask first": "Always ask the user first before editing code."},
			AntiPatternMap: map[string]string{"skips reqs": "Never skip confirming requirements."},
		},
	}

	categories := []compiledSkillCategory{
		{
			Category:              "clarification",
			Title:                 "Clarify Before Building",
			Rules:                 []string{"ask first", "confirm constraints"},
			AntiPatterns:          []string{"skips reqs"},
			PreRefineRules:        []string{"ask first", "confirm constraints"},
			PreRefineAntiPatterns: []string{"skips reqs"},
		},
	}

	// No agent provided — new rules should remain as raw text.
	result := diffAndRefineCategories(categories, prev, nil)
	require.Len(t, result, 1)
	// "ask first" reused from prev, "confirm constraints" kept as raw (no agent).
	require.Equal(t, []string{"Always ask the user first before editing code.", "confirm constraints"}, result[0].Rules)
	require.Equal(t, []string{"Never skip confirming requirements."}, result[0].AntiPatterns)
}

func TestDiffAndRefineCategoriesAllUnchangedSkipsLLM(t *testing.T) {
	prev := map[string]*previousCategoryRefineData{
		"validation": {
			RuleMap:        map[string]string{"run tests": "Run the full test suite after every change."},
			AntiPatternMap: map[string]string{"skip tests": "Never skip test validation."},
		},
	}

	categories := []compiledSkillCategory{
		{
			Category:              "validation",
			Title:                 "Validate Before Done",
			Rules:                 []string{"run tests"},
			AntiPatterns:          []string{"skip tests"},
			PreRefineRules:        []string{"run tests"},
			PreRefineAntiPatterns: []string{"skip tests"},
		},
	}

	// Even with a nil agent, all rules are reused.
	result := diffAndRefineCategories(categories, prev, nil)
	require.Len(t, result, 1)
	require.Equal(t, []string{"Run the full test suite after every change."}, result[0].Rules)
	require.Equal(t, []string{"Never skip test validation."}, result[0].AntiPatterns)
}

func TestExtractPreviousRefineMapFromVersion(t *testing.T) {
	manifest := `{
		"schema_version": "crux-skillset.v1",
		"bundle_name": "crux-personal-skillset",
		"project_id": "proj-1",
		"version": "v-abc123",
		"compiled_hash": "abc123",
		"generated_at": "2026-03-17T00:00:00Z",
		"documents": [
			{
				"path": "01-clarification.md",
				"category": "clarification",
				"title": "Clarify Before Building",
				"confidence": 0.85,
				"pre_refine_rules": ["ask first"],
				"pre_refine_anti_patterns": ["skips reqs"],
				"refined_rules": ["Always ask the user first."],
				"refined_anti_patterns": ["Never skip requirements."],
				"reusable_rule_mappings": [true],
				"reusable_anti_mappings": [true]
			}
		]
	}`

	version := &SkillSetVersion{
		Files: []SkillSetVersionFile{
			{Path: "00-manifest.json", Content: manifest},
			{Path: "01-clarification.md", Content: "# Clarify\n"},
		},
	}

	result := extractPreviousRefineMap(version)
	require.NotNil(t, result)
	require.Contains(t, result, "clarification")

	data := result["clarification"]
	require.Equal(t, "Always ask the user first.", data.RuleMap["ask first"])
	require.Equal(t, "Never skip requirements.", data.AntiPatternMap["skips reqs"])
}

func TestExtractPreviousRefineMapSkipsNonReusableFallbackMappings(t *testing.T) {
	manifest := `{
		"schema_version": "crux-skillset.v1",
		"bundle_name": "crux-personal-skillset",
		"project_id": "proj-1",
		"version": "v-abc123",
		"compiled_hash": "abc123",
		"generated_at": "2026-03-17T00:00:00Z",
		"documents": [
			{
				"path": "01-clarification.md",
				"category": "clarification",
				"title": "Clarify Before Building",
				"confidence": 0.85,
				"pre_refine_rules": ["ask first"],
				"pre_refine_anti_patterns": ["skips reqs"],
				"refined_rules": ["ask first"],
				"refined_anti_patterns": ["skips reqs"],
				"reusable_rule_mappings": [false],
				"reusable_anti_mappings": [false]
			}
		]
	}`

	version := &SkillSetVersion{
		Files: []SkillSetVersionFile{
			{Path: "00-manifest.json", Content: manifest},
		},
	}

	result := extractPreviousRefineMap(version)
	require.NotNil(t, result)
	require.Contains(t, result, "clarification")
	require.Empty(t, result["clarification"].RuleMap)
	require.Empty(t, result["clarification"].AntiPatternMap)
}

func TestExtractPreviousRefineMapNilVersion(t *testing.T) {
	result := extractPreviousRefineMap(nil)
	require.Nil(t, result)
}

func TestExtractPreviousRefineMapNoManifest(t *testing.T) {
	version := &SkillSetVersion{
		Files: []SkillSetVersionFile{
			{Path: "01-clarification.md", Content: "# Clarify\n"},
		},
	}
	result := extractPreviousRefineMap(version)
	require.Nil(t, result)
}

func TestBuildLatestSkillSetBundleStoresPreRefineData(t *testing.T) {
	reports := []*Report{{
		ID:             "rep-1",
		ProjectID:      "proj-1",
		Title:          "Requirement clarification drift",
		Reason:         "Questions were skipped.",
		ExpectedImpact: "Better alignment.",
		Frictions:      []string{"Starts coding before confirming requirements."},
		NextSteps:      []string{"Ask for constraints first."},
		Status:         "active",
		CreatedAt:      time.Now().UTC(),
	}}

	bundle, err := buildLatestSkillSetBundle("proj-1", reports)
	require.NoError(t, err)
	require.Equal(t, "ready", bundle.Status)

	// Find the manifest and verify it contains pre-refine data.
	var manifestContent string
	for _, f := range bundle.Files {
		if f.Path == "00-manifest.json" {
			manifestContent = f.Content
			break
		}
	}
	require.NotEmpty(t, manifestContent)
	require.Contains(t, manifestContent, "pre_refine_rules")
	require.Contains(t, manifestContent, "refined_rules")
	require.Contains(t, manifestContent, "reusable_rule_mappings")
}

// ── Edge-case tests for diff-based refinement ──

func TestDiffAndRefineCategoriesNilPrevNilAgent(t *testing.T) {
	categories := []compiledSkillCategory{
		{
			Category:              "clarification",
			Title:                 "Clarify",
			Rules:                 []string{"raw rule"},
			AntiPatterns:          []string{"raw anti"},
			PreRefineRules:        []string{"raw rule"},
			PreRefineAntiPatterns: []string{"raw anti"},
		},
	}
	// Both nil → returns categories unchanged (no refinement, no reuse).
	result := diffAndRefineCategories(categories, nil, nil)
	require.Len(t, result, 1)
	require.Equal(t, []string{"raw rule"}, result[0].Rules)
	require.Equal(t, []string{"raw anti"}, result[0].AntiPatterns)
}

func TestDiffAndRefineCategoriesEmptyPrev(t *testing.T) {
	// Empty prev map (not nil) — all rules are new.
	prev := map[string]*previousCategoryRefineData{}
	categories := []compiledSkillCategory{
		{
			Category:              "planning",
			Title:                 "Plan",
			Rules:                 []string{"outline steps"},
			AntiPatterns:          []string{"no plan"},
			PreRefineRules:        []string{"outline steps"},
			PreRefineAntiPatterns: []string{"no plan"},
		},
	}

	result := diffAndRefineCategories(categories, prev, nil)
	require.Len(t, result, 1)
	// No prev match, no agent → raw text kept.
	require.Equal(t, []string{"outline steps"}, result[0].Rules)
	require.Equal(t, []string{"no plan"}, result[0].AntiPatterns)
}

func TestDiffAndRefineCategoriesMultipleCategoriesMixed(t *testing.T) {
	// Two categories: one fully reused, one with a new rule.
	prev := map[string]*previousCategoryRefineData{
		"clarification": {
			RuleMap:        map[string]string{"ask first": "REFINED: ask first"},
			AntiPatternMap: map[string]string{"skip reqs": "REFINED: skip reqs"},
		},
		"validation": {
			RuleMap:        map[string]string{"run tests": "REFINED: run tests"},
			AntiPatternMap: map[string]string{},
		},
	}

	categories := []compiledSkillCategory{
		{
			Category:              "clarification",
			Title:                 "Clarify",
			Rules:                 []string{"ask first"},
			AntiPatterns:          []string{"skip reqs"},
			PreRefineRules:        []string{"ask first"},
			PreRefineAntiPatterns: []string{"skip reqs"},
		},
		{
			Category:              "validation",
			Title:                 "Validate",
			Rules:                 []string{"run tests", "check coverage"},
			AntiPatterns:          []string{"new anti"},
			PreRefineRules:        []string{"run tests", "check coverage"},
			PreRefineAntiPatterns: []string{"new anti"},
		},
	}

	result := diffAndRefineCategories(categories, prev, nil)
	require.Len(t, result, 2)

	// Clarification: fully reused.
	require.Equal(t, []string{"REFINED: ask first"}, result[0].Rules)
	require.Equal(t, []string{"REFINED: skip reqs"}, result[0].AntiPatterns)

	// Validation: "run tests" reused, "check coverage" new (raw), "new anti" new (raw).
	require.Equal(t, []string{"REFINED: run tests", "check coverage"}, result[1].Rules)
	require.Equal(t, []string{"new anti"}, result[1].AntiPatterns)
}

func TestDiffAndRefineCategoriesPreservesOriginalOrder(t *testing.T) {
	// Verify the order: reused and new rules are interleaved correctly.
	prev := map[string]*previousCategoryRefineData{
		"execution": {
			RuleMap: map[string]string{
				"rule-a": "REFINED-A",
				"rule-c": "REFINED-C",
			},
			AntiPatternMap: map[string]string{},
		},
	}

	// Order: a(reused), b(new), c(reused), d(new)
	categories := []compiledSkillCategory{
		{
			Category:              "execution",
			Title:                 "Execute",
			Rules:                 []string{"rule-a", "rule-b", "rule-c", "rule-d"},
			AntiPatterns:          nil,
			PreRefineRules:        []string{"rule-a", "rule-b", "rule-c", "rule-d"},
			PreRefineAntiPatterns: nil,
		},
	}

	result := diffAndRefineCategories(categories, prev, nil)
	require.Len(t, result, 1)
	require.Equal(t, []string{"REFINED-A", "rule-b", "REFINED-C", "rule-d"}, result[0].Rules)
}

func TestDiffAndRefineCategoriesAllNewRules(t *testing.T) {
	// Prev exists but has no matching rules at all.
	prev := map[string]*previousCategoryRefineData{
		"clarification": {
			RuleMap:        map[string]string{"old rule": "REFINED: old rule"},
			AntiPatternMap: map[string]string{},
		},
	}

	categories := []compiledSkillCategory{
		{
			Category:              "clarification",
			Title:                 "Clarify",
			Rules:                 []string{"brand new rule 1", "brand new rule 2"},
			AntiPatterns:          []string{"brand new anti"},
			PreRefineRules:        []string{"brand new rule 1", "brand new rule 2"},
			PreRefineAntiPatterns: []string{"brand new anti"},
		},
	}

	result := diffAndRefineCategories(categories, prev, nil)
	require.Len(t, result, 1)
	// No matches in prev → all kept as raw.
	require.Equal(t, []string{"brand new rule 1", "brand new rule 2"}, result[0].Rules)
	require.Equal(t, []string{"brand new anti"}, result[0].AntiPatterns)
}

func TestDiffAndRefineCategoriesEmptyRulesCategory(t *testing.T) {
	prev := map[string]*previousCategoryRefineData{
		"planning": {
			RuleMap:        map[string]string{},
			AntiPatternMap: map[string]string{},
		},
	}

	categories := []compiledSkillCategory{
		{
			Category:              "planning",
			Title:                 "Plan",
			Rules:                 nil,
			AntiPatterns:          nil,
			PreRefineRules:        nil,
			PreRefineAntiPatterns: nil,
		},
	}

	result := diffAndRefineCategories(categories, prev, nil)
	require.Len(t, result, 1)
	require.Nil(t, result[0].Rules)
	require.Nil(t, result[0].AntiPatterns)
}

func TestDiffAndRefineCategoriesCategoryNotInPrev(t *testing.T) {
	// Prev only has "clarification", but categories include "execution".
	prev := map[string]*previousCategoryRefineData{
		"clarification": {
			RuleMap:        map[string]string{"ask": "REFINED: ask"},
			AntiPatternMap: map[string]string{},
		},
	}

	categories := []compiledSkillCategory{
		{
			Category:              "execution",
			Title:                 "Execute",
			Rules:                 []string{"keep scope narrow"},
			AntiPatterns:          []string{"scope creep"},
			PreRefineRules:        []string{"keep scope narrow"},
			PreRefineAntiPatterns: []string{"scope creep"},
		},
	}

	result := diffAndRefineCategories(categories, prev, nil)
	require.Len(t, result, 1)
	// Category not found in prev → all rules treated as new.
	require.Equal(t, []string{"keep scope narrow"}, result[0].Rules)
	require.Equal(t, []string{"scope creep"}, result[0].AntiPatterns)
}

func TestExtractPreviousRefineMapMismatchedLengths(t *testing.T) {
	// pre_refine_rules has 3 items, refined_rules has 2.
	// Only the first 2 should be mapped.
	manifest := `{
		"schema_version": "crux-skillset.v1",
		"bundle_name": "test",
		"project_id": "proj-1",
		"version": "v-abc",
		"compiled_hash": "abc",
		"generated_at": "2026-03-17T00:00:00Z",
		"documents": [{
			"path": "01-clarification.md",
			"category": "clarification",
			"title": "Clarify",
			"confidence": 0.8,
			"pre_refine_rules": ["a", "b", "c"],
			"refined_rules": ["A", "B"],
			"reusable_rule_mappings": [true, true, true]
		}]
	}`

	version := &SkillSetVersion{
		Files: []SkillSetVersionFile{
			{Path: "00-manifest.json", Content: manifest},
		},
	}

	result := extractPreviousRefineMap(version)
	require.NotNil(t, result)
	data := result["clarification"]
	require.Equal(t, "A", data.RuleMap["a"])
	require.Equal(t, "B", data.RuleMap["b"])
	_, hasCMapping := data.RuleMap["c"]
	require.False(t, hasCMapping, "rule 'c' should not have a mapping since refined_rules is shorter")
}

func TestExtractPreviousRefineMapInvalidJSON(t *testing.T) {
	version := &SkillSetVersion{
		Files: []SkillSetVersionFile{
			{Path: "00-manifest.json", Content: "not valid json {{{"},
		},
	}
	result := extractPreviousRefineMap(version)
	require.Nil(t, result)
}

func TestExtractPreviousRefineMapMultipleCategories(t *testing.T) {
	manifest := `{
		"schema_version": "crux-skillset.v1",
		"bundle_name": "test",
		"project_id": "proj-1",
		"version": "v-abc",
		"compiled_hash": "abc",
		"generated_at": "2026-03-17T00:00:00Z",
		"documents": [
			{
				"path": "01-clarification.md",
				"category": "clarification",
				"title": "Clarify",
				"confidence": 0.8,
				"pre_refine_rules": ["ask"],
				"refined_rules": ["Always ask."],
				"reusable_rule_mappings": [true]
			},
			{
				"path": "02-planning.md",
				"category": "planning",
				"title": "Plan",
				"confidence": 0.9,
				"pre_refine_rules": ["outline"],
				"pre_refine_anti_patterns": ["no plan"],
				"refined_rules": ["Always outline steps."],
				"refined_anti_patterns": ["Never start without a plan."],
				"reusable_rule_mappings": [true],
				"reusable_anti_mappings": [true]
			}
		]
	}`

	version := &SkillSetVersion{
		Files: []SkillSetVersionFile{
			{Path: "00-manifest.json", Content: manifest},
		},
	}

	result := extractPreviousRefineMap(version)
	require.Len(t, result, 2)

	require.Equal(t, "Always ask.", result["clarification"].RuleMap["ask"])
	require.Equal(t, "Always outline steps.", result["planning"].RuleMap["outline"])
	require.Equal(t, "Never start without a plan.", result["planning"].AntiPatternMap["no plan"])
}

func TestBuildLatestSkillSetBundlePreviouslyRawFallbackCanRefineLater(t *testing.T) {
	reports := []*Report{{
		ID:             "rep-1",
		ProjectID:      "proj-1",
		Title:          "Requirement clarification drift",
		Reason:         "Questions were skipped.",
		ExpectedImpact: "Better alignment.",
		Frictions:      []string{"Starts coding before confirming requirements."},
		NextSteps:      []string{"Ask for constraints first."},
		Status:         "active",
		CreatedAt:      time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC),
	}}

	first, err := buildLatestSkillSetBundle("proj-1", reports)
	require.NoError(t, err)

	prevVersion := &SkillSetVersion{
		Version:      first.Version,
		CompiledHash: first.CompiledHash,
	}
	for _, f := range first.Files {
		prevVersion.Files = append(prevVersion.Files, SkillSetVersionFile{
			Path: f.Path, Content: f.Content, SHA256: f.SHA256, Bytes: f.Bytes,
		})
	}

	prev := extractPreviousRefineMap(prevVersion)
	require.Contains(t, prev, "clarification")
	require.Empty(t, prev["clarification"].RuleMap)
	require.Empty(t, prev["clarification"].AntiPatternMap)

	agent := newTestSkillRefineAgent(t, `[{
		"category":"clarification",
		"rules":["Always ask for missing constraints before editing."],
		"anti_patterns":["Never start coding before confirming the requirements."]
	}]`)

	second, err := buildLatestSkillSetBundle("proj-1", reports, skillSetBuildOptions{
		RefineAgent:     agent,
		PreviousVersion: prevVersion,
	})
	require.NoError(t, err)

	var clarifyContent string
	for _, f := range second.Files {
		if f.Path == "01-clarification.md" {
			clarifyContent = f.Content
			break
		}
	}
	require.Contains(t, clarifyContent, "Always ask for missing constraints before editing")
	require.Contains(t, clarifyContent, "Never start coding before confirming the requirements")
}

func TestBuildLatestSkillSetBundleRoundTrip(t *testing.T) {
	// Build first bundle → extract manifest → use as previous → build second
	// with same reports → verify hash is identical (no-op refinement).
	reports := []*Report{{
		ID:             "rep-1",
		ProjectID:      "proj-1",
		Title:          "Requirement clarification drift",
		Reason:         "Questions were skipped.",
		ExpectedImpact: "Better alignment.",
		Frictions:      []string{"Starts coding before confirming requirements."},
		NextSteps:      []string{"Ask for constraints first."},
		Status:         "active",
		CreatedAt:      time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC),
	}}

	first, err := buildLatestSkillSetBundle("proj-1", reports)
	require.NoError(t, err)
	require.Equal(t, "ready", first.Status)

	// Simulate storing as a SkillSetVersion.
	prevVersion := &SkillSetVersion{
		Version:      first.Version,
		CompiledHash: first.CompiledHash,
	}
	for _, f := range first.Files {
		prevVersion.Files = append(prevVersion.Files, SkillSetVersionFile{
			Path:    f.Path,
			Content: f.Content,
			SHA256:  f.SHA256,
			Bytes:   f.Bytes,
		})
	}

	// Build again with the same reports and previous version (no refine agent).
	second, err := buildLatestSkillSetBundle("proj-1", reports, skillSetBuildOptions{
		PreviousVersion: prevVersion,
	})
	require.NoError(t, err)
	require.Equal(t, "ready", second.Status)

	// Without a refine agent, raw rules are used each time.
	// The hashes should be identical because the same reports produce the
	// same raw rules, and the previous version's pre-refine == refined (no LLM).
	require.Equal(t, first.CompiledHash, second.CompiledHash,
		"rebuilding with same reports and previous version should produce identical hash")
	require.Equal(t, first.Version, second.Version)
}

func TestBuildLatestSkillSetBundleRoundTripWithAddedReport(t *testing.T) {
	// Build first bundle, then add a new report → verify first report's
	// rules are preserved and only the new report's rules change the hash.
	baseReport := &Report{
		ID:             "rep-base",
		ProjectID:      "proj-1",
		Title:          "Requirement clarification drift",
		Reason:         "Skipped questions.",
		ExpectedImpact: "Better alignment.",
		Frictions:      []string{"Starts coding before confirming."},
		NextSteps:      []string{"Ask constraints first."},
		Status:         "active",
		CreatedAt:      time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC),
	}

	first, err := buildLatestSkillSetBundle("proj-1", []*Report{baseReport})
	require.NoError(t, err)

	prevVersion := &SkillSetVersion{
		Version:      first.Version,
		CompiledHash: first.CompiledHash,
	}
	for _, f := range first.Files {
		prevVersion.Files = append(prevVersion.Files, SkillSetVersionFile{
			Path: f.Path, Content: f.Content, SHA256: f.SHA256, Bytes: f.Bytes,
		})
	}

	// New report with an additional rule.
	newReport := &Report{
		ID:             "rep-new",
		ProjectID:      "proj-1",
		Title:          "Test validation gap",
		Reason:         "Tests were skipped.",
		ExpectedImpact: "Better coverage.",
		Frictions:      []string{"Skips test suite after changes."},
		NextSteps:      []string{"Run full test suite."},
		Status:         "active",
		CreatedAt:      time.Date(2026, 3, 17, 1, 0, 0, 0, time.UTC),
	}

	second, err := buildLatestSkillSetBundle("proj-1", []*Report{baseReport, newReport}, skillSetBuildOptions{
		PreviousVersion: prevVersion,
	})
	require.NoError(t, err)
	require.Equal(t, "ready", second.Status)

	// Hash should differ because there are new rules from newReport.
	require.NotEqual(t, first.CompiledHash, second.CompiledHash,
		"adding new report should change hash")

	// Verify the bundle has files for both categories.
	paths := make(map[string]bool)
	for _, f := range second.Files {
		paths[f.Path] = true
	}
	require.True(t, paths["01-clarification.md"])
}

// TestServerBundleHashMatchesClientDiskHash is the definitive end-to-end
// test for the "modified locally" conflict bug. It:
//  1. Calls the real buildLatestSkillSetBundle (server-side)
//  2. Writes the resulting files to a temp directory (simulating client deploy)
//  3. Computes the client-side hash (identical algorithm to cmd/crux hashManagedSkillBundle)
//  4. Compares with bundle.CompiledHash — they MUST match
//
// If this test fails, every sync will produce a spurious conflict.
func TestServerBundleHashMatchesClientDiskHash(t *testing.T) {
	reports := []*Report{
		{
			ID:             "rep-1",
			ProjectID:      "proj-1",
			Title:          "Requirement clarification drift",
			Reason:         "Requirement questions were skipped before implementation.",
			ExpectedImpact: "Fewer premature implementations.",
			Frictions:      []string{"Starts implementation before confirming missing requirements."},
			NextSteps:      []string{"Ask for missing constraints before touching code."},
			Status:         "active",
			CreatedAt:      time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC),
		},
		{
			ID:             "rep-2",
			ProjectID:      "proj-1",
			Title:          "Test validation gap",
			Reason:         "Tests were skipped after changes.",
			ExpectedImpact: "Better test coverage.",
			Frictions:      []string{"Skips test suite after making changes."},
			NextSteps:      []string{"Run the full test suite after each change."},
			Status:         "active",
			CreatedAt:      time.Date(2026, 3, 17, 1, 0, 0, 0, time.UTC),
		},
	}

	bundle, err := buildLatestSkillSetBundle("proj-1", reports)
	require.NoError(t, err)
	require.Equal(t, "ready", bundle.Status)
	require.NotEmpty(t, bundle.CompiledHash)
	require.NotEmpty(t, bundle.Version)

	// ── Step 2: write all bundle files to disk (like client deploy) ──
	root := t.TempDir()
	for _, f := range bundle.Files {
		target := filepath.Join(root, filepath.FromSlash(strings.TrimSpace(f.Path)))
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		require.NoError(t, os.WriteFile(target, []byte(f.Content), 0o644))
	}

	// ── Step 3: compute client-side hash (same algorithm as cmd/crux) ──
	diskHash := clientHashManagedSkillBundle(t, root)

	// ── Step 4: compare ──
	require.Equal(t, bundle.CompiledHash, diskHash,
		"server CompiledHash must equal client disk hash after deploy.\n"+
			"Mismatch here causes the spurious 'managed skill bundle was modified locally' conflict.\n"+
			"server CompiledHash: %s\nclient disk hash:    %s\nbundle version:      %s",
		bundle.CompiledHash, diskHash, bundle.Version)
}

// TestServerBundleHashMatchesClientDiskHash_MultipleRebuilds verifies
// stability across multiple rebuilds with the same reports.
func TestServerBundleHashMatchesClientDiskHash_MultipleRebuilds(t *testing.T) {
	reports := []*Report{{
		ID:             "rep-1",
		ProjectID:      "proj-1",
		Title:          "Clarification drift",
		Reason:         "Questions skipped.",
		ExpectedImpact: "Better alignment.",
		Frictions:      []string{"Starts coding before confirming."},
		NextSteps:      []string{"Ask constraints first."},
		Status:         "active",
		CreatedAt:      time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC),
	}}

	for i := 0; i < 5; i++ {
		bundle, err := buildLatestSkillSetBundle("proj-1", reports)
		require.NoError(t, err)

		root := t.TempDir()
		for _, f := range bundle.Files {
			target := filepath.Join(root, filepath.FromSlash(strings.TrimSpace(f.Path)))
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte(f.Content), 0o644))
		}

		diskHash := clientHashManagedSkillBundle(t, root)
		require.Equal(t, bundle.CompiledHash, diskHash,
			"rebuild %d: hash mismatch", i+1)
	}
}

// clientHashManagedSkillBundle replicates the client-side hash algorithm
// from cmd/crux hashManagedSkillBundle (including the SKILL.md version
// line stripping) so we can test server↔client hash agreement in a single
// package.
func clientHashManagedSkillBundle(t *testing.T, root string) string {
	t.Helper()

	type hashedFile struct {
		Path    string
		Content []byte
	}

	var files []hashedFile
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.EqualFold(rel, "00-manifest.json") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Strip the Version line from SKILL.md (same as client).
		if strings.EqualFold(rel, "SKILL.md") {
			content = clientStripSkillVersionLine(content)
		}
		files = append(files, hashedFile{Path: rel, Content: content})
		return nil
	})
	require.NoError(t, err)

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	hasher := sha256.New()
	for _, f := range files {
		_, _ = hasher.Write([]byte(strings.TrimSpace(f.Path)))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(f.Content)
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// clientStripSkillVersionLine mirrors the cmd/crux stripSkillVersionLine.
func clientStripSkillVersionLine(content []byte) []byte {
	var result []byte
	remaining := content
	skipNextBlank := false
	for len(remaining) > 0 {
		idx := bytes.IndexByte(remaining, '\n')
		var line []byte
		if idx < 0 {
			line = remaining
			remaining = nil
		} else {
			line = remaining[:idx+1]
			remaining = remaining[idx+1:]
		}
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("Version:")) && bytes.Contains(trimmed, []byte("`v")) {
			skipNextBlank = true
			continue
		}
		if skipNextBlank && len(trimmed) == 0 {
			skipNextBlank = false
			continue
		}
		skipNextBlank = false
		result = append(result, line...)
	}
	return result
}

func newTestSkillRefineAgent(t *testing.T, output string) *SkillRefineAgent {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{
  "output": [
    {
      "type": "message",
      "content": [
        {
          "type": "output_text",
          "text": ` + strconv.Quote(output) + `
        }
      ]
    }
  ]
}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	return &SkillRefineAgent{
		client: openai.NewClient(
			option.WithAPIKey("test-openai-key"),
			option.WithBaseURL(server.URL+"/v1"),
			option.WithHTTPClient(server.Client()),
		),
		model:  defaultSkillRefineModel,
		apiKey: "test-openai-key",
	}
}
