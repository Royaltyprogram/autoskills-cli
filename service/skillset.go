package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

const (
	skillSetBundleSchemaVersion    = "skill-set-bundle.v1"
	managedSkillBundleName         = "crux-personal-skillset"
	managedSkillBundleTitle        = "Crux Personal Skill Set"
	managedSkillBundleDescription  = "Use as the default operating skill set for this user across coding sessions to preserve recurring clarification, planning, validation, and collaboration rules learned from prior sessions."
	skillSetDeploymentHistoryLimit = 20
	skillSetVersionHistoryLimit    = 12
	skillSetDiffFileLimit          = 4
	skillSetDiffLineLimit          = 4
)

type compiledSkillSetFile struct {
	Path    string
	Content string
}

type compiledSkillCategory struct {
	Path          string
	Category      string
	Title         string
	Rules         []string
	AntiPatterns  []string
	Confidence    float64
	ReportIDs     []string
	EvidenceLines []string

	// PreRefineRules and PreRefineAntiPatterns store the raw rules before
	// LLM refinement.  They are persisted in the manifest so the next build
	// can diff against them and only refine truly new items.
	PreRefineRules        []string
	PreRefineAntiPatterns []string
	ReusableRuleMappings  []bool
	ReusableAntiMappings  []bool
}

type compiledSkillManifest struct {
	SchemaVersion    string                        `json:"schema_version"`
	BundleName       string                        `json:"bundle_name"`
	ProjectID        string                        `json:"project_id"`
	Version          string                        `json:"version"`
	CompiledHash     string                        `json:"compiled_hash"`
	GeneratedAt      time.Time                     `json:"generated_at"`
	BasedOnReportIDs []string                      `json:"based_on_report_ids"`
	Documents        []compiledSkillManifestDocRef `json:"documents"`
}

type compiledSkillManifestDocRef struct {
	Path       string   `json:"path"`
	Category   string   `json:"category"`
	Title      string   `json:"title"`
	Confidence float64  `json:"confidence"`
	ReportIDs  []string `json:"report_ids,omitempty"`

	// Pre-refine data: stored so the next build can diff and skip
	// re-refining unchanged rules.
	PreRefineRules        []string `json:"pre_refine_rules,omitempty"`
	PreRefineAntiPatterns []string `json:"pre_refine_anti_patterns,omitempty"`
	RefinedRules          []string `json:"refined_rules,omitempty"`
	RefinedAntiPatterns   []string `json:"refined_anti_patterns,omitempty"`
	ReusableRuleMappings  []bool   `json:"reusable_rule_mappings,omitempty"`
	ReusableAntiMappings  []bool   `json:"reusable_anti_mappings,omitempty"`
}

type skillSetCandidateEvaluation struct {
	Decision         string
	Reason           string
	ShadowEvaluation *SkillSetShadowEvaluation
}

