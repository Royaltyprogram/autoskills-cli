package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/liushuangls/go-server-template/configs"
	"github.com/liushuangls/go-server-template/dto/response"
)

type AnalyticsStore struct {
	mu sync.RWMutex

	filePath string
	seq      uint64

	organizations          map[string]*Organization
	users                  map[string]*User
	agents                 map[string]*Agent
	projects               map[string]*Project
	configSnapshots        map[string][]*ConfigSnapshot
	sessionSummaries       map[string][]*SessionSummary
	recommendations        map[string]*Recommendation
	projectRecommendations map[string][]string
	applyOperations        map[string]*ApplyOperation
	audits                 []*AuditEvent
}

type analyticsStoreState struct {
	Seq                    uint64                       `json:"seq"`
	Organizations          map[string]*Organization     `json:"organizations"`
	Users                  map[string]*User             `json:"users"`
	Agents                 map[string]*Agent            `json:"agents"`
	Projects               map[string]*Project          `json:"projects"`
	ConfigSnapshots        map[string][]*ConfigSnapshot `json:"config_snapshots"`
	SessionSummaries       map[string][]*SessionSummary `json:"session_summaries"`
	Recommendations        map[string]*Recommendation   `json:"recommendations"`
	ProjectRecommendations map[string][]string          `json:"project_recommendations"`
	ApplyOperations        map[string]*ApplyOperation   `json:"apply_operations"`
	Audits                 []*AuditEvent                `json:"audits"`
}

type Organization struct {
	ID   string
	Name string
}

type User struct {
	ID    string
	OrgID string
	Email string
}

type Agent struct {
	ID            string
	OrgID         string
	UserID        string
	DeviceName    string
	Hostname      string
	Platform      string
	CLIVersion    string
	Tools         []string
	ConsentScopes []string
	RegisteredAt  time.Time
}

type Project struct {
	ID             string
	OrgID          string
	AgentID        string
	Name           string
	RepoHash       string
	RepoPath       string
	LanguageMix    map[string]float64
	DefaultTool    string
	LastProfileID  string
	LastIngestedAt *time.Time
	ConnectedAt    time.Time
}

type ConfigSnapshot struct {
	ID                  string
	ProjectID           string
	Tool                string
	ProfileID           string
	Settings            map[string]any
	EnabledMCPCount     int
	HooksEnabled        bool
	InstructionFiles    []string
	ConfigFingerprint   string
	RecentConfigChanges []string
	CapturedAt          time.Time
}

type SessionSummary struct {
	ID                       string
	ProjectID                string
	Tool                     string
	ProjectHash              string
	LanguageMix              map[string]float64
	TotalPromptsCount        int
	TotalToolCalls           int
	BashCallsCount           int
	ReadOps                  int
	EditOps                  int
	WriteOps                 int
	MCPUsageCount            int
	PermissionRejectCount    int
	RetryCount               int
	TokenIn                  int
	TokenOut                 int
	EstimatedCost            float64
	TaskType                 string
	RepoSizeBucket           string
	ConfigProfileID          string
	TaskTypeDistribution     map[string]float64
	RepoExplorationIntensity float64
	ShellHeavy               bool
	WorkloadTags             []string
	AcceptanceProxy          float64
	EventSummaries           []string
	Timestamp                time.Time
}

type Recommendation struct {
	ID               string
	ProjectID        string
	Kind             string
	Title            string
	Summary          string
	Reason           string
	Explanation      string
	ExpectedBenefit  string
	Risk             string
	ExpectedImpact   string
	Score            float64
	Status           string
	TargetTool       string
	TargetFileHint   string
	ResearchProvider string
	ResearchModel    string
	Evidence         []string
	ChangePlan       []ChangePlanStep
	SettingsUpdates  map[string]any
	CreatedAt        time.Time
}

type ChangePlanStep struct {
	Type            string
	Action          string
	TargetFile      string
	Summary         string
	SettingsUpdates map[string]any
	ContentPreview  string
}

type PatchPreview struct {
	FilePath        string
	Operation       string
	Summary         string
	SettingsUpdates map[string]any
	ContentPreview  string
}

type ApplyOperation struct {
	ID               string
	RecommendationID string
	ProjectID        string
	RequestedBy      string
	Scope            string
	Status           string
	ApprovalStatus   string
	Decision         string
	ReviewedBy       string
	ReviewNote       string
	AppliedText      string
	PatchPreview     []PatchPreview
	AppliedFile      string
	AppliedSettings  map[string]any
	Note             string
	RolledBack       bool
	RequestedAt      time.Time
	ReviewedAt       *time.Time
	AppliedAt        *time.Time
}

