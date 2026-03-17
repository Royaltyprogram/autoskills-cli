package service

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/Royaltyprogram/aiops/configs"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

const (
	defaultSkillRefineModel          = "gpt-5.4"
	defaultSkillRefineRequestTimeout = 30 * time.Second
)

//go:embed prompts/skill_refine_prompt.md
var skillRefinePromptFS embed.FS

var skillRefinePromptTemplate = template.Must(template.New("skill_refine_prompt.md").ParseFS(
	skillRefinePromptFS,
	"prompts/skill_refine_prompt.md",
))

// SkillRefineAgent rewrites human-readable skill rules into
// machine-actionable instructions for an AI coding agent.
type SkillRefineAgent struct {
	client openai.Client
	model  string
	apiKey string
}

func (a *SkillRefineAgent) CanRefine() bool {
	return a != nil && strings.TrimSpace(a.apiKey) != ""
}

// NewSkillRefineAgent creates a refinement agent using the same OpenAI
// configuration as the research agent.
func NewSkillRefineAgent(conf *configs.Config) *SkillRefineAgent {
	var openAIConf configs.OpenAI
	if conf != nil {
		openAIConf = conf.OpenAI
	}
	apiKey := strings.TrimSpace(openAIConf.APIKey)
	model := firstNonEmptyString(strings.TrimSpace(openAIConf.ResponsesModel), defaultSkillRefineModel)
	clientOptions := []option.RequestOption{}
	if apiKey != "" {
		clientOptions = append(clientOptions, option.WithAPIKey(apiKey))
		clientOptions = append(clientOptions, option.WithHTTPClient(&http.Client{Timeout: defaultSkillRefineRequestTimeout}))
		if baseURL := strings.TrimSpace(openAIConf.BaseURL); baseURL != "" {
			clientOptions = append(clientOptions, option.WithBaseURL(baseURL))
		}
	} else {
		model = ""
	}
	return &SkillRefineAgent{
		client: openai.NewClient(clientOptions...),
		model:  model,
		apiKey: apiKey,
	}
}

// RefineCategories rewrites the Rules and AntiPatterns in each category
// so they read as direct agent instructions instead of human advice.
// On failure or when the API key is empty, the original categories are
// returned unchanged.
func (a *SkillRefineAgent) RefineCategories(ctx context.Context, categories []compiledSkillCategory) ([]compiledSkillCategory, error) {
	if !a.CanRefine() || len(categories) == 0 {
		return categories, nil
	}

	prompt, err := buildSkillRefinePrompt(categories)
	if err != nil {
		return categories, err
	}

	resp, err := a.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: openai.ResponsesModel(a.model),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(prompt),
		},
	})
	if err != nil {
		return categories, err
	}

	refined, err := parseSkillRefineResponse(resp.OutputText(), categories)
	if err != nil {
		return categories, err
	}
	return refined, nil
}

// ── prompt ──

type skillRefinePromptData struct {
	CategoriesJSON string
}

type skillRefineInputItem struct {
	Category     string   `json:"category"`
	Title        string   `json:"title"`
	Rules        []string `json:"rules"`
	AntiPatterns []string `json:"anti_patterns"`
}

func buildSkillRefinePrompt(categories []compiledSkillCategory) (string, error) {
	items := make([]skillRefineInputItem, 0, len(categories))
	for _, cat := range categories {
		items = append(items, skillRefineInputItem{
			Category:     cat.Category,
			Title:        cat.Title,
			Rules:        cat.Rules,
			AntiPatterns: cat.AntiPatterns,
		})
	}
	encoded, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal skill categories for refine prompt: %w", err)
	}

	data := skillRefinePromptData{
		CategoriesJSON: string(encoded),
	}
	var buf bytes.Buffer
	if err := skillRefinePromptTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render skill refine prompt: %w", err)
	}
	return strings.TrimSpace(buf.String()), nil
}

// ── response parsing ──

type skillRefineOutputItem struct {
	Category     string   `json:"category"`
	Rules        []string `json:"rules"`
	AntiPatterns []string `json:"anti_patterns"`
}

func parseSkillRefineResponse(raw string, originals []compiledSkillCategory) ([]compiledSkillCategory, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var items []skillRefineOutputItem
	if err := json.Unmarshal([]byte(cleaned), &items); err != nil {
		return nil, fmt.Errorf("parse skill refine response: %w", err)
	}

	if len(items) != len(originals) {
		return nil, fmt.Errorf("skill refine returned %d categories, expected %d", len(items), len(originals))
	}

	result := make([]compiledSkillCategory, len(originals))
	for i, orig := range originals {
		result[i] = orig

		refined := items[i]
		if len(refined.Rules) == len(orig.Rules) {
			result[i].Rules = sanitizeRefineLines(refined.Rules)
		}
		if len(refined.AntiPatterns) == len(orig.AntiPatterns) {
			result[i].AntiPatterns = sanitizeRefineLines(refined.AntiPatterns)
		}
	}
	return result, nil
}

func sanitizeRefineLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