func (s *AnalyticsService) GetLatestSkillSetBundle(ctx context.Context, req *request.SkillSetBundleReq) (*response.SkillSetBundleResp, error) {
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProject(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

	reports := s.activeSkillSetReportsLocked(req.ProjectID)
	clientState := s.AnalyticsStore.skillSetClients[req.ProjectID]
	latestVersion := latestSkillSetVersion(s.AnalyticsStore.skillSetVersions[req.ProjectID])
	previousVersion := previousSkillSetVersion(s.AnalyticsStore.skillSetVersions[req.ProjectID])
	persisted := false

	var bundle *response.SkillSetBundleResp
	if latestVersion == nil && len(reports) > 0 {
		// Backfill the first bundle version from stored reports without invoking
		// the external refine agent on the read path.
		compiled, err := buildLatestSkillSetBundle(project.ID, reports)
		if err != nil {
			return nil, err
		}
		bundle = compiled
		if compiled != nil && strings.TrimSpace(compiled.Status) == "ready" {
			var versionRecorded bool
			latestVersion, previousVersion, versionRecorded = s.ensureLatestSkillSetVersionLocked(project, compiled, reports)
			persisted = versionRecorded
			if latestVersion != nil {
				bundle = skillSetBundleFromVersion(latestVersion)
			}
		}
	}
	if bundle == nil {
		bundle = skillSetBundleFromVersion(latestVersion)
	}
	if bundle == nil {
		bundle = newSkillSetStatusBundle(project.ID, len(reports) > 0)
	}

	if s.reconcileSkillSetVersionDecisionLocked(project, nil, clientState) {
		persisted = true
		latestVersion = latestSkillSetVersion(s.AnalyticsStore.skillSetVersions[req.ProjectID])
		previousVersion = previousSkillSetVersion(s.AnalyticsStore.skillSetVersions[req.ProjectID])
	}
	if persisted {
		if err := s.AnalyticsStore.persistLocked(); err != nil {
			return nil, err
		}
	}
	bundle.ClientState = toSkillSetClientStateResp(clientState)
	bundle.DeploymentHistory = toSkillSetDeploymentHistoryResp(s.AnalyticsStore.skillSetDeployments[req.ProjectID])
	bundle.VersionHistory = toSkillSetVersionHistoryResp(s.AnalyticsStore.skillSetVersions[req.ProjectID])
	bundle.LatestDiff = buildSkillSetVersionDiffResp(previousVersion, latestVersion)
	return bundle, nil
}

func (s *AnalyticsService) UpsertSkillSetClientState(ctx context.Context, req *request.SkillSetClientStateUpsertReq) (*response.SkillSetClientStateResp, error) {
	s.AnalyticsStore.mu.Lock()
	defer s.AnalyticsStore.mu.Unlock()

	project, err := s.resolveProjectLocked(ctx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeProjectAgentBinding(ctx, project); err != nil {
		return nil, err
	}
	req.ProjectID = project.ID

	identity, _ := AuthIdentityFromContext(ctx)
	now := time.Now().UTC()
	previousState := normalizeSkillSetClientState(s.AnalyticsStore.skillSetClients[req.ProjectID])
	clientState := normalizeSkillSetClientState(previousState)
	if clientState == nil {
		clientState = &SkillSetClientState{
			ProjectID: req.ProjectID,
			OrgID:     project.OrgID,
			AgentID:   project.AgentID,
		}
	}

	clientState.ProjectID = req.ProjectID
	clientState.OrgID = project.OrgID
	clientState.AgentID = firstNonEmpty(strings.TrimSpace(identity.AgentID), strings.TrimSpace(project.AgentID))
	clientState.BundleName = firstNonEmpty(strings.TrimSpace(req.BundleName), managedSkillBundleName)
	clientState.Mode = strings.TrimSpace(req.Mode)
	clientState.SyncStatus = strings.TrimSpace(req.SyncStatus)
	clientState.AppliedVersion = strings.TrimSpace(req.AppliedVersion)
	clientState.AppliedHash = strings.TrimSpace(req.AppliedHash)
	clientState.LastSyncedAt = cloneTime(req.LastSyncedAt)
	clientState.PausedAt = cloneTime(req.PausedAt)
	clientState.LastError = strings.TrimSpace(req.LastError)
	clientState.UpdatedAt = now
	s.AnalyticsStore.skillSetClients[req.ProjectID] = clientState
	s.recordSkillSetDeploymentLocked(project, previousState, clientState)
	s.reconcileSkillSetVersionDecisionLocked(project, previousState, clientState)

	s.appendAuditLocked(ctx, project.OrgID, auditEventInput{
		ProjectID: req.ProjectID,
		Type:      "skillset.client_state",
		Message:   "managed skill bundle client state updated",
		Result:    "success",
		Reason:    firstNonEmpty(clientState.SyncStatus, "skillset client state reported"),
	})
	if err := s.AnalyticsStore.persistLocked(); err != nil {
		return nil, err
	}
	return toSkillSetClientStateResp(clientState), nil
}

// skillSetBuildOptions configures optional behaviour for buildLatestSkillSetBundle.
type skillSetBuildOptions struct {
	RefineAgent     *SkillRefineAgent
	PreviousVersion *SkillSetVersion
}

func buildLatestSkillSetBundle(projectID string, reports []*Report, opts ...skillSetBuildOptions) (*response.SkillSetBundleResp, error) {
	var options skillSetBuildOptions
	if len(opts) > 0 {
		options = opts[0]
	}

	if len(reports) == 0 {
		return newSkillSetStatusBundle(projectID, false), nil
	}

	categories := compileSkillCategories(reports)
	if len(categories) == 0 {
		return newSkillSetStatusBundle(projectID, true), nil
	}

	// Snapshot pre-refine rules before any LLM transformation.
	for i := range categories {
		categories[i].PreRefineRules = append([]string(nil), categories[i].Rules...)
		categories[i].PreRefineAntiPatterns = append([]string(nil), categories[i].AntiPatterns...)
		categories[i].ReusableRuleMappings = make([]bool, len(categories[i].Rules))
		categories[i].ReusableAntiMappings = make([]bool, len(categories[i].AntiPatterns))
	}

	// Diff-based refinement: reuse previously refined text for unchanged
	// rules and only send truly new rules through the LLM.
	if options.RefineAgent != nil {
		prev := extractPreviousRefineMap(options.PreviousVersion)
		categories = diffAndRefineCategories(categories, prev, options.RefineAgent)
	}

	basedOnReportIDs := make([]string, 0, len(reports))
	for _, report := range reports {
		basedOnReportIDs = append(basedOnReportIDs, report.ID)
	}
	sort.Strings(basedOnReportIDs)

	files := make([]compiledSkillSetFile, 0, len(categories)+3)
	for _, category := range categories {
		files = append(files, compiledSkillSetFile{
			Path:    category.Path,
			Content: renderSkillCategoryFile(category),
		})
	}
	files = append(files, compiledSkillSetFile{
		Path:    "references/evidence-summary.md",
		Content: renderSkillEvidenceSummary(reports),
	})
	files = append(files, compiledSkillSetFile{
		Path:    "agents/openai.yaml",
		Content: renderSkillOpenAIYAML(),
	})

	generatedAt := latestSkillSetGeneratedAt(reports)
	versionSeed := append([]compiledSkillSetFile(nil), files...)
	versionSeed = append(versionSeed, compiledSkillSetFile{
		Path:    "SKILL.md",
		Content: renderSkillEntryPoint(managedSkillBundleName, "", generatedAt, categories),
	})
	compiledHash := computeCompiledSkillHash(versionSeed)
	version := "v" + compiledHash[:12]

	files = append(files, compiledSkillSetFile{
		Path:    "SKILL.md",
		Content: renderSkillEntryPoint(managedSkillBundleName, version, generatedAt, categories),
	})

	manifest := compiledSkillManifest{
		SchemaVersion:    skillSetBundleSchemaVersion,
		BundleName:       managedSkillBundleName,
		ProjectID:        strings.TrimSpace(projectID),
		Version:          version,
		CompiledHash:     compiledHash,
		GeneratedAt:      generatedAt,
		BasedOnReportIDs: basedOnReportIDs,
		Documents:        make([]compiledSkillManifestDocRef, 0, len(categories)),
	}
	for _, category := range categories {
		manifest.Documents = append(manifest.Documents, compiledSkillManifestDocRef{
			Path:                  category.Path,
			Category:              category.Category,
			Title:                 category.Title,
			Confidence:            category.Confidence,
			ReportIDs:             append([]string(nil), category.ReportIDs...),
			PreRefineRules:        append([]string(nil), category.PreRefineRules...),
			PreRefineAntiPatterns: append([]string(nil), category.PreRefineAntiPatterns...),
			RefinedRules:          append([]string(nil), category.Rules...),
			RefinedAntiPatterns:   append([]string(nil), category.AntiPatterns...),
			ReusableRuleMappings:  append([]bool(nil), category.ReusableRuleMappings...),
			ReusableAntiMappings:  append([]bool(nil), category.ReusableAntiMappings...),
		})
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	files = append(files, compiledSkillSetFile{
		Path:    "00-manifest.json",
		Content: string(append(manifestBytes, '\n')),
	})

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	respFiles := make([]response.SkillSetFileResp, 0, len(files))
	for _, file := range files {
		sum := sha256.Sum256([]byte(file.Content))
		respFiles = append(respFiles, response.SkillSetFileResp{
			Path:    file.Path,
			Content: file.Content,
			SHA256:  hex.EncodeToString(sum[:]),
			Bytes:   len(file.Content),
		})
	}

	return &response.SkillSetBundleResp{
		SchemaVersion:    skillSetBundleSchemaVersion,
		ProjectID:        strings.TrimSpace(projectID),
		Status:           "ready",
		BundleName:       managedSkillBundleName,
		Version:          version,
		CompiledHash:     compiledHash,
		GeneratedAt:      cloneTime(&generatedAt),
		BasedOnReportIDs: basedOnReportIDs,
		Summary:          buildSkillSetSummary(reports),
		Files:            respFiles,
	}, nil
}

func newSkillSetStatusBundle(projectID string, hasReports bool) *response.SkillSetBundleResp {
	status := "no_reports"
	if hasReports {
		status = "no_candidate"
	}
	return &response.SkillSetBundleResp{
		SchemaVersion: skillSetBundleSchemaVersion,
		ProjectID:     strings.TrimSpace(projectID),
		Status:        status,
		BundleName:    managedSkillBundleName,
	}
}

func skillSetBundleFromVersion(version *SkillSetVersion) *response.SkillSetBundleResp {
	if version == nil {
		return nil
	}

	var generatedAt *time.Time
	if !version.GeneratedAt.IsZero() {
		generatedAt = cloneTime(&version.GeneratedAt)
	}

	files := make([]response.SkillSetFileResp, 0, len(version.Files))
	for _, file := range version.Files {
		content := file.Content
		checksum := strings.TrimSpace(file.SHA256)
		if checksum == "" {
			sum := sha256.Sum256([]byte(content))
			checksum = hex.EncodeToString(sum[:])
		}
		size := file.Bytes
		if size <= 0 {
			size = len(content)
		}
		files = append(files, response.SkillSetFileResp{
			Path:    strings.TrimSpace(file.Path),
			Content: content,
			SHA256:  checksum,
			Bytes:   size,
		})
	}

	return &response.SkillSetBundleResp{
		SchemaVersion:    skillSetBundleSchemaVersion,
		ProjectID:        strings.TrimSpace(version.ProjectID),
		Status:           "ready",
		BundleName:       firstNonEmpty(strings.TrimSpace(version.BundleName), managedSkillBundleName),
		Version:          strings.TrimSpace(version.Version),
		CompiledHash:     strings.TrimSpace(version.CompiledHash),
		GeneratedAt:      generatedAt,
		BasedOnReportIDs: cloneStringSlice(version.BasedOnReportIDs),
		Summary:          cloneStringSlice(version.Summary),
		Files:            files,
	}
}

func toSkillSetClientStateResp(state *SkillSetClientState) *response.SkillSetClientStateResp {
	if state == nil {
		return nil
	}
	return &response.SkillSetClientStateResp{
		ProjectID:      strings.TrimSpace(state.ProjectID),
		AgentID:        strings.TrimSpace(state.AgentID),
		BundleName:     strings.TrimSpace(state.BundleName),
		Mode:           strings.TrimSpace(state.Mode),
		SyncStatus:     strings.TrimSpace(state.SyncStatus),
		AppliedVersion: strings.TrimSpace(state.AppliedVersion),
		AppliedHash:    strings.TrimSpace(state.AppliedHash),
		LastSyncedAt:   cloneTime(state.LastSyncedAt),
		PausedAt:       cloneTime(state.PausedAt),
		LastError:      strings.TrimSpace(state.LastError),
		UpdatedAt:      state.UpdatedAt.UTC(),
	}
}

func toSkillSetDeploymentHistoryResp(history []*SkillSetDeploymentEvent) []response.SkillSetDeploymentEventResp {
	if len(history) == 0 {
		return nil
	}
	items := make([]*SkillSetDeploymentEvent, 0, len(history))
	for _, event := range history {
		if normalized := normalizeSkillSetDeploymentEvent(event); normalized != nil {
			items = append(items, normalized)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})

	out := make([]response.SkillSetDeploymentEventResp, 0, len(items))
	for _, event := range items {
		out = append(out, response.SkillSetDeploymentEventResp{
			ID:              strings.TrimSpace(event.ID),
			ProjectID:       strings.TrimSpace(event.ProjectID),
			AgentID:         strings.TrimSpace(event.AgentID),
			BundleName:      strings.TrimSpace(event.BundleName),
			EventType:       strings.TrimSpace(event.EventType),
			Summary:         strings.TrimSpace(event.Summary),
			Mode:            strings.TrimSpace(event.Mode),
			SyncStatus:      strings.TrimSpace(event.SyncStatus),
			AppliedVersion:  strings.TrimSpace(event.AppliedVersion),
			PreviousVersion: strings.TrimSpace(event.PreviousVersion),
			AppliedHash:     strings.TrimSpace(event.AppliedHash),
			LastError:       strings.TrimSpace(event.LastError),
			OccurredAt:      event.OccurredAt.UTC(),
		})
	}
	return out
}

func toSkillSetVersionHistoryResp(history []*SkillSetVersion) []response.SkillSetVersionResp {
	if len(history) == 0 {
		return nil
	}
	items := make([]*SkillSetVersion, 0, len(history))
	for _, version := range history {
		if normalized := normalizeSkillSetVersion(version); normalized != nil {
			items = append(items, normalized)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	out := make([]response.SkillSetVersionResp, 0, len(items))
	for _, version := range items {
		out = append(out, response.SkillSetVersionResp{
			ID:                 strings.TrimSpace(version.ID),
			ProjectID:          strings.TrimSpace(version.ProjectID),
			BundleName:         strings.TrimSpace(version.BundleName),
			Version:            strings.TrimSpace(version.Version),
			CompiledHash:       strings.TrimSpace(version.CompiledHash),
			CreatedAt:          version.CreatedAt.UTC(),
			GeneratedAt:        version.GeneratedAt.UTC(),
			BasedOnReportIDs:   cloneStringSlice(version.BasedOnReportIDs),
			Summary:            cloneStringSlice(version.Summary),
			DeploymentDecision: strings.TrimSpace(version.DeploymentDecision),
			DecisionReason:     strings.TrimSpace(version.DecisionReason),
			ShadowEvaluation:   toSkillSetShadowEvaluationResp(version.ShadowEvaluation),
		})
	}
	return out
}

func toSkillSetShadowEvaluationResp(evaluation *SkillSetShadowEvaluation) *response.SkillSetShadowEvaluationResp {
	if evaluation == nil {
		return nil
	}
	normalized := normalizeSkillSetShadowEvaluation(evaluation)
	if normalized == nil {
		return nil
	}
	return &response.SkillSetShadowEvaluationResp{
		Score:                normalized.Score,
		AverageConfidence:    normalized.AverageConfidence,
		ChangedDocumentCount: normalized.ChangedDocumentCount,
		AddedRuleCount:       normalized.AddedRuleCount,
		RemovedRuleCount:     normalized.RemovedRuleCount,
		RuleChurn:            normalized.RuleChurn,
		Guardrail:            strings.TrimSpace(normalized.Guardrail),
	}
}

func buildSkillSetVersionDiffResp(previous, current *SkillSetVersion) *response.SkillSetVersionDiffResp {
	if previous == nil || current == nil {
		return nil
	}
	changedFiles := compareSkillSetVersionFiles(previous.Files, current.Files)
	if len(changedFiles) == 0 && strings.TrimSpace(previous.Version) == strings.TrimSpace(current.Version) {
		return nil
	}

	addedCount := 0
	removedCount := 0
	for _, item := range changedFiles {
		addedCount += len(item.Added)
		removedCount += len(item.Removed)
	}

	summary := make([]string, 0, 4)
	if previous.Version != "" && current.Version != "" && previous.Version != current.Version {
		summary = append(summary, fmt.Sprintf("Version %s -> %s.", previous.Version, current.Version))
	}
	if len(changedFiles) > 0 {
		summary = append(summary, fmt.Sprintf("%d document%s changed.", len(changedFiles), pluralizeCount(len(changedFiles))))
	}
	if addedCount > 0 {
		summary = append(summary, fmt.Sprintf("%d rule%s added.", addedCount, pluralizeCount(addedCount)))
	}
	if removedCount > 0 {
		summary = append(summary, fmt.Sprintf("%d rule%s removed.", removedCount, pluralizeCount(removedCount)))
	}

	respFiles := make([]response.SkillSetVersionDiffFileResp, 0, len(changedFiles))
	for _, item := range changedFiles {
		respFiles = append(respFiles, response.SkillSetVersionDiffFileResp{
			Path:    item.Path,
			Added:   cloneStringSlice(item.Added),
			Removed: cloneStringSlice(item.Removed),
		})
	}

	return &response.SkillSetVersionDiffResp{
		FromVersion:  strings.TrimSpace(previous.Version),
		ToVersion:    strings.TrimSpace(current.Version),
		Summary:      summary,
		ChangedFiles: respFiles,
	}
}

type skillSetVersionFileDiff struct {
	Path    string
	Added   []string
	Removed []string
}

func compareSkillSetVersionFiles(previous, current []SkillSetVersionFile) []skillSetVersionFileDiff {
	previousLines := collectSkillSetVersionComparableLines(previous)
	currentLines := collectSkillSetVersionComparableLines(current)

	pathSet := make(map[string]struct{}, len(previousLines)+len(currentLines))
	for path := range previousLines {
		pathSet[path] = struct{}{}
	}
	for path := range currentLines {
		pathSet[path] = struct{}{}
	}

	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	diffs := make([]skillSetVersionFileDiff, 0, len(paths))
	for _, path := range paths {
		added := diffStringSets(currentLines[path], previousLines[path], skillSetDiffLineLimit)
		removed := diffStringSets(previousLines[path], currentLines[path], skillSetDiffLineLimit)
		if len(added) == 0 && len(removed) == 0 {
			continue
		}
		diffs = append(diffs, skillSetVersionFileDiff{
			Path:    path,
			Added:   added,
			Removed: removed,
		})
		if len(diffs) == skillSetDiffFileLimit {
			break
		}
	}
	return diffs
}

func collectSkillSetVersionComparableLines(files []SkillSetVersionFile) map[string][]string {
	out := make(map[string][]string, len(files))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		lowerPath := strings.ToLower(path)
		if !strings.HasSuffix(lowerPath, ".md") || lowerPath == "skill.md" || strings.HasPrefix(lowerPath, "references/") {
			continue
		}
		seen := make(map[string]struct{})
		currentSection := ""
		for _, rawLine := range strings.Split(file.Content, "\n") {
			line := strings.TrimSpace(rawLine)
			if strings.HasPrefix(line, "## ") {
				currentSection = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
				continue
			}
			if currentSection != "rules" && currentSection != "anti-patterns" {
				continue
			}
			if !strings.HasPrefix(line, "- ") {
				continue
			}
			line = normalizeSkillLine(strings.TrimPrefix(line, "- "))
			if line == "" {
				continue
			}
			if _, exists := seen[line]; exists {
				continue
			}
			seen[line] = struct{}{}
			out[path] = append(out[path], line)
		}
	}
	return out
}

func diffStringSets(left, right []string, limit int) []string {
	if len(left) == 0 {
		return nil
	}
	rightSet := make(map[string]struct{}, len(right))
	for _, item := range right {
		rightSet[item] = struct{}{}
	}
	out := make([]string, 0, len(left))
	for _, item := range left {
		if _, exists := rightSet[item]; exists {
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

func pluralizeCount(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
}

func (s *AnalyticsService) activeSkillSetReportsLocked(projectID string) []*Report {
	reports := make([]*Report, 0, len(s.AnalyticsStore.projectReports[projectID]))
	for _, reportID := range s.AnalyticsStore.projectReports[projectID] {
		report := s.AnalyticsStore.reports[reportID]
		if report == nil || strings.TrimSpace(report.Status) != "active" {
			continue
		}
		reports = append(reports, report)
	}
	return reports
}

func (s *AnalyticsService) ensureLatestSkillSetVersionLocked(project *Project, bundle *response.SkillSetBundleResp, reports []*Report) (*SkillSetVersion, *SkillSetVersion, bool) {
	if project == nil || bundle == nil {
		return nil, nil, false
	}
	projectID := strings.TrimSpace(project.ID)
	history := s.AnalyticsStore.skillSetVersions[projectID]
	if strings.TrimSpace(bundle.Status) != "ready" {
		return latestSkillSetVersion(history), previousSkillSetVersion(history), false
	}

	latest := latestSkillSetVersion(history)
	if latest != nil && strings.TrimSpace(latest.Version) == strings.TrimSpace(bundle.Version) && strings.TrimSpace(latest.CompiledHash) == strings.TrimSpace(bundle.CompiledHash) {
		return latest, previousSkillSetVersion(history), false
	}

	now := time.Now().UTC()
	generatedAt := now
	if bundle.GeneratedAt != nil && !bundle.GeneratedAt.IsZero() {
		generatedAt = bundle.GeneratedAt.UTC()
	}
	currentFiles := snapshotSkillSetVersionFiles(bundle.Files)
	baselineVersion := latestSkillSetVersion(history)
	evaluation := evaluateSkillSetCandidate(baselineVersion, currentFiles, reports)

	version := &SkillSetVersion{
		ID:                 s.AnalyticsStore.nextID("skver"),
		ProjectID:          projectID,
		OrgID:              strings.TrimSpace(project.OrgID),
		BundleName:         firstNonEmpty(strings.TrimSpace(bundle.BundleName), managedSkillBundleName),
		Version:            strings.TrimSpace(bundle.Version),
		CompiledHash:       strings.TrimSpace(bundle.CompiledHash),
		CreatedAt:          now,
		GeneratedAt:        generatedAt,
		BasedOnReportIDs:   cloneStringSlice(bundle.BasedOnReportIDs),
		Summary:            cloneStringSlice(bundle.Summary),
		Files:              currentFiles,
		DeploymentDecision: evaluation.Decision,
		DecisionReason:     evaluation.Reason,
		ShadowEvaluation:   normalizeSkillSetShadowEvaluation(evaluation.ShadowEvaluation),
	}
	history = append(history, version)
	sort.Slice(history, func(i, j int) bool {
		if history[i] == nil || history[j] == nil {
			return history[i] != nil
		}
		if history[i].CreatedAt.Equal(history[j].CreatedAt) {
			return history[i].ID < history[j].ID
		}
		return history[i].CreatedAt.Before(history[j].CreatedAt)
	})
	if len(history) > skillSetVersionHistoryLimit {
		history = append([]*SkillSetVersion(nil), history[len(history)-skillSetVersionHistoryLimit:]...)
	}
	s.AnalyticsStore.skillSetVersions[projectID] = history
	return version, previousSkillSetVersion(history), true
}

func evaluateSkillSetCandidate(previous *SkillSetVersion, currentFiles []SkillSetVersionFile, reports []*Report) skillSetCandidateEvaluation {
	avgConfidence := averageSkillSetReportConfidence(reports)
	changedFiles := compareSkillSetVersionFiles(nil, currentFiles)
	addedCount := 0
	removedCount := 0
	if previous != nil {
		changedFiles = compareSkillSetVersionFiles(previous.Files, currentFiles)
		for _, item := range changedFiles {
			addedCount += len(item.Added)
			removedCount += len(item.Removed)
		}
	}
	totalRuleChurn := addedCount + removedCount
	score := skillSetShadowScore(avgConfidence, len(changedFiles), totalRuleChurn)
	evaluation := skillSetCandidateEvaluation{
		Decision: "shadow",
		Reason:   fmt.Sprintf("Shadow evaluation passed with score %.2f and is waiting for the connected CLI to sync.", score),
		ShadowEvaluation: &SkillSetShadowEvaluation{
			Score:                round(score),
			AverageConfidence:    round(avgConfidence),
			ChangedDocumentCount: len(changedFiles),
			AddedRuleCount:       addedCount,
			RemovedRuleCount:     removedCount,
			RuleChurn:            totalRuleChurn,
			Guardrail:            "passed",
		},
	}
	return evaluation
}

func averageSkillSetReportConfidence(reports []*Report) float64 {
	if len(reports) == 0 {
		return 0
	}
	sum := 0.0
	count := 0
	for _, report := range reports {
		if report == nil {
			continue
		}
		sum += reportSkillConfidence(report)
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func skillSetShadowScore(avgConfidence float64, changedFiles, totalRuleChurn int) float64 {
	score := avgConfidence
	score -= minFloat64(0.20, float64(changedFiles)*0.04)
	score -= minFloat64(0.20, float64(totalRuleChurn)*0.015)
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func (s *AnalyticsService) reconcileSkillSetVersionDecisionLocked(project *Project, previous, current *SkillSetClientState) bool {
	if project == nil || current == nil {
		return false
	}
	history := s.AnalyticsStore.skillSetVersions[strings.TrimSpace(project.ID)]
	if len(history) == 0 {
		return false
	}

	status := strings.ToLower(strings.TrimSpace(current.SyncStatus))
	currentVersion := strings.TrimSpace(current.AppliedVersion)
	currentHash := strings.TrimSpace(current.AppliedHash)
	latest := latestSkillSetVersionRecord(history)
	if latest == nil {
		return false
	}

	updated := false
	switch status {
	case "synced", "unchanged":
		if version := findSkillSetVersionRecord(history, currentVersion, currentHash); version != nil {
			updated = setSkillSetVersionDecision(version, "deployed", "connected CLI applied the managed bundle") || updated
		}
	case "blocked":
		if version := latestSkillSetVersionRecord(history); version != nil {
			reason := firstNonEmpty(strings.TrimSpace(current.LastError), strings.TrimSpace(version.DecisionReason), "connected CLI kept the current bundle because the candidate was blocked")
			updated = setSkillSetVersionDecision(version, "blocked", reason) || updated
		}
	case "rolled_back":
		rolledBackVersion := strings.TrimSpace(skillSetPreviousVersion(previous))
		if rolledBackVersion == "" && latest != nil {
			rolledBackVersion = strings.TrimSpace(latest.Version)
		}
		if version := findSkillSetVersionRecord(history, rolledBackVersion, ""); version != nil && strings.TrimSpace(version.Version) != currentVersion {
			updated = setSkillSetVersionDecision(version, "rolled_back", "connected CLI rolled back from this managed bundle version") || updated
		}
		if version := findSkillSetVersionRecord(history, currentVersion, currentHash); version != nil {
			updated = setSkillSetVersionDecision(version, "deployed", "connected CLI restored this managed bundle version via rollback") || updated
		}
	case "failed", "conflict":
		if version := latestSkillSetVersionRecord(history); version != nil {
			reason := firstNonEmpty(strings.TrimSpace(current.LastError), "connected CLI could not apply the managed bundle")
			updated = setSkillSetVersionDecision(version, "blocked", reason) || updated
		}
	}
	return updated
}

func setSkillSetVersionDecision(version *SkillSetVersion, decision, reason string) bool {
	if version == nil {
		return false
	}
	decision = strings.TrimSpace(decision)
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(version.DeploymentDecision) == decision && strings.TrimSpace(version.DecisionReason) == reason {
		return false
	}
	version.DeploymentDecision = decision
	version.DecisionReason = reason
	return true
}

func snapshotSkillSetVersionFiles(files []response.SkillSetFileResp) []SkillSetVersionFile {
	if len(files) == 0 {
		return []SkillSetVersionFile{}
	}
	out := make([]SkillSetVersionFile, 0, len(files))
	for _, file := range files {
		out = append(out, SkillSetVersionFile{
			Path:    strings.TrimSpace(file.Path),
			Content: file.Content,
			SHA256:  strings.TrimSpace(file.SHA256),
			Bytes:   file.Bytes,
		})
	}
	return out
}

func latestSkillSetVersion(history []*SkillSetVersion) *SkillSetVersion {
	if len(history) == 0 {
		return nil
	}
	var latest *SkillSetVersion
	for _, item := range history {
		if item == nil {
			continue
		}
		if latest == nil || item.CreatedAt.After(latest.CreatedAt) || (item.CreatedAt.Equal(latest.CreatedAt) && item.ID > latest.ID) {
			latest = item
		}
	}
	return normalizeSkillSetVersion(latest)
}

func latestSkillSetVersionRecord(history []*SkillSetVersion) *SkillSetVersion {
	if len(history) == 0 {
		return nil
	}
	var latest *SkillSetVersion
	for _, item := range history {
		if item == nil {
			continue
		}
		if latest == nil || item.CreatedAt.After(latest.CreatedAt) || (item.CreatedAt.Equal(latest.CreatedAt) && item.ID > latest.ID) {
			latest = item
		}
	}
	return latest
}

func previousSkillSetVersion(history []*SkillSetVersion) *SkillSetVersion {
	if len(history) < 2 {
		return nil
	}
	items := make([]*SkillSetVersion, 0, len(history))
	for _, item := range history {
		if normalized := normalizeSkillSetVersion(item); normalized != nil {
			items = append(items, normalized)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) < 2 {
		return nil
	}
	return items[1]
}

func findSkillSetVersionRecord(history []*SkillSetVersion, version, hash string) *SkillSetVersion {
	version = strings.TrimSpace(version)
	hash = strings.TrimSpace(hash)
	if version == "" && hash == "" {
		return nil
	}
	for _, item := range history {
		if item == nil {
			continue
		}
		if version != "" && strings.TrimSpace(item.Version) != version {
			continue
		}
		if hash != "" && strings.TrimSpace(item.CompiledHash) != hash {
			continue
		}
		return item
	}
	if hash != "" {
		for _, item := range history {
			if item != nil && strings.TrimSpace(item.CompiledHash) == hash {
				return item
			}
		}
	}
	if version != "" {
		for _, item := range history {
			if item != nil && strings.TrimSpace(item.Version) == version {
				return item
			}
		}
	}
	return nil
}

func (s *AnalyticsService) recordSkillSetDeploymentLocked(project *Project, previous, current *SkillSetClientState) {
	event := buildSkillSetDeploymentEvent(project, previous, current, s.AnalyticsStore.nextID("skdeploy"))
	if event == nil {
		return
	}
	projectID := strings.TrimSpace(event.ProjectID)
	history := append(s.AnalyticsStore.skillSetDeployments[projectID], event)
	sort.Slice(history, func(i, j int) bool {
		if history[i] == nil || history[j] == nil {
			return history[i] != nil
		}
		if history[i].OccurredAt.Equal(history[j].OccurredAt) {
			return history[i].ID < history[j].ID
		}
		return history[i].OccurredAt.Before(history[j].OccurredAt)
	})
	if len(history) > skillSetDeploymentHistoryLimit {
		history = append([]*SkillSetDeploymentEvent(nil), history[len(history)-skillSetDeploymentHistoryLimit:]...)
	}
	s.AnalyticsStore.skillSetDeployments[projectID] = history
}

func buildSkillSetDeploymentEvent(project *Project, previous, current *SkillSetClientState, eventID string) *SkillSetDeploymentEvent {
	if current == nil || !skillSetDeploymentChanged(previous, current) {
		return nil
	}
	eventType, summary := classifySkillSetDeploymentEvent(previous, current)
	if eventType == "" {
		return nil
	}

	occurredAt := current.UpdatedAt.UTC()
	if current.LastSyncedAt != nil && !current.LastSyncedAt.IsZero() {
		switch eventType {
		case "skillset_deployed", "skillset_sync_succeeded", "skillset_sync_failed", "skillset_rolled_back", "skillset_auto_blocked":
			occurredAt = current.LastSyncedAt.UTC()
		}
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	projectID := strings.TrimSpace(current.ProjectID)
	orgID := strings.TrimSpace(current.OrgID)
	if project != nil {
		projectID = firstNonEmpty(strings.TrimSpace(project.ID), projectID)
		orgID = firstNonEmpty(strings.TrimSpace(project.OrgID), orgID)
	}

	return &SkillSetDeploymentEvent{
		ID:              strings.TrimSpace(eventID),
		ProjectID:       projectID,
		OrgID:           orgID,
		AgentID:         strings.TrimSpace(current.AgentID),
		BundleName:      firstNonEmpty(strings.TrimSpace(current.BundleName), managedSkillBundleName),
		EventType:       eventType,
		Summary:         summary,
		Mode:            strings.TrimSpace(current.Mode),
		SyncStatus:      strings.TrimSpace(current.SyncStatus),
		AppliedVersion:  strings.TrimSpace(current.AppliedVersion),
		PreviousVersion: skillSetPreviousVersion(previous),
		AppliedHash:     strings.TrimSpace(current.AppliedHash),
		LastError:       strings.TrimSpace(current.LastError),
		OccurredAt:      occurredAt,
	}
}

func skillSetDeploymentChanged(previous, current *SkillSetClientState) bool {
	if current == nil {
		return false
	}
	if previous == nil {
		return strings.TrimSpace(current.Mode) != "" ||
			strings.TrimSpace(current.SyncStatus) != "" ||
			strings.TrimSpace(current.AppliedVersion) != "" ||
			strings.TrimSpace(current.AppliedHash) != "" ||
			current.PausedAt != nil ||
			strings.TrimSpace(current.LastError) != ""
	}
	return strings.TrimSpace(previous.Mode) != strings.TrimSpace(current.Mode) ||
		strings.TrimSpace(previous.SyncStatus) != strings.TrimSpace(current.SyncStatus) ||
		strings.TrimSpace(previous.AppliedVersion) != strings.TrimSpace(current.AppliedVersion) ||
		strings.TrimSpace(previous.AppliedHash) != strings.TrimSpace(current.AppliedHash) ||
		!timesMatch(previous.PausedAt, current.PausedAt) ||
		strings.TrimSpace(previous.LastError) != strings.TrimSpace(current.LastError)
}

func classifySkillSetDeploymentEvent(previous, current *SkillSetClientState) (string, string) {
	status := strings.ToLower(strings.TrimSpace(current.SyncStatus))
	currentVersion := strings.TrimSpace(current.AppliedVersion)
	previousVersion := skillSetPreviousVersion(previous)

	switch {
	case status == "rolled_back":
		if previousVersion != "" && currentVersion != "" && previousVersion != currentVersion {
			return "skillset_rolled_back", fmt.Sprintf("Rolled back the managed bundle from %s to %s.", previousVersion, currentVersion)
		}
		if currentVersion != "" {
			return "skillset_rolled_back", fmt.Sprintf("Rolled back the managed bundle to %s.", currentVersion)
		}
		return "skillset_rolled_back", "Rolled back the managed bundle to the previous backup."
	case status == "paused" || (strings.EqualFold(strings.TrimSpace(current.Mode), "frozen") && current.PausedAt != nil && (previous == nil || previous.PausedAt == nil)):
		return "skillset_paused", "Automatic managed bundle updates were paused."
	case status == "resumed" || (previous != nil && previous.PausedAt != nil && current.PausedAt == nil && !strings.EqualFold(strings.TrimSpace(current.Mode), "frozen")):
		return "skillset_resumed", "Automatic managed bundle updates resumed."
	case status == "failed" || status == "conflict":
		if errText := strings.TrimSpace(current.LastError); errText != "" {
			return "skillset_sync_failed", errText
		}
		return "skillset_sync_failed", "Managed bundle sync failed on the connected CLI."
	case status == "unsupported":
		return "skillset_auto_blocked", "The connected server does not expose managed bundle sync yet."
	case status == "no_reports":
		return "skillset_auto_blocked", "No report evidence exists yet, so there is no managed bundle to deploy."
	case status == "no_candidate":
		return "skillset_auto_blocked", "Reports exist, but the latest synthesis pass did not produce a deployable bundle."
	case status == "blocked":
		if errText := strings.TrimSpace(current.LastError); errText != "" {
			return "skillset_auto_blocked", errText
		}
		return "skillset_auto_blocked", "Shadow evaluation blocked the latest managed bundle candidate."
	case status == "synced":
		if currentVersion != "" && currentVersion != previousVersion {
			if previousVersion != "" {
				return "skillset_deployed", fmt.Sprintf("Deployed %s after replacing %s.", currentVersion, previousVersion)
			}
			return "skillset_deployed", fmt.Sprintf("Deployed %s to the connected workspace.", currentVersion)
		}
		return "skillset_sync_succeeded", "Managed bundle sync completed without changing the applied version."
	case status == "unchanged":
		if previous == nil || !strings.EqualFold(strings.TrimSpace(previous.SyncStatus), "unchanged") {
			return "skillset_sync_succeeded", "The CLI checked the current bundle and no new deployment was needed."
		}
		return "", ""
	default:
		if status == "" {
			return "", ""
		}
		return "skillset_state_updated", fmt.Sprintf("Managed bundle state changed to %s.", status)
	}
}

func skillSetPreviousVersion(state *SkillSetClientState) string {
	if state == nil {
		return ""
	}
	return strings.TrimSpace(state.AppliedVersion)
}

func timesMatch(left, right *time.Time) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return left.UTC().Equal(right.UTC())
	}
}

// ── diff-based refinement helpers ──

// previousCategoryRefineData holds the raw→refined mapping for a single
// category from the previous skill set version.
type previousCategoryRefineData struct {
	// RuleMap maps a pre-refine rule string to its refined counterpart.
	RuleMap map[string]string
	// AntiPatternMap maps a pre-refine anti-pattern to its refined counterpart.
	AntiPatternMap map[string]string
}

// extractPreviousRefineMap parses the previous SkillSetVersion's manifest
// to build per-category raw→refined mappings.
func extractPreviousRefineMap(prev *SkillSetVersion) map[string]*previousCategoryRefineData {
	if prev == nil {
		return nil
	}

	// The manifest is stored in the "00-manifest.json" file.
	var manifestContent string
	for _, f := range prev.Files {
		if f.Path == "00-manifest.json" {
			manifestContent = f.Content
			break
		}
	}
	if manifestContent == "" {
		return nil
	}

	var manifest compiledSkillManifest
	if err := json.Unmarshal([]byte(manifestContent), &manifest); err != nil {
		return nil
	}

	result := make(map[string]*previousCategoryRefineData, len(manifest.Documents))
	for _, doc := range manifest.Documents {
		data := &previousCategoryRefineData{
			RuleMap:        make(map[string]string, len(doc.PreRefineRules)),
			AntiPatternMap: make(map[string]string, len(doc.PreRefineAntiPatterns)),
		}
		for i, raw := range doc.PreRefineRules {
			if i < len(doc.RefinedRules) && i < len(doc.ReusableRuleMappings) && doc.ReusableRuleMappings[i] {
				data.RuleMap[raw] = doc.RefinedRules[i]
			}
		}
		for i, raw := range doc.PreRefineAntiPatterns {
			if i < len(doc.RefinedAntiPatterns) && i < len(doc.ReusableAntiMappings) && doc.ReusableAntiMappings[i] {
				data.AntiPatternMap[raw] = doc.RefinedAntiPatterns[i]
			}
		}
		result[doc.Category] = data
	}
	return result
}

// diffAndRefineCategories applies diff-based refinement: rules that already
// exist in the previous version are carried forward as-is; only truly new
// rules are sent to the LLM refine agent.
func diffAndRefineCategories(categories []compiledSkillCategory, prev map[string]*previousCategoryRefineData, agent *SkillRefineAgent) []compiledSkillCategory {
	if prev == nil && agent == nil {
		return categories
	}

	// Separate each category's rules into "reusable" (found in prev) and
	// "new" (needs refinement).
	type splitResult struct {
		reusedRules []string // already-refined text, in order
		reusedAntis []string
		newRawRules []string // raw text that needs refining
		newRawAntis []string
		ruleIsNew   []bool // positional: true if the rule at this index is new
		antiIsNew   []bool
	}

	splits := make([]splitResult, len(categories))
	var needsRefine bool

	for i, cat := range categories {
		var sr splitResult
		prevData := prev[cat.Category] // may be nil

		for _, raw := range cat.PreRefineRules {
			if prevData != nil {
				if refined, ok := prevData.RuleMap[raw]; ok {
					sr.reusedRules = append(sr.reusedRules, refined)
					sr.ruleIsNew = append(sr.ruleIsNew, false)
					continue
				}
			}
			sr.newRawRules = append(sr.newRawRules, raw)
			sr.ruleIsNew = append(sr.ruleIsNew, true)
			needsRefine = true
		}

		for _, raw := range cat.PreRefineAntiPatterns {
			if prevData != nil {
				if refined, ok := prevData.AntiPatternMap[raw]; ok {
					sr.reusedAntis = append(sr.reusedAntis, refined)
					sr.antiIsNew = append(sr.antiIsNew, false)
					continue
				}
			}
			sr.newRawAntis = append(sr.newRawAntis, raw)
			sr.antiIsNew = append(sr.antiIsNew, true)
			needsRefine = true
		}

		splits[i] = sr
	}

	// If nothing new, just apply the reused refined text and return.
	if !needsRefine {
		for i := range categories {
			categories[i].Rules = splits[i].reusedRules
			categories[i].AntiPatterns = splits[i].reusedAntis
			categories[i].ReusableRuleMappings = make([]bool, len(splits[i].reusedRules))
			for j := range categories[i].ReusableRuleMappings {
				categories[i].ReusableRuleMappings[j] = true
			}
			categories[i].ReusableAntiMappings = make([]bool, len(splits[i].reusedAntis))
			for j := range categories[i].ReusableAntiMappings {
				categories[i].ReusableAntiMappings[j] = true
			}
		}
		return categories
	}

	// Build categories containing only new rules and send to the LLM.
	type refinedNewItems struct {
		Rules []string
		Antis []string
	}
	newlyRefined := make(map[string]*refinedNewItems, len(categories))

	toRefine := make([]compiledSkillCategory, 0, len(categories))
	refineIndexMap := make([]int, 0, len(categories)) // maps toRefine index → original index
	for i, sr := range splits {
		if len(sr.newRawRules) == 0 && len(sr.newRawAntis) == 0 {
			continue
		}
		toRefine = append(toRefine, compiledSkillCategory{
			Category:     categories[i].Category,
			Title:        categories[i].Title,
			Rules:        sr.newRawRules,
			AntiPatterns: sr.newRawAntis,
		})
		refineIndexMap = append(refineIndexMap, i)
	}

	refineSucceeded := false
	if agent != nil && agent.CanRefine() && len(toRefine) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), defaultSkillRefineRequestTimeout)
		defer cancel()
		refined, err := agent.RefineCategories(ctx, toRefine)
		if err != nil {
			refined = toRefine // fallback: use raw text
		} else {
			refineSucceeded = true
		}
		for j, ref := range refined {
			origIdx := refineIndexMap[j]
			newlyRefined[categories[origIdx].Category] = &refinedNewItems{
				Rules: ref.Rules,
				Antis: ref.AntiPatterns,
			}
		}
	} else {
		// No agent available — use raw text for new items.
		for j := range toRefine {
			origIdx := refineIndexMap[j]
			newlyRefined[categories[origIdx].Category] = &refinedNewItems{
				Rules: splits[origIdx].newRawRules,
				Antis: splits[origIdx].newRawAntis,
			}
		}
	}

	// Merge: walk the original order, picking reused or newly-refined text.
	for i, sr := range splits {
		cat := categories[i].Category
		nr := newlyRefined[cat]

		var mergedRules []string
		ruleMappingReusable := make([]bool, 0, len(sr.ruleIsNew))
		var newRuleIdx int
		for j, isNew := range sr.ruleIsNew {
			if isNew {
				if nr != nil && newRuleIdx < len(nr.Rules) {
					mergedRules = append(mergedRules, nr.Rules[newRuleIdx])
					ruleMappingReusable = append(ruleMappingReusable, refineSucceeded)
				} else if newRuleIdx < len(sr.newRawRules) {
					mergedRules = append(mergedRules, sr.newRawRules[newRuleIdx])
					ruleMappingReusable = append(ruleMappingReusable, false)
				}
				newRuleIdx++
			} else {
				// reused: count how many non-new we've seen so far
				reusedIdx := j - newRuleIdx
				if reusedIdx < len(sr.reusedRules) {
					mergedRules = append(mergedRules, sr.reusedRules[reusedIdx])
					ruleMappingReusable = append(ruleMappingReusable, true)
				}
			}
		}

		var mergedAntis []string
		antiMappingReusable := make([]bool, 0, len(sr.antiIsNew))
		var newAntiIdx int
		for j, isNew := range sr.antiIsNew {
			if isNew {
				if nr != nil && newAntiIdx < len(nr.Antis) {
					mergedAntis = append(mergedAntis, nr.Antis[newAntiIdx])
					antiMappingReusable = append(antiMappingReusable, refineSucceeded)
				} else if newAntiIdx < len(sr.newRawAntis) {
					mergedAntis = append(mergedAntis, sr.newRawAntis[newAntiIdx])
					antiMappingReusable = append(antiMappingReusable, false)
				}
				newAntiIdx++
			} else {
				reusedIdx := j - newAntiIdx
				if reusedIdx < len(sr.reusedAntis) {
					mergedAntis = append(mergedAntis, sr.reusedAntis[reusedIdx])
					antiMappingReusable = append(antiMappingReusable, true)
				}
			}
		}

		categories[i].Rules = mergedRules
		categories[i].AntiPatterns = mergedAntis
		categories[i].ReusableRuleMappings = ruleMappingReusable
		categories[i].ReusableAntiMappings = antiMappingReusable
	}

	return categories
}

func compileSkillCategories(reports []*Report) []compiledSkillCategory {
	categoryOrder := []struct {
		Category string
		Path     string
		Title    string
	}{
		{Category: "clarification", Path: "01-clarification.md", Title: "Clarify Before Building"},
		{Category: "planning", Path: "02-planning.md", Title: "Plan Before Touching Code"},
		{Category: "validation", Path: "03-validation.md", Title: "Validate Before Declaring Done"},
		{Category: "execution", Path: "04-execution.md", Title: "Keep Execution Narrow"},
	}

	type categoryAccumulator struct {
		meta           compiledSkillCategory
		rulesSeen      map[string]struct{}
		antiSeen       map[string]struct{}
		evidenceSeen   map[string]struct{}
		reportIDSeen   map[string]struct{}
		confidenceSum  float64
		confidenceHits int
	}

	accumulators := make(map[string]*categoryAccumulator, len(categoryOrder))
	for _, item := range categoryOrder {
		accumulators[item.Category] = &categoryAccumulator{
			meta: compiledSkillCategory{
				Path:     item.Path,
				Category: item.Category,
				Title:    item.Title,
			},
			rulesSeen:    make(map[string]struct{}),
			antiSeen:     make(map[string]struct{}),
			evidenceSeen: make(map[string]struct{}),
			reportIDSeen: make(map[string]struct{}),
		}
	}

	for _, report := range reports {
		category := categorizeReportForSkillSet(report)
		acc, ok := accumulators[category]
		if !ok {
			continue
		}
		for _, rule := range extractSkillRules(report) {
			if _, exists := acc.rulesSeen[rule]; exists {
				continue
			}
			acc.rulesSeen[rule] = struct{}{}
			acc.meta.Rules = append(acc.meta.Rules, rule)
		}
		for _, anti := range extractSkillAntiPatterns(report) {
			if _, exists := acc.antiSeen[anti]; exists {
				continue
			}
			acc.antiSeen[anti] = struct{}{}
			acc.meta.AntiPatterns = append(acc.meta.AntiPatterns, anti)
		}
		for _, evidence := range buildSkillEvidenceLines(report) {
			if _, exists := acc.evidenceSeen[evidence]; exists {
				continue
			}
			acc.evidenceSeen[evidence] = struct{}{}
			acc.meta.EvidenceLines = append(acc.meta.EvidenceLines, evidence)
		}
		if report != nil && strings.TrimSpace(report.ID) != "" {
			if _, exists := acc.reportIDSeen[report.ID]; !exists {
				acc.reportIDSeen[report.ID] = struct{}{}
				acc.meta.ReportIDs = append(acc.meta.ReportIDs, report.ID)
			}
		}
		acc.confidenceSum += reportSkillConfidence(report)
		acc.confidenceHits++
	}

	compiled := make([]compiledSkillCategory, 0, len(categoryOrder))
	for _, item := range categoryOrder {
		acc := accumulators[item.Category]
		if acc == nil || (len(acc.meta.Rules) == 0 && len(acc.meta.AntiPatterns) == 0) {
			continue
		}
		if acc.confidenceHits > 0 {
			acc.meta.Confidence = acc.confidenceSum / float64(acc.confidenceHits)
		}
		sort.Strings(acc.meta.ReportIDs)
		compiled = append(compiled, acc.meta)
	}
	return compiled
}

func categorizeReportForSkillSet(report *Report) string {
	text := strings.ToLower(strings.Join([]string{
		trimReportString(report, func(item *Report) string { return item.Kind }),
		trimReportString(report, func(item *Report) string { return item.Title }),
		trimReportString(report, func(item *Report) string { return item.Summary }),
		trimReportString(report, func(item *Report) string { return item.Reason }),
		strings.Join(trimReportSlice(report, func(item *Report) []string { return item.Frictions }), " "),
		strings.Join(trimReportSlice(report, func(item *Report) []string { return item.NextSteps }), " "),
	}, " "))

	switch {
	case containsAny(text, "clarif", "requirement", "assumption", "question"):
		return "clarification"
	case containsAny(text, "verify", "validation", "test", "regression", "check"):
		return "validation"
	case containsAny(text, "minimal", "small patch", "scope", "narrow", "compare"):
		return "execution"
	default:
		return "planning"
	}
}

func extractSkillRules(report *Report) []string {
	candidates := append([]string(nil), trimReportSlice(report, func(item *Report) []string { return item.NextSteps })...)
	if len(candidates) == 0 {
		candidates = append(candidates, trimReportString(report, func(item *Report) string { return item.Reason }))
	}
	if len(candidates) == 0 {
		candidates = append(candidates, trimReportString(report, func(item *Report) string { return item.Summary }))
	}
	out := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, item := range candidates {
		rule := normalizeSkillLine(item)
		if rule == "" {
			continue
		}
		if _, exists := seen[rule]; exists {
			continue
		}
		seen[rule] = struct{}{}
		out = append(out, rule)
	}
	return out
}

func extractSkillAntiPatterns(report *Report) []string {
	candidates := append([]string(nil), trimReportSlice(report, func(item *Report) []string { return item.Frictions })...)
	if len(candidates) == 0 {
		candidates = append(candidates, trimReportString(report, func(item *Report) string { return item.Risk }))
	}
	out := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, item := range candidates {
		line := normalizeSkillLine(item)
		if line == "" {
			continue
		}
		if _, exists := seen[line]; exists {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}

func buildSkillEvidenceLines(report *Report) []string {
	if report == nil {
		return nil
	}
	lines := make([]string, 0, len(report.Evidence)+1)
	if reason := strings.TrimSpace(report.Reason); reason != "" {
		lines = append(lines, report.ID+": "+reason)
	}
	for _, evidence := range report.Evidence {
		evidence = normalizeSkillLine(evidence)
		if evidence == "" {
			continue
		}
		lines = append(lines, report.ID+": "+evidence)
	}
	return lines
}

func reportSkillConfidence(report *Report) float64 {
	if report == nil {
		return 0
	}
	base := report.Score
	if base <= 0 {
		base = 0.7
	}
	switch strings.ToLower(strings.TrimSpace(report.Confidence)) {
	case "high":
		return maxFloat64(base, 0.9)
	case "medium":
		return maxFloat64(base, 0.75)
	case "low":
		return maxFloat64(base, 0.55)
	default:
		if base > 1 {
			return 1
		}
		return base
	}
}

func renderSkillEntryPoint(bundleName, version string, generatedAt time.Time, categories []compiledSkillCategory) string {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("name: " + bundleName + "\n")
	builder.WriteString("description: " + managedSkillBundleDescription + "\n")
	builder.WriteString("---\n\n")
	builder.WriteString("# " + managedSkillBundleTitle + "\n\n")
	builder.WriteString("This skill is managed automatically by Crux and should be treated as the user's standing policy layer.\n\n")
	builder.WriteString("Generated at: `" + generatedAt.UTC().Format(time.RFC3339) + "`\n\n")
	if version != "" {
		builder.WriteString("Version: `" + version + "`\n\n")
	}
	builder.WriteString("## Workflow\n\n")
	builder.WriteString("1. Treat the documents below as cumulative standing instructions for this user.\n")
	builder.WriteString("2. Load only the category documents that are relevant to the current request.\n")
	builder.WriteString("3. Preserve prior collaboration rules across follow-up turns unless a newer rule overrides them.\n")
	builder.WriteString("4. Use the evidence summary only when you need to explain why a rule exists.\n\n")
	builder.WriteString("## Included documents\n\n")
	for _, category := range categories {
		builder.WriteString("- [`" + category.Path + "`](" + filepath.ToSlash(category.Path) + ")")
		if title := strings.TrimSpace(category.Title); title != "" {
			builder.WriteString(" - " + title)
		}
		builder.WriteByte('\n')
	}
	builder.WriteString("\n## References\n\n")
	builder.WriteString("- [`references/evidence-summary.md`](references/evidence-summary.md)\n")
	return builder.String()
}

func renderSkillOpenAIYAML() string {
	var builder strings.Builder
	builder.WriteString("interface:\n")
	builder.WriteString("  display_name: \"" + managedSkillBundleTitle + "\"\n")
	builder.WriteString("  short_description: \"Auto-synced personal operating rules from Crux\"\n")
	builder.WriteString("  default_prompt: \"Use $" + managedSkillBundleName + " as the default operating skill set for this user, then follow the relevant category documents before responding.\"\n\n")
	builder.WriteString("policy:\n")
	builder.WriteString("  allow_implicit_invocation: true\n")
	return builder.String()
}

func renderSkillCategoryFile(category compiledSkillCategory) string {
	var builder strings.Builder
	builder.WriteString("# " + strings.TrimSpace(category.Title) + "\n\n")
	if category.Confidence > 0 {
		builder.WriteString(fmt.Sprintf("Confidence: `%.2f`\n\n", category.Confidence))
	}
	if len(category.Rules) > 0 {
		builder.WriteString("## Rules\n\n")
		for _, rule := range category.Rules {
			builder.WriteString("- " + rule + "\n")
		}
		builder.WriteByte('\n')
	}
	if len(category.AntiPatterns) > 0 {
		builder.WriteString("## Anti-patterns\n\n")
		for _, anti := range category.AntiPatterns {
			builder.WriteString("- " + anti + "\n")
		}
		builder.WriteByte('\n')
	}
	if len(category.EvidenceLines) > 0 {
		builder.WriteString("## Evidence\n\n")
		for _, evidence := range category.EvidenceLines {
			builder.WriteString("- " + evidence + "\n")
		}
	}
	return builder.String()
}

func renderSkillEvidenceSummary(reports []*Report) string {
	var builder strings.Builder
	builder.WriteString("# Evidence Summary\n\n")
	for _, report := range reports {
		if report == nil {
			continue
		}
		title := strings.TrimSpace(report.Title)
		if title == "" {
			title = report.ID
		}
		builder.WriteString("## " + title + "\n\n")
		if reason := strings.TrimSpace(report.Reason); reason != "" {
			builder.WriteString("- Reason: " + reason + "\n")
		}
		if impact := strings.TrimSpace(report.ExpectedImpact); impact != "" {
			builder.WriteString("- Expected impact: " + impact + "\n")
		}
		for _, evidence := range trimReportSlice(report, func(item *Report) []string { return item.Evidence }) {
			builder.WriteString("- Evidence: " + evidence + "\n")
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func computeCompiledSkillHash(files []compiledSkillSetFile) string {
	filtered := make([]compiledSkillSetFile, 0, len(files))
	for _, file := range files {
		if strings.EqualFold(strings.TrimSpace(file.Path), "00-manifest.json") {
			continue
		}
		filtered = append(filtered, file)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Path < filtered[j].Path
	})

	hasher := sha256.New()
	for _, file := range filtered {
		_, _ = hasher.Write([]byte(strings.TrimSpace(file.Path)))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(file.Content))
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func buildSkillSetSummary(reports []*Report) []string {
	summary := make([]string, 0, min(3, len(reports)))
	for _, report := range reports {
		if report == nil {
			continue
		}
		line := strings.TrimSpace(report.ExpectedImpact)
		if line == "" {
			line = strings.TrimSpace(report.Summary)
		}
		if line == "" {
			line = strings.TrimSpace(report.Reason)
		}
		line = normalizeSkillLine(line)
		if line == "" || slices.Contains(summary, line) {
			continue
		}
		summary = append(summary, line)
		if len(summary) == 3 {
			break
		}
	}
	return summary
}

func latestSkillSetGeneratedAt(reports []*Report) time.Time {
	latest := time.Time{}
	for _, report := range reports {
		if report == nil {
			continue
		}
		if report.CreatedAt.After(latest) {
			latest = report.CreatedAt
		}
	}
	if latest.IsZero() {
		latest = time.Now().UTC()
	}
	return latest.UTC()
}

func normalizeSkillLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimLeft(value, "-*0123456789. ")
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".")
	return strings.TrimSpace(value)
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(strings.TrimSpace(needle))) {
			return true
		}
	}
	return false
}

func trimReportString(report *Report, getter func(*Report) string) string {
	if report == nil || getter == nil {
		return ""
	}
	return strings.TrimSpace(getter(report))
}

func trimReportSlice(report *Report, getter func(*Report) []string) []string {
	if report == nil || getter == nil {
		return nil
	}
	out := make([]string, 0, len(getter(report)))
	for _, item := range getter(report) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func maxFloat64(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
