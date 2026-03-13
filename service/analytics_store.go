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
	filePath       string
	allowDemoUser  bool
	bootstrapUsers []configs.BootstrapUser
	seq            uint64

	organizations    map[string]*Organization
	users            map[string]*User
	accessTokens     map[string]*AccessToken
	agents           map[string]*Agent
	projects         map[string]*Project
	configSnapshots  map[string][]*ConfigSnapshot
	sessionSummaries map[string][]*SessionSummary
	reports          map[string]*Report
	projectReports   map[string][]string
	reportResearch   map[string]*ReportResearchStatus
	audits           []*AuditEvent
}

type analyticsStoreState struct {
	SchemaVersion    string                           `json:"schema_version"`
	Seq              uint64                           `json:"seq"`
	Organizations    map[string]*Organization         `json:"organizations"`
	Users            map[string]*User                 `json:"users"`
	AccessTokens     map[string]*AccessToken          `json:"access_tokens"`
	Agents           map[string]*Agent                `json:"agents"`
	Projects         map[string]*Project              `json:"projects"`
	ConfigSnapshots  map[string][]*ConfigSnapshot     `json:"config_snapshots"`
	SessionSummaries map[string][]*SessionSummary     `json:"session_summaries"`
	Reports          map[string]*Report               `json:"reports"`
	ProjectReports   map[string][]string              `json:"project_reports"`
	ReportResearch   map[string]*ReportResearchStatus `json:"report_research"`
	Audits           []*AuditEvent                    `json:"audits"`
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
	PasswordSalt string
	PasswordHash string
	CreatedAt    time.Time
	LastLoginAt  *time.Time
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

type AuditEvent struct {
	ID        string
	OrgID     string
	ProjectID string
	Type      string
	Message   string
	CreatedAt time.Time
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

	store := &AnalyticsStore{
		db:               db,
		filePath:         conf.App.StorePath,
		allowDemoUser:    conf.AllowsDemoUser(),
		bootstrapUsers:   append([]configs.BootstrapUser(nil), conf.Auth.BootstrapUsers...),
		organizations:    make(map[string]*Organization),
		users:            make(map[string]*User),
		accessTokens:     make(map[string]*AccessToken),
		agents:           make(map[string]*Agent),
		projects:         make(map[string]*Project),
		configSnapshots:  make(map[string][]*ConfigSnapshot),
		sessionSummaries: make(map[string][]*SessionSummary),
		reports:          make(map[string]*Report),
		projectReports:   make(map[string][]string),
		reportResearch:   make(map[string]*ReportResearchStatus),
		audits:           make([]*AuditEvent, 0, 32),
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
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (meta_key TEXT PRIMARY KEY, meta_value TEXT NOT NULL)", analyticsStoreMetaTable),
	); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s (record_type TEXT NOT NULL, scope_id TEXT NOT NULL, record_id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(record_type, scope_id, record_id))",
			analyticsStoreRecordTable,
		),
	); err != nil {
		return err
	}
	return nil
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
	s.audits = make([]*AuditEvent, 0, 32)
}

func (s *AnalyticsStore) snapshotStateLocked() analyticsStoreState {
	return analyticsStoreState{
		SchemaVersion:    analyticsStoreSchemaVersion,
		Seq:              s.seq,
		Organizations:    s.organizations,
		Users:            s.users,
		AccessTokens:     s.accessTokens,
		Agents:           s.agents,
		Projects:         s.projects,
		ConfigSnapshots:  s.configSnapshots,
		SessionSummaries: s.sessionSummaries,
		Reports:          s.reports,
		ProjectReports:   s.projectReports,
		ReportResearch:   normalizeReportResearchMap(s.reportResearch),
		Audits:           s.audits,
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
