package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Royaltyprogram/aiops/configs"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
)

type AnalyticsStore struct {
	mu sync.RWMutex

	db             *sql.DB
	dbDialect      string
	filePath       string
	allowDemoUser  bool
	bootstrapUsers []configs.BootstrapUser
	seq            uint64
	lastSeenDirty  bool
	lastSeenFlush  bool

	organizations       map[string]*Organization
	users               map[string]*User
	accessTokens        map[string]*AccessToken
	agents              map[string]*Agent
	projects            map[string]*Project
	configSnapshots     map[string][]*ConfigSnapshot
	sessionSummaries    map[string][]*SessionSummary
	reports             map[string]*Report
	projectReports      map[string][]string
	reportResearch      map[string]*ReportResearchStatus
	skillSetClients     map[string]*SkillSetClientState
	skillSetDeployments map[string][]*SkillSetDeploymentEvent
	skillSetVersions    map[string][]*SkillSetVersion
	sessionImportJobs   map[string]*SessionImportJob
	audits              []*AuditEvent
}

type analyticsStoreState struct {
	SchemaVersion       string                                `json:"schema_version"`
	Seq                 uint64                                `json:"seq"`
	Organizations       map[string]*Organization              `json:"organizations"`
	Users               map[string]*User                      `json:"users"`
	AccessTokens        map[string]*AccessToken               `json:"access_tokens"`
	Agents              map[string]*Agent                     `json:"agents"`
	Projects            map[string]*Project                   `json:"projects"`
	ConfigSnapshots     map[string][]*ConfigSnapshot          `json:"config_snapshots"`
	SessionSummaries    map[string][]*SessionSummary          `json:"session_summaries"`
	Reports             map[string]*Report                    `json:"reports"`
	ProjectReports      map[string][]string                   `json:"project_reports"`
	ReportResearch      map[string]*ReportResearchStatus      `json:"report_research"`
	SkillSetClients     map[string]*SkillSetClientState       `json:"skill_set_clients"`
	SkillSetDeployments map[string][]*SkillSetDeploymentEvent `json:"skill_set_deployments"`
	SkillSetVersions    map[string][]*SkillSetVersion         `json:"skill_set_versions"`
	SessionImportJobs   map[string]*SessionImportJob          `json:"session_import_jobs"`
	Audits              []*AuditEvent                         `json:"audits"`
}

type Organization struct {
	ID   string
	Name string
}

type User struct {
	ID           string
	OrgID        string
	Email        string
	Name         string
	Source       string
	AuthProvider string
	AuthSubject  string
	Role         string
	Status       string
	CreatedAt    time.Time
	LastLoginAt  *time.Time
	DisabledAt   *time.Time
	DeletedAt    *time.Time
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
	ID                     string
	ProjectID              string
	Tool                   string
	TokenIn                int
	TokenOut               int
	CachedInputTokens      int
	ReasoningOutputTokens  int
	FunctionCallCount      int
	ToolErrorCount         int
	SessionDurationMS      int
	ToolWallTimeMS         int
	ToolCalls              map[string]int
	ToolErrors             map[string]int
	ToolWallTimesMS        map[string]int
	RawQueries             []string
	Models                 []string
	ModelProvider          string
	FirstResponseLatencyMS int
	AssistantResponses     []string
	ReasoningSummaries     []string
	Timestamp              time.Time
}

type Report struct {
	ID                  string
	ProjectID           string
	Kind                string
	Title               string
	Summary             string
	UserIntent          string
	ModelInterpretation string
	Reason              string
	Explanation         string
	ExpectedBenefit     string
	Risk                string
	ExpectedImpact      string
	Confidence          string
	Strengths           []string
	Frictions           []string
	NextSteps           []string
	Score               float64
	Status              string
	TargetTool          string
	ResearchProvider    string
	ResearchModel       string
	Evidence            []string
	RawSuggestion       string
	CreatedAt           time.Time
}

type ReportResearchStatus struct {
	ProjectID        string
	State            string
	Summary          string
	Provider         string
	Model            string
	MinimumSessions  int
	SessionCount     int
	RawQueryCount    int
	ReportCount      int `json:"report_count"`
	TriggerSessionID string
	LastError        string
	TriggeredAt      *time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
	LastSuccessfulAt *time.Time
	LastDurationMS   int
}

type SkillSetClientState struct {
	ProjectID      string
	OrgID          string
	AgentID        string
	BundleName     string
	Mode           string
	SyncStatus     string
	AppliedVersion string
	AppliedHash    string
	LastSyncedAt   *time.Time
	PausedAt       *time.Time
	LastError      string
	UpdatedAt      time.Time
}

type SkillSetDeploymentEvent struct {
	ID              string
	ProjectID       string
	OrgID           string
	AgentID         string
	BundleName      string
	EventType       string
	Summary         string
	Mode            string
	SyncStatus      string
	AppliedVersion  string
	PreviousVersion string
	AppliedHash     string
	LastError       string
	OccurredAt      time.Time
}

