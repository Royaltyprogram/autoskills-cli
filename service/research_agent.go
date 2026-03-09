package service

import (
	"fmt"
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

func NewCloudResearchAgentPlaceholder(conf *configs.Config) *CloudResearchAgent {
	_ = conf
	return &CloudResearchAgent{
		Provider: "openai",
		Model:    "placeholder-research-agent",
		Mode:     "rule-based-placeholder",
	}
}

func (a *CloudResearchAgent) AnalyzeProject(project *Project, sessions []*SessionSummary, snapshots []*ConfigSnapshot) []researchRecommendation {
	if len(sessions) == 0 {
		return []researchRecommendation{{
			Kind:            "measurement-baseline",
			Title:           "Keep collecting baseline metrics",
			Summary:         "The cloud research agent needs more session evidence before proposing a stronger rollout.",
			Reason:          "There is not enough longitudinal data yet.",
			Explanation:     "The placeholder OpenAI orchestrator is configured, but recommendation generation is still driven by local heuristics until more metrics arrive.",
			ExpectedBenefit: "Higher confidence for the next recommendation cycle.",
			Risk:            "Low. This only preserves measurement and does not alter active coding behavior.",
			ExpectedImpact:  "Improved recommendation quality after more sessions.",
			Score:           0.42,
			Evidence:        []string{"no session summaries uploaded"},
			Steps: []ChangePlanStep{{
				Type:            "json_merge",
				Action:          "merge_patch",
				TargetFile:      targetFileHint(project.DefaultTool),
				Summary:         "Keep measurement-only mode enabled.",
				SettingsUpdates: map[string]any{"recommendation_mode": "observe", "metrics_sampling": "session-summary-only"},
			}},
			Settings: map[string]any{"recommendation_mode": "observe", "metrics_sampling": "session-summary-only"},
		}}
	}

	latestTool := project.DefaultTool
	if latestTool == "" {
		latestTool = sessions[len(sessions)-1].Tool
	}
	latestSnapshot := latestConfigSnapshot(snapshots)

	var (
		prompts          int
		toolCalls        int
		bashCalls        int
		readOps          int
		editOps          int
		retries          int
		rejects          int
		mcpUsage         int
		totalExploration float64
		totalAcceptProxy float64
		shellHeavyHits   int
		taskCounts       = map[string]int{}
	)

	for _, session := range sessions {
		prompts += maxInt(session.TotalPromptsCount, 1)
		toolCalls += session.TotalToolCalls
		bashCalls += session.BashCallsCount
		readOps += session.ReadOps
		editOps += session.EditOps
		retries += session.RetryCount
		rejects += session.PermissionRejectCount
		mcpUsage += session.MCPUsageCount
		totalExploration += session.RepoExplorationIntensity
		totalAcceptProxy += session.AcceptanceProxy
		if session.ShellHeavy {
			shellHeavyHits++
		}
		taskCounts[strings.ToLower(session.TaskType)]++
	}

	dominantTask := dominantTask(taskCounts)
	readShare := safeDiv(float64(readOps), float64(maxInt(readOps+editOps, 1)))
	bashShare := safeDiv(float64(bashCalls), float64(maxInt(toolCalls, 1)))
	rejectRate := safeDiv(float64(rejects), float64(maxInt(toolCalls, 1)))
	retryRate := safeDiv(float64(retries), float64(maxInt(prompts, 1)))
	avgExploration := safeDiv(totalExploration, float64(len(sessions)))
	avgAcceptance := safeDiv(totalAcceptProxy, float64(len(sessions)))
	enabledMCPs := 0
	if latestSnapshot != nil {
		enabledMCPs = latestSnapshot.EnabledMCPCount
	}

	evidenceBase := []string{
		fmt.Sprintf("dominant_task=%s", dominantTask),
		fmt.Sprintf("read_share=%.2f", readShare),
		fmt.Sprintf("bash_share=%.2f", bashShare),
		fmt.Sprintf("retry_rate=%.2f", retryRate),
		fmt.Sprintf("reject_rate=%.2f", rejectRate),
		fmt.Sprintf("repo_exploration=%.2f", avgExploration),
		fmt.Sprintf("acceptance_proxy=%.2f", avgAcceptance),
	}

	recs := make([]researchRecommendation, 0, 4)
	if avgExploration >= 0.55 || readShare >= 0.58 || dominantTask == "repo-qna" {
		recs = append(recs, researchRecommendation{
			Kind:            "instruction-pack",
			Title:           "Roll out a repo research instruction pack",
			Summary:         "Repo exploration is high enough that the cloud agent recommends a shared instruction layer for investigation-heavy sessions.",
			Reason:          "The project behaves like a research-oriented workspace with repeated repository navigation.",
			Explanation:     "The placeholder OpenAI research agent classified this project as repo-Q&A heavy. The production version will use OpenAI for explanation synthesis, but the current build keeps a deterministic rule path.",
			ExpectedBenefit: "Reduce search churn and improve context assembly speed for large codebases.",
			Risk:            "Low to medium. Instruction packs can bias future prompts, so approval should confirm the content matches team policy.",
			ExpectedImpact:  "Higher repo navigation efficiency and lower token waste.",
			Score:           0.9,
			Evidence:        append(cloneStringSlice(evidenceBase), "recommendation=repo_research_pack"),
			Steps: []ChangePlanStep{{
				Type:           "text_append",
				Action:         "append_block",
				TargetFile:     "AGENTS.md",
				Summary:        "Append a repo research instruction pack for investigation-heavy work.",
				ContentPreview: "\n## AgentOpt Research Pack\n- Prefer repo structure discovery before deep edits.\n- Summarize impacted files before patching.\n- Verify hooks, MCPs, and approval scope before execution.\n",
			}},
			Settings: map[string]any{"instructions_pack": "repo-research", "retrieval_mode": "hierarchical"},
		})
	}

	if dominantTask == "bugfix" || dominantTask == "test" || retryRate >= 0.12 {
		recs = append(recs, researchRecommendation{
			Kind:            "verification-hook",
			Title:           "Add a local verification hook",
			Summary:         "Bugfix and retry-heavy workflows should validate edits immediately after patch application.",
			Reason:          "The session mix suggests a test-heavy flow where quick verification prevents repeated retries.",
			Explanation:     "The future OpenAI-backed agent will generate tool-specific hook suggestions. The placeholder currently emits a safe, structured merge plan only.",
			ExpectedBenefit: "Improve inferred accept rate by catching regressions before the user leaves the session.",
			Risk:            "Medium. Hooks may slow local execution if they are too broad, so the scope stays narrow and reviewable.",
			ExpectedImpact:  "Fewer retries and better post-apply retention.",
			Score:           0.84,
			Evidence:        append(cloneStringSlice(evidenceBase), "recommendation=verification_hook"),
			Steps: []ChangePlanStep{{
				Type:            "json_merge",
				Action:          "merge_patch",
				TargetFile:      targetFileHint(latestTool),
				Summary:         "Enable a lightweight post-edit verification hook.",
				SettingsUpdates: map[string]any{"post_edit_hook": "placeholder: run local verification", "hook_timeout_sec": 90},
			}},
			Settings: map[string]any{"post_edit_hook": "placeholder: run local verification", "hook_timeout_sec": 90},
		})
	}

	if enabledMCPs >= 3 && mcpUsage <= maxInt(len(sessions), 1) {
		recs = append(recs, researchRecommendation{
			Kind:            "mcp-prune",
			Title:           "Prune low-value MCP integrations",
			Summary:         "Configured MCP breadth is high relative to observed usage, so the cloud agent recommends trimming low-signal integrations.",
			Reason:          "Multiple MCP servers are enabled but session evidence does not show meaningful usage.",
			Explanation:     "The OpenAI integration is still a placeholder, so the server cannot name a specific MCP yet. It can still flag over-provisioning and send a safe review item.",
			ExpectedBenefit: "Reduce config complexity and permission noise.",
			Risk:            "Medium. A rarely used MCP can still be important, so this plan only proposes a reviewable disable placeholder.",
			ExpectedImpact:  "Lower permission churn and less config entropy.",
			Score:           0.76,
			Evidence:        append(cloneStringSlice(evidenceBase), fmt.Sprintf("enabled_mcp_count=%d", enabledMCPs)),
			Steps: []ChangePlanStep{{
				Type:            "json_merge",
				Action:          "merge_patch",
				TargetFile:      ".mcp.json",
				Summary:         "Mark a low-signal MCP integration for disable review.",
				SettingsUpdates: map[string]any{"agentopt_review": map[string]any{"candidate_action": "disable_low_signal_mcp", "source": "placeholder-openai-analysis"}},
			}},
			Settings: map[string]any{"agentopt_review": map[string]any{"candidate_action": "disable_low_signal_mcp", "source": "placeholder-openai-analysis"}},
		})
	}

	if len(recs) == 0 || (rejectRate >= 0.05 || bashShare >= 0.2 || shellHeavyHits > 0) {
		recs = append(recs, researchRecommendation{
			Kind:            "safe-executor-profile",
			Title:           "Enable a safer local executor profile",
			Summary:         "Shell-heavy usage and permission rejections indicate the local execution agent needs a tighter policy baseline.",
			Reason:          "The current runtime leans on shell activity and encounters approval friction.",
			Explanation:     "This recommendation keeps execution local and deterministic. The future OpenAI layer will only author the plan, not execute arbitrary commands.",
			ExpectedBenefit: "Lower rejection rate and clearer local guardrails.",
			Risk:            "Low. The plan only adjusts policy hints and approval scope metadata.",
			ExpectedImpact:  "Reduced permission churn and safer rollout defaults.",
			Score:           0.71,
			Evidence:        append(cloneStringSlice(evidenceBase), "recommendation=safe_executor_profile"),
			Steps: []ChangePlanStep{{
				Type:            "json_merge",
				Action:          "merge_patch",
				TargetFile:      targetFileHint(latestTool),
				Summary:         "Set safe executor defaults for the local CLI agent.",
				SettingsUpdates: map[string]any{"shell_profile": "safe", "approval_scope": "review-required", "local_guard": "strict"},
			}},
			Settings: map[string]any{"shell_profile": "safe", "approval_scope": "review-required", "local_guard": "strict"},
		})
	}

	return recs
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
