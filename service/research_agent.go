package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/liushuangls/go-server-template/configs"
)

type CloudResearchAgent struct {
	Provider string
	Model    string
	Mode     string
}

type researchRecommendation struct {
	Kind            string
	Title           string
	Summary         string
	Reason          string
	Explanation     string
	ExpectedBenefit string
	Risk            string
	ExpectedImpact  string
	Score           float64
	Evidence        []string
	Steps           []ChangePlanStep
	Settings        map[string]any
}

type instructionPattern struct {
	Key         string
	Label       string
	Terms       []string
	Instruction string
}

type matchedInstructionPattern struct {
	Pattern instructionPattern
	Count   int
}

var personalInstructionPatterns = []instructionPattern{
	{
		Key:         "repo_discovery",
		Label:       "repo discovery",
		Terms:       []string{"find", "inspect", "explore", "locate", "repo", "which file", "control flow", "summarize the current"},
		Instruction: "Before editing, identify the exact files involved and summarize the current control flow.",
	},
	{
		Key:         "root_cause",
		Label:       "root-cause analysis",
		Terms:       []string{"why", "root cause", "cause", "bug", "error", "failing", "regression"},
		Instruction: "State the likely root cause in one sentence before proposing a patch.",
	},
	{
		Key:         "minimal_patch",
		Label:       "minimal patching",
		Terms:       []string{"minimal", "smallest", "small", "least", "patch", "fix only", "without changing"},
		Instruction: "Prefer the smallest viable patch and call out any behavior that stays intentionally unchanged.",
	},
	{
		Key:         "verification",
		Label:       "targeted verification",
		Terms:       []string{"test", "verify", "verification", "regression", "repro", "run", "check"},
		Instruction: "List the exact verification steps or targeted tests immediately after each substantial edit.",
	},
	{
		Key:         "contract_review",
		Label:       "contract comparison",
		Terms:       []string{"compare", "same", "contract", "response", "shared", "similar"},
		Instruction: "Compare neighboring implementations before changing a shared API, route, or response contract.",
	},
}

func NewCloudResearchAgentPlaceholder(conf *configs.Config) *CloudResearchAgent {
	_ = conf
	return &CloudResearchAgent{
		Provider: "local",
		Model:    "personal-usage-mvp",
		Mode:     "instruction-only",
	}
}

func (a *CloudResearchAgent) AnalyzeProject(project *Project, sessions []*SessionSummary, snapshots []*ConfigSnapshot) []researchRecommendation {
	_ = snapshots

	rawQueries := collectRawQueries(sessions)
	if len(rawQueries) == 0 {
		return nil
	}

	totalTokens := 0
	for _, session := range sessions {
		totalTokens += session.TokenIn + session.TokenOut
	}
	avgTokensPerQuery := safeDiv(float64(totalTokens), float64(maxInt(len(rawQueries), 1)))
	matches := topInstructionPatterns(matchInstructionPatterns(rawQueries), 3)
	contentPreview := buildInstructionContent(matches, avgTokensPerQuery)

	evidence := []string{
		fmt.Sprintf("sessions=%d", len(sessions)),
		fmt.Sprintf("raw_query_count=%d", len(rawQueries)),
		fmt.Sprintf("avg_tokens_per_query=%.0f", avgTokensPerQuery),
		"priority_order=instructions>hooks>mcp>skills",
		"mvp_scope=instruction_only",
	}
	for _, match := range matches {
		evidence = append(evidence, fmt.Sprintf("pattern_%s=%d", match.Pattern.Key, match.Count))
	}

	return []researchRecommendation{{
		Kind:            "instruction-custom-rules",
		Title:           instructionRecommendationTitle(project),
		Summary:         "Recent raw queries repeat the same setup asks, so the MVP agent recommends adding a reusable instruction block.",
		Reason:          buildInstructionReason(matches, avgTokensPerQuery),
		Explanation:     "This MVP only looks at uploaded token usage and raw query history. Web search and non-instruction recommendation types are intentionally deferred.",
		ExpectedBenefit: "Reduce repeated prompt boilerplate and make the first useful answer more consistent.",
		Risk:            "Low. The plan is a reviewable append to AGENTS.md.",
		ExpectedImpact:  "Lower setup churn and fewer repeated discovery prompts in later sessions.",
		Score:           instructionRecommendationScore(matches, avgTokensPerQuery),
		Evidence:        evidence,
		Steps: []ChangePlanStep{{
			Type:           "text_append",
			Action:         "append_block",
			TargetFile:     "AGENTS.md",
			Summary:        "Append a custom instruction block distilled from recent raw query patterns.",
			ContentPreview: contentPreview,
		}},
	}}
}