type AuditEvent struct {
	ID        string
	OrgID     string
	ProjectID string
	Type      string
	Message   string
	CreatedAt time.Time
}

func NewAnalyticsStore(conf *configs.Config) (*AnalyticsStore, error) {
	store := &AnalyticsStore{
		filePath:               conf.App.StorePath,
		organizations:          make(map[string]*Organization),
		users:                  make(map[string]*User),
		agents:                 make(map[string]*Agent),
		projects:               make(map[string]*Project),
		configSnapshots:        make(map[string][]*ConfigSnapshot),
		sessionSummaries:       make(map[string][]*SessionSummary),
		recommendations:        make(map[string]*Recommendation),
		projectRecommendations: make(map[string][]string),
		applyOperations:        make(map[string]*ApplyOperation),
		audits:                 make([]*AuditEvent, 0, 32),
	}
	if err := store.loadFromDisk(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *AnalyticsStore) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%06d", prefix, s.seq)
}

func (s *AnalyticsStore) persistLocked() error {
	if s.filePath == "" {
		return nil
	}

	state := analyticsStoreState{
		Seq:                    s.seq,
		Organizations:          s.organizations,
		Users:                  s.users,
		Agents:                 s.agents,
		Projects:               s.projects,
		ConfigSnapshots:        s.configSnapshots,
		SessionSummaries:       s.sessionSummaries,
		Recommendations:        s.recommendations,
		ProjectRecommendations: s.projectRecommendations,
		ApplyOperations:        s.applyOperations,
		Audits:                 s.audits,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o755); err != nil {
		return err
	}

	tempPath := s.filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tempPath, s.filePath)
}

func (s *AnalyticsStore) loadFromDisk() error {
	if s.filePath == "" {
		return nil
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var state analyticsStoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	s.seq = state.Seq
	s.organizations = ensureMap(state.Organizations)
	s.users = ensureMap(state.Users)
	s.agents = ensureMap(state.Agents)
	s.projects = ensureMap(state.Projects)
	s.configSnapshots = ensureNestedMap(state.ConfigSnapshots)
	s.sessionSummaries = ensureNestedMap(state.SessionSummaries)
	s.recommendations = ensureMap(state.Recommendations)
	s.projectRecommendations = ensureStringSliceMap(state.ProjectRecommendations)
	s.applyOperations = ensureMap(state.ApplyOperations)
	if state.Audits == nil {
		s.audits = make([]*AuditEvent, 0, 32)
	} else {
		s.audits = state.Audits
	}
	return nil
}

func ensureMap[T any](in map[string]T) map[string]T {
	if in == nil {
		return make(map[string]T)
	}
	return in
}

func ensureNestedMap[T any](in map[string][]T) map[string][]T {
	if in == nil {
		return make(map[string][]T)
	}
	return in
}

func ensureStringSliceMap(in map[string][]string) map[string][]string {
	if in == nil {
		return make(map[string][]string)
	}
	return in
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func cloneFloatMap(input map[string]float64) map[string]float64 {
	if len(input) == 0 {
		return map[string]float64{}
	}
	out := make(map[string]float64, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func cloneStringSlice(input []string) []string {
	if len(input) == 0 {
		return []string{}
	}
	out := make([]string, len(input))
	copy(out, input)
	return out
}

func cloneChangePlanSteps(input []ChangePlanStep) []ChangePlanStep {
	if len(input) == 0 {
		return []ChangePlanStep{}
	}
	out := make([]ChangePlanStep, 0, len(input))
	for _, item := range input {
		out = append(out, ChangePlanStep{
			Type:            item.Type,
			Action:          item.Action,
			TargetFile:      item.TargetFile,
			Summary:         item.Summary,
			SettingsUpdates: cloneAnyMap(item.SettingsUpdates),
			ContentPreview:  item.ContentPreview,
		})
	}
	return out
}

func sortedTaskBreakdown(counts map[string]int) []response.TaskBreakdown {
	items := make([]response.TaskBreakdown, 0, len(counts))
	for task, count := range counts {
		items = append(items, response.TaskBreakdown{TaskType: task, Sessions: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Sessions == items[j].Sessions {
			return items[i].TaskType < items[j].TaskType
		}
		return items[i].Sessions > items[j].Sessions
	})
	if len(items) > 3 {
		items = items[:3]
	}
	return items
}