type SkillSetVersion struct {
	ID                 string
	ProjectID          string
	OrgID              string
	BundleName         string
	Version            string
	CompiledHash       string
	CreatedAt          time.Time
	GeneratedAt        time.Time
	BasedOnReportIDs   []string
	Summary            []string
	Files              []SkillSetVersionFile
	DeploymentDecision string
	DecisionReason     string
	ShadowEvaluation   *SkillSetShadowEvaluation
}

type SkillSetVersionFile struct {
	Path    string
	Content string
	SHA256  string
	Bytes   int
}

type SkillSetShadowEvaluation struct {
	Score                float64
	AverageConfidence    float64
	ChangedDocumentCount int
	AddedRuleCount       int
	RemovedRuleCount     int
	RuleChurn            int
	Guardrail            string
}

type SessionImportJobSession struct {
	SessionID              string
	Tool                   string
	TokenIn                int
	TokenOut               int
	CachedInputTokens      int
	ReasoningOutputTokens  int
	FunctionCallCount      int
	ToolErrorCount         int
	SessionDurationMS      int
	ToolWallTimeMS         int
	ToolCalls              map[string]int
	ToolErrors             map[string]int
	ToolWallTimesMS        map[string]int
	RawQueries             []string
	Models                 []string
	ModelProvider          string
	FirstResponseLatencyMS int
	AssistantResponses     []string
	ReasoningSummaries     []string
	Timestamp              time.Time
}

type SessionImportJobFailure struct {
	SessionID    string
	Error        string
	HTTPStatus   int
	APIErrorCode int
}

type SessionImportJob struct {
	ID                string
	ProjectID         string
	OrgID             string
	AgentID           string
	Status            string
	TotalSessions     int
	ReceivedSessions  int
	ProcessedSessions int
	UploadedSessions  int
	UpdatedSessions   int
	FailedSessions    int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	StartedAt         *time.Time
	CompletedAt       *time.Time
	LastError         string
	LastSessionID     string
	Sessions          []SessionImportJobSession
	Failures          []SessionImportJobFailure
}

type AuditEvent struct {
	ID           string
	OrgID        string
	ProjectID    string
	Type         string
	Message      string
	ActorUserID  string
	ActorRole    string
	TargetUserID string
	SourceIP     string
	UserAgent    string
	Result       string
	Reason       string
	CreatedAt    time.Time
}

const (
	analyticsStoreMetaTable   = "analytics_store_meta"
	analyticsStoreRecordTable = "analytics_store_records"
)

func NewAnalyticsStore(conf *configs.Config) (*AnalyticsStore, error) {
	db, err := openAnalyticsStoreDB(conf)
	if err != nil {
		return nil, err
	}

	dialect := strings.TrimSpace(conf.DB.Dialect)
	if dialect == "" {
		dialect = "sqlite3"
	}

	store := &AnalyticsStore{
		db:                  db,
		dbDialect:           dialect,
		filePath:            conf.App.StorePath,
		allowDemoUser:       conf.AllowsDemoUser(),
		bootstrapUsers:      append([]configs.BootstrapUser(nil), conf.Auth.BootstrapUsers...),
		organizations:       make(map[string]*Organization),
		users:               make(map[string]*User),
		accessTokens:        make(map[string]*AccessToken),
		agents:              make(map[string]*Agent),
		projects:            make(map[string]*Project),
		configSnapshots:     make(map[string][]*ConfigSnapshot),
		sessionSummaries:    make(map[string][]*SessionSummary),
		reports:             make(map[string]*Report),
		projectReports:      make(map[string][]string),
		reportResearch:      make(map[string]*ReportResearchStatus),
		skillSetClients:     make(map[string]*SkillSetClientState),
		skillSetDeployments: make(map[string][]*SkillSetDeploymentEvent),
		skillSetVersions:    make(map[string][]*SkillSetVersion),
		sessionImportJobs:   make(map[string]*SessionImportJob),
		audits:              make([]*AuditEvent, 0, 32),
	}
	if err := store.initDB(); err != nil {
		_ = db.Close()
		return nil, err
	}
	loaded, err := store.loadFromDB()
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if !loaded {
		legacyLoaded, err := store.loadFromLegacyJSON()
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		if legacyLoaded {
			if err := store.persistLocked(); err != nil {
				_ = db.Close()
				return nil, err
			}
		}
	}
	if err := store.ensureBootstrapData(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *AnalyticsStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *AnalyticsStore) OnServerClose(ctx context.Context) error {
	_ = ctx
	return s.Close()
}

func (s *AnalyticsStore) ExportStateJSON() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.snapshotStateLocked(), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (s *AnalyticsStore) ImportStateJSON(data []byte) error {
	var state analyticsStoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.SchemaVersion != "" && state.SchemaVersion != analyticsStoreSchemaVersion {
		return fmt.Errorf("unsupported analytics store schema_version %q", state.SchemaVersion)
	}

	s.mu.Lock()
	if err := s.replaceStateLocked(state); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.persistLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	return s.ensureBootstrapData()
}

func (s *AnalyticsStore) MarkAccessTokenSeenAsync(tokenID string, seenAt time.Time) {
	tokenID = strings.TrimSpace(tokenID)
	if tokenID == "" {
		return
	}

	s.mu.Lock()
	record := s.accessTokens[tokenID]
	if record == nil {
		s.mu.Unlock()
		return
	}
	seenAt = seenAt.UTC()
	if record.LastSeenAt != nil && !seenAt.After(record.LastSeenAt.UTC()) {
		s.mu.Unlock()
		return
	}
	record.LastSeenAt = cloneTime(&seenAt)
	s.lastSeenDirty = true
	if s.lastSeenFlush {
		s.mu.Unlock()
		return
	}
	s.lastSeenFlush = true
	s.mu.Unlock()

	go s.flushLastSeenAsync()
}

func (s *AnalyticsStore) flushLastSeenAsync() {
	time.Sleep(500 * time.Millisecond)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastSeenDirty {
		s.lastSeenDirty = false
		_ = s.persistLocked()
	}
	s.lastSeenFlush = false
}

func (s *AnalyticsStore) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%06d", prefix, s.seq)
}