func instructionRecommendationTitle(project *Project) string {
	if project != nil && strings.TrimSpace(project.Name) != "" {
		return "Add a shared instruction block for " + project.Name
	}
	return "Add a shared instruction block"
}

func collectRawQueries(sessions []*SessionSummary) []string {
	out := make([]string, 0)
	for _, session := range sessions {
		for _, query := range session.RawQueries {
			query = strings.TrimSpace(query)
			if query == "" {
				continue
			}
			out = append(out, query)
		}
	}
	return out
}

func matchInstructionPatterns(queries []string) []matchedInstructionPattern {
	out := make([]matchedInstructionPattern, 0, len(personalInstructionPatterns))
	for _, pattern := range personalInstructionPatterns {
		count := 0
		for _, query := range queries {
			if queryMatchesPattern(query, pattern.Terms) {
				count++
			}
		}
		if count == 0 {
			continue
		}
		out = append(out, matchedInstructionPattern{
			Pattern: pattern,
			Count:   count,
		})
	}
	return out
}

func queryMatchesPattern(query string, terms []string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	for _, term := range terms {
		if strings.Contains(query, term) {
			return true
		}
	}
	return false
}

func topInstructionPatterns(items []matchedInstructionPattern, limit int) []matchedInstructionPattern {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Pattern.Label < items[j].Pattern.Label
		}
		return items[i].Count > items[j].Count
	})
	if limit > 0 && len(items) > limit {
		return append([]matchedInstructionPattern(nil), items[:limit]...)
	}
	return append([]matchedInstructionPattern(nil), items...)
}

func buildInstructionContent(matches []matchedInstructionPattern, avgTokensPerQuery float64) string {
	lines := []string{
		"",
		"## AgentOpt Personal Instruction Pack",
		"- Before editing, restate the goal, affected files, and the success check you will use.",
	}
	for _, match := range matches {
		lines = append(lines, "- "+match.Pattern.Instruction)
	}
	if avgTokensPerQuery >= 2500 {
		lines = append(lines, "- Keep the first response compact and avoid reopening files without new evidence.")
	}
	return strings.Join(lines, "\n") + "\n"
}

func buildInstructionReason(matches []matchedInstructionPattern, avgTokensPerQuery float64) string {
	if len(matches) == 0 {
		if avgTokensPerQuery >= 2500 {
			return "Recent sessions spend enough tokens on prompt setup that a shared instruction block should tighten the first pass."
		}
		return "Recent raw queries show enough repeated setup work to justify a reusable instruction block."
	}
	labels := make([]string, 0, len(matches))
	for _, match := range matches {
		labels = append(labels, match.Pattern.Label)
	}
	return "Recent raw queries repeatedly ask for " + joinHumanList(labels) + "."
}

func instructionRecommendationScore(matches []matchedInstructionPattern, avgTokensPerQuery float64) float64 {
	score := 0.64 + 0.07*float64(len(matches))
	if avgTokensPerQuery >= 2500 {
		score += 0.07
	}
	if score > 0.93 {
		score = 0.93
	}
	return round(score)
}

func joinHumanList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}

func latestConfigSnapshot(items []*ConfigSnapshot) *ConfigSnapshot {
	if len(items) == 0 {
		return nil
	}
	latest := items[0]
	for _, item := range items[1:] {
		if item.CapturedAt.After(latest.CapturedAt) {
			latest = item
		}
	}
	return latest
}