func (s *AnalyticsStore) persistLocked() error {
	if s.db == nil {
		return nil
	}

	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", analyticsStoreMetaTable)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		fmt.Sprintf("INSERT INTO %s(meta_key, meta_value) VALUES(?, ?)", analyticsStoreMetaTable),
		"seq",
		fmt.Sprintf("%d", s.seq),
	); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", analyticsStoreRecordTable)); err != nil {
		return err
	}

	records, err := s.recordsForPersistence()
	if err != nil {
		return err
	}
	for _, record := range records {
		if _, err := tx.ExecContext(
			ctx,
			fmt.Sprintf("INSERT INTO %s(record_type, scope_id, record_id, payload) VALUES(?, ?, ?, ?)", analyticsStoreRecordTable),
			record.recordType,
			record.scopeID,
			record.recordID,
			record.payload,
		); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	tx = nil
	return nil
}

func (s *AnalyticsStore) initDB() error {
	if s.db == nil {
		return nil
	}
	ctx := context.Background()

	if _, err := s.db.ExecContext(
		ctx,
		analyticsStoreMetaTableDDL(s.dbDialect),
	); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(
		ctx,
		analyticsStoreRecordTableDDL(s.dbDialect),
	); err != nil {
		return err
	}
	return nil
}

func analyticsStoreMetaTableDDL(dialect string) string {
	if strings.TrimSpace(dialect) == "mysql" {
		return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (meta_key VARCHAR(191) PRIMARY KEY, meta_value LONGTEXT NOT NULL)", analyticsStoreMetaTable)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (meta_key TEXT PRIMARY KEY, meta_value TEXT NOT NULL)", analyticsStoreMetaTable)
}

func analyticsStoreRecordTableDDL(dialect string) string {
	if strings.TrimSpace(dialect) == "mysql" {
		return fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s (record_type VARCHAR(191) NOT NULL, scope_id VARCHAR(191) NOT NULL, record_id VARCHAR(191) NOT NULL, payload LONGTEXT NOT NULL, PRIMARY KEY(record_type, scope_id, record_id))",
			analyticsStoreRecordTable,
		)
	}
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (record_type TEXT NOT NULL, scope_id TEXT NOT NULL, record_id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(record_type, scope_id, record_id))",
		analyticsStoreRecordTable,
	)
}

type analyticsDBRecord struct {
	recordType string
	scopeID    string
	recordID   string
	payload    string
}

func (s *AnalyticsStore) recordsForPersistence() ([]analyticsDBRecord, error) {
	recordMap := make(map[string]analyticsDBRecord)

	appendRecord := func(recordType, scopeID, recordID string, payload any) error {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		key := strings.Join([]string{recordType, scopeID, recordID}, "\x00")
		recordMap[key] = analyticsDBRecord{
			recordType: recordType,
			scopeID:    scopeID,
			recordID:   recordID,
			payload:    string(data),
		}
		return nil
	}

	for _, id := range sortedKeys(s.organizations) {
		if err := appendRecord("organization", "", id, s.organizations[id]); err != nil {
			return nil, err
		}
	}
	for _, id := range sortedKeys(s.users) {
		if err := appendRecord("user", "", id, s.users[id]); err != nil {
			return nil, err
		}
	}
	for _, id := range sortedKeys(s.accessTokens) {
		if err := appendRecord("access_token", "", id, s.accessTokens[id]); err != nil {
			return nil, err
		}
	}
	for _, id := range sortedKeys(s.agents) {
		if err := appendRecord("agent", "", id, s.agents[id]); err != nil {
			return nil, err
		}
	}
	for _, id := range sortedKeys(s.projects) {
		if err := appendRecord("project", "", id, s.projects[id]); err != nil {
			return nil, err
		}
	}
	for _, projectID := range sortedKeys(s.configSnapshots) {
		items := append([]*ConfigSnapshot(nil), s.configSnapshots[projectID]...)
		sort.Slice(items, func(i, j int) bool {
			if items[i].CapturedAt.Equal(items[j].CapturedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].CapturedAt.Before(items[j].CapturedAt)
		})
		for _, item := range items {
			if item != nil {
				if err := appendRecord("config_snapshot", projectID, item.ID, item); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, projectID := range sortedKeys(s.sessionSummaries) {
		items := append([]*SessionSummary(nil), s.sessionSummaries[projectID]...)
		sort.Slice(items, func(i, j int) bool {
			if items[i].Timestamp.Equal(items[j].Timestamp) {
				return items[i].ID < items[j].ID
			}
			return items[i].Timestamp.Before(items[j].Timestamp)
		})
		for _, item := range items {
			if item != nil {
				if err := appendRecord("session_summary", projectID, item.ID, item); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, id := range sortedKeys(s.reports) {
		if err := appendRecord("report", "", id, s.reports[id]); err != nil {
			return nil, err
		}
	}
	for _, projectID := range sortedKeys(s.projectReports) {
		if err := appendRecord("project_report", "", projectID, s.projectReports[projectID]); err != nil {
			return nil, err
		}
	}
	for _, projectID := range sortedKeys(s.reportResearch) {
		if err := appendRecord("report_research", "", projectID, normalizeReportResearchStatus(s.reportResearch[projectID])); err != nil {
			return nil, err
		}
	}
	for _, projectID := range sortedKeys(s.skillSetClients) {
		if err := appendRecord("skill_set_client", "", projectID, normalizeSkillSetClientState(s.skillSetClients[projectID])); err != nil {
			return nil, err
		}
	}
	for _, projectID := range sortedKeys(s.skillSetDeployments) {
		items := append([]*SkillSetDeploymentEvent(nil), s.skillSetDeployments[projectID]...)
		sort.Slice(items, func(i, j int) bool {
			if items[i].OccurredAt.Equal(items[j].OccurredAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].OccurredAt.Before(items[j].OccurredAt)
		})
		for _, item := range items {
			if item != nil {
				if err := appendRecord("skill_set_deployment", projectID, item.ID, normalizeSkillSetDeploymentEvent(item)); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, projectID := range sortedKeys(s.skillSetVersions) {
		items := append([]*SkillSetVersion(nil), s.skillSetVersions[projectID]...)
		sort.Slice(items, func(i, j int) bool {
			if items[i].CreatedAt.Equal(items[j].CreatedAt) {
				return items[i].ID < items[j].ID
			}
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		})
		for _, item := range items {
			if item != nil {
				if err := appendRecord("skill_set_version", projectID, item.ID, normalizeSkillSetVersion(item)); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, id := range sortedKeys(s.sessionImportJobs) {
		if err := appendRecord("session_import_job", "", id, normalizeSessionImportJob(s.sessionImportJobs[id])); err != nil {
			return nil, err
		}
	}
	for _, audit := range s.audits {
		if audit != nil {
			if err := appendRecord("audit", "", audit.ID, audit); err != nil {
				return nil, err
			}
		}
	}

	keys := make([]string, 0, len(recordMap))
	for key := range recordMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	records := make([]analyticsDBRecord, 0, len(keys))
	for _, key := range keys {
		records = append(records, recordMap[key])
	}
	return records, nil
}

func (s *AnalyticsStore) loadFromDB() (bool, error) {
	if s.db == nil {
		return false, nil
	}

	ctx := context.Background()
	s.resetInMemoryState()

	var seqRaw string
	err := s.db.QueryRowContext(
		ctx,
		fmt.Sprintf("SELECT meta_value FROM %s WHERE meta_key = ?", analyticsStoreMetaTable),
		"seq",
	).Scan(&seqRaw)
	switch {
	case err == nil:
		if _, err := fmt.Sscanf(seqRaw, "%d", &s.seq); err != nil {
			return false, err
		}
	case err != sql.ErrNoRows:
		return false, err
	}

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf("SELECT record_type, scope_id, record_id, payload FROM %s ORDER BY record_type, scope_id, record_id", analyticsStoreRecordTable),
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	loaded := false
	for rows.Next() {
		var (
			recordType string
			scopeID    string
			recordID   string
			payload    string
		)
		if err := rows.Scan(&recordType, &scopeID, &recordID, &payload); err != nil {
			return false, err
		}
		if err := s.applyLoadedRecord(recordType, scopeID, recordID, []byte(payload)); err != nil {
			return false, err
		}
		loaded = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if !loaded && s.seq == 0 {
		return false, nil
	}
	return true, nil
}

func (s *AnalyticsStore) applyLoadedRecord(recordType, scopeID, recordID string, payload []byte) error {
	switch recordType {
	case "organization":
		var item Organization
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.organizations[recordID] = &item
	case "user":
		var item User
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.users[recordID] = &item
	case "access_token":
		var item AccessToken
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.accessTokens[recordID] = &item
	case "agent":
		var item Agent
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.agents[recordID] = &item
	case "project":
		var item Project
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.projects[recordID] = &item
	case "config_snapshot":
		var item ConfigSnapshot
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.configSnapshots[scopeID] = append(s.configSnapshots[scopeID], &item)
	case "session_summary":
		var item SessionSummary
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.sessionSummaries[scopeID] = append(s.sessionSummaries[scopeID], &item)
	case "report":
		var item Report
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.reports[recordID] = &item
	case "project_report":
		var item []string
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.projectReports[recordID] = append([]string(nil), item...)
	case "report_research":
		var item ReportResearchStatus
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.reportResearch[recordID] = normalizeReportResearchStatus(&item)
	case "skill_set_client":
		var item SkillSetClientState
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.skillSetClients[recordID] = normalizeSkillSetClientState(&item)
	case "skill_set_deployment":
		var item SkillSetDeploymentEvent
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.skillSetDeployments[scopeID] = append(s.skillSetDeployments[scopeID], normalizeSkillSetDeploymentEvent(&item))
	case "skill_set_version":
		var item SkillSetVersion
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.skillSetVersions[scopeID] = append(s.skillSetVersions[scopeID], normalizeSkillSetVersion(&item))
	case "session_import_job":
		var item SessionImportJob
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.sessionImportJobs[recordID] = normalizeSessionImportJob(&item)
	case "audit":
		var item AuditEvent
		if err := json.Unmarshal(payload, &item); err != nil {
			return err
		}
		s.audits = append(s.audits, &item)
	}
	return nil
}

func (s *AnalyticsStore) loadFromLegacyJSON() (bool, error) {
	if strings.TrimSpace(s.filePath) == "" {
		return false, nil
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if len(data) == 0 {
		return false, nil
	}

	var state analyticsStoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return false, err
	}

	return true, s.replaceStateLocked(state)
}

func (s *AnalyticsStore) resetInMemoryState() {
	s.seq = 0
	s.organizations = make(map[string]*Organization)
	s.users = make(map[string]*User)
	s.accessTokens = make(map[string]*AccessToken)
	s.agents = make(map[string]*Agent)
	s.projects = make(map[string]*Project)
	s.configSnapshots = make(map[string][]*ConfigSnapshot)
	s.sessionSummaries = make(map[string][]*SessionSummary)
	s.reports = make(map[string]*Report)
	s.projectReports = make(map[string][]string)
	s.reportResearch = make(map[string]*ReportResearchStatus)
	s.skillSetClients = make(map[string]*SkillSetClientState)
	s.skillSetDeployments = make(map[string][]*SkillSetDeploymentEvent)
	s.skillSetVersions = make(map[string][]*SkillSetVersion)
	s.sessionImportJobs = make(map[string]*SessionImportJob)
	s.audits = make([]*AuditEvent, 0, 32)
}

func (s *AnalyticsStore) snapshotStateLocked() analyticsStoreState {
	return analyticsStoreState{
		SchemaVersion:       analyticsStoreSchemaVersion,
		Seq:                 s.seq,
		Organizations:       s.organizations,
		Users:               s.users,
		AccessTokens:        s.accessTokens,
		Agents:              s.agents,
		Projects:            s.projects,
		ConfigSnapshots:     s.configSnapshots,
		SessionSummaries:    s.sessionSummaries,
		Reports:             s.reports,
		ProjectReports:      s.projectReports,
		ReportResearch:      normalizeReportResearchMap(s.reportResearch),
		SkillSetClients:     normalizeSkillSetClientMap(s.skillSetClients),
		SkillSetDeployments: normalizeSkillSetDeploymentMap(s.skillSetDeployments),
		SkillSetVersions:    normalizeSkillSetVersionMap(s.skillSetVersions),
		SessionImportJobs:   normalizeSessionImportJobMap(s.sessionImportJobs),
		Audits:              s.audits,
	}
}

func (s *AnalyticsStore) replaceStateLocked(state analyticsStoreState) error {
	s.seq = state.Seq
	s.organizations = ensureMap(state.Organizations)
	s.users = ensureMap(state.Users)
	s.accessTokens = ensureMap(state.AccessTokens)
	s.agents = ensureMap(state.Agents)
	s.projects = ensureMap(state.Projects)
	s.configSnapshots = ensureNestedMap(state.ConfigSnapshots)
	s.sessionSummaries = ensureNestedMap(state.SessionSummaries)
	s.reports = ensureMap(state.Reports)
	s.projectReports = ensureStringSliceMap(state.ProjectReports)
	s.reportResearch = ensureMap(normalizeReportResearchMap(state.ReportResearch))
	s.skillSetClients = ensureMap(normalizeSkillSetClientMap(state.SkillSetClients))
	s.skillSetDeployments = ensureNestedMap(normalizeSkillSetDeploymentMap(state.SkillSetDeployments))
	s.skillSetVersions = ensureNestedMap(normalizeSkillSetVersionMap(state.SkillSetVersions))
	s.sessionImportJobs = ensureMap(normalizeSessionImportJobMap(state.SessionImportJobs))
	if state.Audits == nil {
		s.audits = make([]*AuditEvent, 0, 32)
	} else {
		s.audits = state.Audits
	}
	return nil
}

func normalizeReportResearchMap(input map[string]*ReportResearchStatus) map[string]*ReportResearchStatus {
	if input == nil {
		return nil
	}
	out := make(map[string]*ReportResearchStatus, len(input))
	for key, value := range input {
		out[key] = normalizeReportResearchStatus(value)
	}
	return out
}

func normalizeReportResearchStatus(status *ReportResearchStatus) *ReportResearchStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	return &cloned
}

func normalizeSkillSetClientMap(input map[string]*SkillSetClientState) map[string]*SkillSetClientState {
	if input == nil {
		return nil
	}
	out := make(map[string]*SkillSetClientState, len(input))
	for key, value := range input {
		out[key] = normalizeSkillSetClientState(value)
	}
	return out
}

func normalizeSkillSetClientState(state *SkillSetClientState) *SkillSetClientState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.LastSyncedAt = cloneTime(state.LastSyncedAt)
	cloned.PausedAt = cloneTime(state.PausedAt)
	if !cloned.UpdatedAt.IsZero() {
		cloned.UpdatedAt = cloned.UpdatedAt.UTC()
	}
	return &cloned
}

func normalizeSkillSetDeploymentMap(input map[string][]*SkillSetDeploymentEvent) map[string][]*SkillSetDeploymentEvent {
	if input == nil {
		return nil
	}
	out := make(map[string][]*SkillSetDeploymentEvent, len(input))
	for key, items := range input {
		if len(items) == 0 {
			out[key] = []*SkillSetDeploymentEvent{}
			continue
		}
		cloned := make([]*SkillSetDeploymentEvent, 0, len(items))
		for _, item := range items {
			if normalized := normalizeSkillSetDeploymentEvent(item); normalized != nil {
				cloned = append(cloned, normalized)
			}
		}
		out[key] = cloned
	}
	return out
}

func normalizeSkillSetDeploymentEvent(event *SkillSetDeploymentEvent) *SkillSetDeploymentEvent {
	if event == nil {
		return nil
	}
	cloned := *event
	if !cloned.OccurredAt.IsZero() {
		cloned.OccurredAt = cloned.OccurredAt.UTC()
	}
	return &cloned
}

func normalizeSkillSetVersionMap(input map[string][]*SkillSetVersion) map[string][]*SkillSetVersion {
	if input == nil {
		return nil
	}
	out := make(map[string][]*SkillSetVersion, len(input))
	for key, items := range input {
		if len(items) == 0 {
			out[key] = []*SkillSetVersion{}
			continue
		}
		cloned := make([]*SkillSetVersion, 0, len(items))
		for _, item := range items {
			if normalized := normalizeSkillSetVersion(item); normalized != nil {
				cloned = append(cloned, normalized)
			}
		}
		out[key] = cloned
	}
	return out
}

func normalizeSkillSetVersion(version *SkillSetVersion) *SkillSetVersion {
	if version == nil {
		return nil
	}
	cloned := *version
	if !cloned.CreatedAt.IsZero() {
		cloned.CreatedAt = cloned.CreatedAt.UTC()
	}
	if !cloned.GeneratedAt.IsZero() {
		cloned.GeneratedAt = cloned.GeneratedAt.UTC()
	}
	cloned.BasedOnReportIDs = cloneStringSlice(version.BasedOnReportIDs)
	cloned.Summary = cloneStringSlice(version.Summary)
	cloned.ShadowEvaluation = normalizeSkillSetShadowEvaluation(version.ShadowEvaluation)
	if len(version.Files) > 0 {
		cloned.Files = make([]SkillSetVersionFile, len(version.Files))
		copy(cloned.Files, version.Files)
	} else {
		cloned.Files = []SkillSetVersionFile{}
	}
	return &cloned
}

func normalizeSkillSetShadowEvaluation(evaluation *SkillSetShadowEvaluation) *SkillSetShadowEvaluation {
	if evaluation == nil {
		return nil
	}
	cloned := *evaluation
	cloned.Score = round(cloned.Score)
	cloned.AverageConfidence = round(cloned.AverageConfidence)
	return &cloned
}

func normalizeSessionImportJobMap(input map[string]*SessionImportJob) map[string]*SessionImportJob {
	if input == nil {
		return nil
	}
	out := make(map[string]*SessionImportJob, len(input))
	for key, value := range input {
		out[key] = normalizeSessionImportJob(value)
	}
	return out
}

func normalizeSessionImportJob(job *SessionImportJob) *SessionImportJob {
	if job == nil {
		return nil
	}
	cloned := *job
	cloned.UpdatedAt = job.UpdatedAt
	cloned.StartedAt = cloneTime(job.StartedAt)
	cloned.CompletedAt = cloneTime(job.CompletedAt)
	if len(job.Sessions) > 0 {
		cloned.Sessions = make([]SessionImportJobSession, 0, len(job.Sessions))
		for _, item := range job.Sessions {
			cloned.Sessions = append(cloned.Sessions, SessionImportJobSession{
				SessionID:              item.SessionID,
				Tool:                   item.Tool,
				TokenIn:                item.TokenIn,
				TokenOut:               item.TokenOut,
				CachedInputTokens:      item.CachedInputTokens,
				ReasoningOutputTokens:  item.ReasoningOutputTokens,
				FunctionCallCount:      item.FunctionCallCount,
				ToolErrorCount:         item.ToolErrorCount,
				SessionDurationMS:      item.SessionDurationMS,
				ToolWallTimeMS:         item.ToolWallTimeMS,
				ToolCalls:              cloneIntMap(item.ToolCalls),
				ToolErrors:             cloneIntMap(item.ToolErrors),
				ToolWallTimesMS:        cloneIntMap(item.ToolWallTimesMS),
				RawQueries:             cloneStringSlice(item.RawQueries),
				Models:                 cloneStringSlice(item.Models),
				ModelProvider:          item.ModelProvider,
				FirstResponseLatencyMS: item.FirstResponseLatencyMS,
				AssistantResponses:     cloneStringSlice(item.AssistantResponses),
				ReasoningSummaries:     cloneStringSlice(item.ReasoningSummaries),
				Timestamp:              item.Timestamp,
			})
		}
	} else {
		cloned.Sessions = nil
	}
	if len(job.Failures) > 0 {
		cloned.Failures = append([]SessionImportJobFailure(nil), job.Failures...)
	} else {
		cloned.Failures = nil
	}
	return &cloned
}

func openAnalyticsStoreDB(conf *configs.Config) (*sql.DB, error) {
	driver := strings.TrimSpace(conf.DB.Dialect)
	if driver == "" {
		driver = "sqlite3"
	}

	dsn := strings.TrimSpace(conf.DB.DSN)
	if dsn == "" {
		dsn = defaultAnalyticsStoreDSN(conf.App.StorePath)
	}
	if driver == "sqlite3" {
		if path := sqliteFilePath(dsn); path != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, err
			}
		}
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	applyDBPoolConfig(db, conf)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func applyDBPoolConfig(db *sql.DB, conf *configs.Config) {
	if db == nil || conf == nil {
		return
	}
	if conf.DB.MaxIdle > 0 {
		db.SetMaxIdleConns(conf.DB.MaxIdle)
	}
	if conf.DB.MaxActive > 0 {
		db.SetMaxOpenConns(conf.DB.MaxActive)
	}
	if conf.DB.MaxLifetime > 0 {
		db.SetConnMaxLifetime(time.Duration(conf.DB.MaxLifetime) * time.Second)
	}
}

func defaultAnalyticsStoreDSN(storePath string) string {
	storePath = strings.TrimSpace(storePath)
	if storePath == "" {
		return "file:crux-store?mode=memory&cache=shared&_fk=1"
	}
	ext := filepath.Ext(storePath)
	if strings.EqualFold(ext, ".json") {
		storePath = strings.TrimSuffix(storePath, ext) + ".db"
	} else if ext == "" {
		storePath += ".db"
	}
	return storePath
}

func sqliteFilePath(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}
	if strings.Contains(dsn, "mode=memory") || dsn == ":memory:" {
		return ""
	}
	if strings.HasPrefix(dsn, "file:") {
		path := strings.TrimPrefix(dsn, "file:")
		if index := strings.Index(path, "?"); index >= 0 {
			path = path[:index]
		}
		if path == "" {
			return ""
		}
		return filepath.Clean(path)
	}
	if index := strings.Index(dsn, "?"); index >= 0 {
		dsn = dsn[:index]
	}
	return filepath.Clean(dsn)
}

func sortedKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (s *AnalyticsStore) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.PingContext(ctx)
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

func cloneIntMap(input map[string]int) map[string]int {
	if len(input) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(input))
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

func (s *AnalyticsStore) collapseProjectsLocked() bool {
	orgProjects := make(map[string][]*Project)
	for _, project := range s.projects {
		if project == nil || project.OrgID == "" {
			continue
		}
		orgProjects[project.OrgID] = append(orgProjects[project.OrgID], project)
	}

	modified := false
	for _, projects := range orgProjects {
		if len(projects) <= 1 {
			continue
		}

		canonical := latestProject(projects)
		if canonical == nil {
			continue
		}

		reportIDs := make([]string, 0, len(s.projectReports[canonical.ID]))
		seenReportIDs := make(map[string]struct{}, len(s.projectReports[canonical.ID]))
		for _, id := range s.projectReports[canonical.ID] {
			if _, exists := seenReportIDs[id]; exists {
				continue
			}
			seenReportIDs[id] = struct{}{}
			reportIDs = append(reportIDs, id)
		}

		for _, project := range projects {
			if project == nil || project.ID == canonical.ID {
				continue
			}
			modified = true

			if canonical.LastIngestedAt == nil || (project.LastIngestedAt != nil && project.LastIngestedAt.After(*canonical.LastIngestedAt)) {
				canonical.LastIngestedAt = project.LastIngestedAt
			}
			if canonical.LastProfileID == "" && project.LastProfileID != "" {
				canonical.LastProfileID = project.LastProfileID
			}
			if canonical.DefaultTool == "" && project.DefaultTool != "" {
				canonical.DefaultTool = project.DefaultTool
			}
			if canonical.RepoHash == "" && project.RepoHash != "" {
				canonical.RepoHash = project.RepoHash
			}
			if canonical.RepoPath == "" && project.RepoPath != "" {
				canonical.RepoPath = project.RepoPath
			}
			if len(canonical.LanguageMix) == 0 && len(project.LanguageMix) > 0 {
				canonical.LanguageMix = cloneFloatMap(project.LanguageMix)
			}
			if project.ConnectedAt.After(canonical.ConnectedAt) {
				canonical.AgentID = project.AgentID
				canonical.RepoHash = project.RepoHash
				canonical.RepoPath = project.RepoPath
				canonical.LanguageMix = cloneFloatMap(project.LanguageMix)
				canonical.DefaultTool = project.DefaultTool
				canonical.ConnectedAt = project.ConnectedAt
			}

			for _, snapshot := range s.configSnapshots[project.ID] {
				if snapshot == nil {
					continue
				}
				snapshot.ProjectID = canonical.ID
				s.configSnapshots[canonical.ID] = append(s.configSnapshots[canonical.ID], snapshot)
			}
			delete(s.configSnapshots, project.ID)

			for _, session := range s.sessionSummaries[project.ID] {
				if session == nil {
					continue
				}
				session.ProjectID = canonical.ID
				s.sessionSummaries[canonical.ID] = append(s.sessionSummaries[canonical.ID], session)
			}
			delete(s.sessionSummaries, project.ID)

			for _, reportID := range s.projectReports[project.ID] {
				rec := s.reports[reportID]
				if rec != nil {
					rec.ProjectID = canonical.ID
				}
				if _, exists := seenReportIDs[reportID]; exists {
					continue
				}
				seenReportIDs[reportID] = struct{}{}
				reportIDs = append(reportIDs, reportID)
			}
			delete(s.projectReports, project.ID)

			for _, rec := range s.reports {
				if rec != nil && rec.ProjectID == project.ID {
					rec.ProjectID = canonical.ID
				}
			}

			if clientState, ok := s.skillSetClients[project.ID]; ok && clientState != nil {
				clientState.ProjectID = canonical.ID
				current := s.skillSetClients[canonical.ID]
				if current == nil || clientState.UpdatedAt.After(current.UpdatedAt) {
					s.skillSetClients[canonical.ID] = clientState
				}
				delete(s.skillSetClients, project.ID)
			}
			if history, ok := s.skillSetDeployments[project.ID]; ok && len(history) > 0 {
				for _, event := range history {
					if event == nil {
						continue
					}
					event.ProjectID = canonical.ID
					s.skillSetDeployments[canonical.ID] = append(s.skillSetDeployments[canonical.ID], event)
				}
				sort.Slice(s.skillSetDeployments[canonical.ID], func(i, j int) bool {
					left := s.skillSetDeployments[canonical.ID][i]
					right := s.skillSetDeployments[canonical.ID][j]
					if left == nil || right == nil {
						return left != nil
					}
					if left.OccurredAt.Equal(right.OccurredAt) {
						return left.ID < right.ID
					}
					return left.OccurredAt.Before(right.OccurredAt)
				})
				delete(s.skillSetDeployments, project.ID)
			}
			if versions, ok := s.skillSetVersions[project.ID]; ok && len(versions) > 0 {
				for _, version := range versions {
					if version == nil {
						continue
					}
					version.ProjectID = canonical.ID
					s.skillSetVersions[canonical.ID] = append(s.skillSetVersions[canonical.ID], version)
				}
				sort.Slice(s.skillSetVersions[canonical.ID], func(i, j int) bool {
					left := s.skillSetVersions[canonical.ID][i]
					right := s.skillSetVersions[canonical.ID][j]
					if left == nil || right == nil {
						return left != nil
					}
					if left.CreatedAt.Equal(right.CreatedAt) {
						return left.ID < right.ID
					}
					return left.CreatedAt.Before(right.CreatedAt)
				})
				delete(s.skillSetVersions, project.ID)
			}

			for _, audit := range s.audits {
				if audit != nil && audit.ProjectID == project.ID {
					audit.ProjectID = canonical.ID
				}
			}

			delete(s.projects, project.ID)
		}

		if modified {
			canonical.Name = sharedWorkspaceName
			s.projectReports[canonical.ID] = reportIDs
		}
	}

	return modified
}

func latestProject(projects []*Project) *Project {
	if len(projects) == 0 {
		return nil
	}

	sorted := append([]*Project(nil), projects...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ConnectedAt.Equal(sorted[j].ConnectedAt) {
			return sorted[i].ID > sorted[j].ID
		}
		return sorted[i].ConnectedAt.After(sorted[j].ConnectedAt)
	})
	return sorted[0]
}
