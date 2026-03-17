package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

const (
	skillSetLocalStateSchemaVersion = "crux-skillset-state.v1"
	skillSetBundleSchemaVersion     = "skill-set-bundle.v1"
	managedSkillBundleName          = "crux-personal-skillset"
	skillSetModeAutopilot           = "autopilot"
	skillSetModeFrozen              = "frozen"
)

type skillSetLocalState struct {
	SchemaVersion  string     `json:"schema_version"`
	Mode           string     `json:"mode"`
	BundleName     string     `json:"bundle_name"`
	AppliedVersion string     `json:"applied_version,omitempty"`
	AppliedHash    string     `json:"applied_hash,omitempty"`
	LastSyncAt     *time.Time `json:"last_sync_at,omitempty"`
	LastSyncStatus string     `json:"last_sync_status,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	PausedAt       *time.Time `json:"paused_at,omitempty"`
}

type skillSetSyncResp struct {
	Status           string     `json:"status"`
	Mode             string     `json:"mode"`
	BundleName       string     `json:"bundle_name"`
	BundlePath       string     `json:"bundle_path,omitempty"`
	StatePath        string     `json:"state_path,omitempty"`
	AppliedVersion   string     `json:"applied_version,omitempty"`
	PreviousVersion  string     `json:"previous_version,omitempty"`
	CompiledHash     string     `json:"compiled_hash,omitempty"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	PausedAt         *time.Time `json:"paused_at,omitempty"`
	Summary          []string   `json:"summary,omitempty"`
	BasedOnReportIDs []string   `json:"based_on_report_ids,omitempty"`
	BackupPath       string     `json:"backup_path,omitempty"`
	Error            string     `json:"error,omitempty"`
}

type skillSetStatusResp struct {
	SchemaVersion    string                `json:"schema_version"`
	Mode             string                `json:"mode"`
	BundleName       string                `json:"bundle_name"`
	BundlePath       string                `json:"bundle_path"`
	StatePath        string                `json:"state_path"`
	AppliedVersion   string                `json:"applied_version,omitempty"`
	AppliedHash      string                `json:"applied_hash,omitempty"`
	LastSyncAt       *time.Time            `json:"last_sync_at,omitempty"`
	LastSyncStatus   string                `json:"last_sync_status,omitempty"`
	LastError        string                `json:"last_error,omitempty"`
	PausedAt         *time.Time            `json:"paused_at,omitempty"`
	AvailableBackups []string              `json:"available_backups,omitempty"`
	ConflictDiff     *skillSetConflictDiff `json:"conflict_diff,omitempty"`
	ResolveHint      string                `json:"resolve_hint,omitempty"`
}

type localSkillSetManifest struct {
	Version      string `json:"version"`
	CompiledHash string `json:"compiled_hash"`
	BundleName   string `json:"bundle_name"`
}

func runSkills(args []string) error {
	if len(args) == 0 {
		return runSkillsStatus(nil)
	}

	switch strings.TrimSpace(args[0]) {
	case "status":
		return runSkillsStatus(args[1:])
	case "sync":
		return runSkillsSync(args[1:])
	case "pause":
		return runSkillsPause(args[1:])
	case "resume":
		return runSkillsResume(args[1:])
	case "rollback":
		return runSkillsRollback(args[1:])
	case "resolve":
		return runSkillsResolve(args[1:])
	case "diff":
		return runSkillsDiff(args[1:])
	default:
		return fmt.Errorf("unknown skills subcommand %q", args[0])
	}
}

func runSkillsStatus(args []string) error {
	fs := flag.NewFlagSet("skills status", flag.ContinueOnError)
	codexHome := fs.String("codex-home", "", "override Codex home used for managed skill bundles")
	if err := fs.Parse(args); err != nil {
		return err
	}

	status, err := currentSkillSetStatus(*codexHome)
	if err != nil {
		return err
	}
	return prettyPrint(status)
}

func runSkillsSync(args []string) error {
	fs := flag.NewFlagSet("skills sync", flag.ContinueOnError)
	codexHome := fs.String("codex-home", "", "override Codex home used for managed skill bundles")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	resp, err := syncLatestSkillSet(&st, newStateAPIClient(&st), *codexHome)
	if err != nil {
		return err
	}
	return prettyPrint(resp)
}

func runSkillsPause(args []string) error {
	fs := flag.NewFlagSet("skills pause", flag.ContinueOnError)
	codexHome := fs.String("codex-home", "", "override Codex home used for managed skill bundles")
	if err := fs.Parse(args); err != nil {
		return err
	}

	current, _, err := loadSkillSetState()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	current.Mode = skillSetModeFrozen
	current.PausedAt = cloneTime(&now)
	current.LastSyncStatus = "paused"
	current.LastError = ""
	if err := saveSkillSetState(current); err != nil {
		return err
	}
	if err := reportSkillSetClientStateIfPossible(current); err != nil {
		return err
	}
	status, err := currentSkillSetStatus(*codexHome)
	if err != nil {
		return err
	}
	return prettyPrint(status)
}

func runSkillsResume(args []string) error {
	fs := flag.NewFlagSet("skills resume", flag.ContinueOnError)
	codexHome := fs.String("codex-home", "", "override Codex home used for managed skill bundles")
	if err := fs.Parse(args); err != nil {
		return err
	}

	current, _, err := loadSkillSetState()
	if err != nil {
		return err
	}
	current.Mode = skillSetModeAutopilot
	current.PausedAt = nil
	if strings.TrimSpace(current.LastSyncStatus) == "paused" {
		current.LastSyncStatus = "resumed"
	}
	current.LastError = ""
	if err := saveSkillSetState(current); err != nil {
		return err
	}
	if err := reportSkillSetClientStateIfPossible(current); err != nil {
		return err
	}
	status, err := currentSkillSetStatus(*codexHome)
	if err != nil {
		return err
	}
	return prettyPrint(status)
}

func runSkillsRollback(args []string) error {
	fs := flag.NewFlagSet("skills rollback", flag.ContinueOnError)
	version := fs.String("version", "", "managed skill bundle version to restore")
	codexHome := fs.String("codex-home", "", "override Codex home used for managed skill bundles")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targetVersion := strings.TrimSpace(*version)
	if targetVersion == "" && fs.NArg() > 0 {
		targetVersion = strings.TrimSpace(fs.Arg(0))
	}
	if targetVersion == "" {
		return errors.New("skills rollback requires --version")
	}

	current, _, err := loadSkillSetState()
	if err != nil {
		return err
	}
	codexRoot, err := codexHomePath(*codexHome)
	if err != nil {
		return err
	}
	livePath := skillSetLiveBundlePath(codexRoot, current.BundleName)
	backupPath, err := skillSetBackupVersionPath(targetVersion)
	if err != nil {
		return err
	}
	if !fileExists(backupPath) {
		return fmt.Errorf("managed skill backup %q does not exist", targetVersion)
	}
	if err := assertManagedSkillBundleUntouched(livePath, current); err != nil {
		return err
	}

	previousVersion := strings.TrimSpace(current.AppliedVersion)
	previousHash := strings.TrimSpace(current.AppliedHash)
	currentVersionBackup := ""
	if fileExists(livePath) && previousVersion != "" && previousVersion != targetVersion {
		currentVersionBackup, err = backupLiveSkillSetBundle(livePath, previousVersion)
		if err != nil {
			return err
		}
	}

	tempPath, err := stageCopiedSkillSetBundle(backupPath, filepath.Dir(livePath), current.BundleName)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempPath)

	if err := os.MkdirAll(filepath.Dir(livePath), 0o755); err != nil {
		return err
	}
	if fileExists(livePath) {
		if err := os.RemoveAll(livePath); err != nil {
			return err
		}
	}
	if err := os.Rename(tempPath, livePath); err != nil {
		return err
	}

	restoredHash, err := hashManagedSkillBundle(livePath)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	current.BundleName = firstNonEmpty(strings.TrimSpace(current.BundleName), managedSkillBundleName)
	current.AppliedVersion = targetVersion
	current.AppliedHash = restoredHash
	current.LastSyncAt = cloneTime(&now)
	current.LastSyncStatus = "rolled_back"
	current.LastError = ""
	if err := saveSkillSetState(current); err != nil {
		return err
	}
	if err := reportSkillSetClientStateIfPossible(current); err != nil {
		return err
	}

	return prettyPrint(skillSetSyncResp{
		Status:          "rolled_back",
		Mode:            current.Mode,
		BundleName:      current.BundleName,
		BundlePath:      livePath,
		StatePath:       mustSkillSetStatePath(),
		AppliedVersion:  targetVersion,
		PreviousVersion: previousVersion,
		CompiledHash:    restoredHash,
		LastSyncedAt:    cloneTime(&now),
		PausedAt:        cloneTime(current.PausedAt),
		BackupPath:      currentVersionBackup,
		Error:           skillRollbackHashChange(previousHash, restoredHash),
	})
}

// skillSetConflictDiff describes the differences between the local bundle and
// the last-applied backup version.
type skillSetConflictDiff struct {
	BundlePath     string                    `json:"bundle_path"`
	AppliedVersion string                    `json:"applied_version,omitempty"`
	HasConflict    bool                      `json:"has_conflict"`
	ModifiedFiles  []skillSetFileDiffSummary `json:"modified_files,omitempty"`
	AddedFiles     []string                  `json:"added_files,omitempty"`
	RemovedFiles   []string                  `json:"removed_files,omitempty"`
}

type skillSetFileDiffSummary struct {
	Path       string `json:"path"`
	AddedLines int    `json:"added_lines"`
	RemovedLines int  `json:"removed_lines"`
}

type skillSetResolveResp struct {
	Action         string     `json:"action"`
	Status         string     `json:"status"`
	BundleName     string     `json:"bundle_name"`
	BundlePath     string     `json:"bundle_path"`
	AppliedVersion string     `json:"applied_version,omitempty"`
	CompiledHash   string     `json:"compiled_hash,omitempty"`
	BackupPath     string     `json:"backup_path,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	Error          string     `json:"error,omitempty"`
}

func runSkillsDiff(args []string) error {
	fs := flag.NewFlagSet("skills diff", flag.ContinueOnError)
	codexHome := fs.String("codex-home", "", "override Codex home used for managed skill bundles")
	if err := fs.Parse(args); err != nil {
		return err
	}

	diff, err := diffManagedSkillBundle(*codexHome)
	if err != nil {
		return err
	}
	return prettyPrint(diff)
}

func runSkillsResolve(args []string) error {
	fs := flag.NewFlagSet("skills resolve", flag.ContinueOnError)
	codexHome := fs.String("codex-home", "", "override Codex home used for managed skill bundles")
	action := fs.String("action", "", "resolve action: keep-local, accept-remote, backup-and-sync")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resolveAction := strings.TrimSpace(*action)
	if resolveAction == "" && fs.NArg() > 0 {
		resolveAction = strings.TrimSpace(fs.Arg(0))
	}

	current, _, err := loadSkillSetState()
	if err != nil {
		return err
	}
	codexRoot, err := codexHomePath(*codexHome)
	if err != nil {
		return err
	}
	livePath := skillSetLiveBundlePath(codexRoot, current.BundleName)

	// If no conflict exists, report that and exit.
	if err := assertManagedSkillBundleUntouched(livePath, current); err == nil {
		return prettyPrint(skillSetResolveResp{
			Action:         "none",
			Status:         "no_conflict",
			BundleName:     current.BundleName,
			BundlePath:     livePath,
			AppliedVersion: current.AppliedVersion,
			CompiledHash:   current.AppliedHash,
		})
	}

	if resolveAction == "" {
		// No action specified — show the conflict diff and available actions.
		diff, err := diffManagedSkillBundle(*codexHome)
		if err != nil {
			return err
		}
		return prettyPrint(struct {
			Status           string               `json:"status"`
			Message          string               `json:"message"`
			Diff             skillSetConflictDiff `json:"diff"`
			AvailableActions []string             `json:"available_actions"`
			Examples         []string             `json:"examples"`
		}{
			Status:  "conflict",
			Message: "Local modifications detected in the managed skill bundle. Choose a resolve action.",
			Diff:    diff,
			AvailableActions: []string{
				"keep-local",
				"accept-remote",
				"backup-and-sync",
			},
			Examples: []string{
				"crux skills resolve --action keep-local       # pause auto-sync and keep your local changes",
				"crux skills resolve --action accept-remote    # discard local changes and sync latest from server",
				"crux skills resolve --action backup-and-sync  # backup local changes, then sync latest from server",
			},
		})
	}

	switch resolveAction {
	case "keep-local":
		return resolveKeepLocal(current, livePath)
	case "accept-remote":
		return resolveAcceptRemote(current, codexRoot, *codexHome)
	case "backup-and-sync":
		return resolveBackupAndSync(current, livePath, codexRoot, *codexHome)
	default:
		return fmt.Errorf("unknown resolve action %q; use keep-local, accept-remote, or backup-and-sync", resolveAction)
	}
}

func resolveKeepLocal(current skillSetLocalState, livePath string) error {
	_, err := doResolveKeepLocal(current, livePath)
	if err != nil {
		return err
	}
	reloaded, _, _ := loadSkillSetState()
	codexRoot, _ := codexHomePath("")
	return prettyPrint(skillSetResolveResp{
		Action:         "keep-local",
		Status:         "resolved",
		BundleName:     reloaded.BundleName,
		BundlePath:     skillSetLiveBundlePath(codexRoot, reloaded.BundleName),
		AppliedVersion: reloaded.AppliedVersion,
		CompiledHash:   reloaded.AppliedHash,
	})
}

func doResolveKeepLocal(current skillSetLocalState, livePath string) (skillSetResolveResp, error) {
	now := time.Now().UTC()
	newHash, err := hashManagedSkillBundle(livePath)
	if err != nil {
		return skillSetResolveResp{}, err
	}
	current.Mode = skillSetModeFrozen
	current.PausedAt = cloneTime(&now)
	current.AppliedHash = newHash
	current.LastSyncStatus = "resolved_keep_local"
	current.LastSyncAt = cloneTime(&now)
	current.LastError = ""
	if err := saveSkillSetState(current); err != nil {
		return skillSetResolveResp{}, err
	}
	if err := reportSkillSetClientStateIfPossible(current); err != nil {
		return skillSetResolveResp{}, err
	}
	return skillSetResolveResp{
		Action:         "keep-local",
		Status:         "resolved",
		BundleName:     current.BundleName,
		BundlePath:     livePath,
		AppliedVersion: current.AppliedVersion,
		CompiledHash:   newHash,
		ResolvedAt:     cloneTime(&now),
	}, nil
}

func resolveAcceptRemote(current skillSetLocalState, codexRoot, codexHome string) error {
	_, err := doResolveAcceptRemote(current, codexRoot, codexHome)
	if err != nil {
		return err
	}
	reloaded, _, _ := loadSkillSetState()
	resolvedRoot, _ := codexHomePath(codexHome)
	return prettyPrint(skillSetResolveResp{
		Action:         "accept-remote",
		Status:         "resolved",
		BundleName:     reloaded.BundleName,
		BundlePath:     skillSetLiveBundlePath(resolvedRoot, reloaded.BundleName),
		AppliedVersion: reloaded.AppliedVersion,
		CompiledHash:   reloaded.AppliedHash,
	})
}

func doResolveAcceptRemote(current skillSetLocalState, codexRoot, codexHome string) (skillSetResolveResp, error) {
	backupPath, err := skillSetBackupVersionPath(current.AppliedVersion)
	if err != nil {
		return skillSetResolveResp{}, err
	}
	livePath := skillSetLiveBundlePath(codexRoot, current.BundleName)

	if !fileExists(backupPath) {
		return doResolveAcceptRemoteFromServer(current, codexRoot, codexHome)
	}

	tempPath, err := stageCopiedSkillSetBundle(backupPath, filepath.Dir(livePath), current.BundleName)
	if err != nil {
		return skillSetResolveResp{}, err
	}
	defer os.RemoveAll(tempPath)

	if fileExists(livePath) {
		if err := os.RemoveAll(livePath); err != nil {
			return skillSetResolveResp{}, err
		}
	}
	if err := os.Rename(tempPath, livePath); err != nil {
		return skillSetResolveResp{}, err
	}

	restoredHash, err := hashManagedSkillBundle(livePath)
	if err != nil {
		return skillSetResolveResp{}, err
	}
	now := time.Now().UTC()
	current.AppliedHash = restoredHash
	current.LastSyncAt = cloneTime(&now)
	current.LastSyncStatus = "resolved_accept_remote"
	current.LastError = ""
	if err := saveSkillSetState(current); err != nil {
		return skillSetResolveResp{}, err
	}
	if err := reportSkillSetClientStateIfPossible(current); err != nil {
		return skillSetResolveResp{}, err
	}
	return skillSetResolveResp{
		Action:         "accept-remote",
		Status:         "resolved",
		BundleName:     current.BundleName,
		BundlePath:     livePath,
		AppliedVersion: current.AppliedVersion,
		CompiledHash:   restoredHash,
		ResolvedAt:     cloneTime(&now),
	}, nil
}

func doResolveAcceptRemoteFromServer(current skillSetLocalState, codexRoot, codexHome string) (skillSetResolveResp, error) {
	st, err := loadWorkspaceState()
	if err != nil {
		return skillSetResolveResp{}, fmt.Errorf("cannot fetch remote bundle: %w", err)
	}
	client := newStateAPIClient(&st)
	resp, syncErr := syncLatestSkillSet(&st, client, codexHome)
	if syncErr != nil {
		return skillSetResolveResp{}, syncErr
	}
	if resp.Status == "conflict" {
		current.AppliedHash = ""
		if err := saveSkillSetState(current); err != nil {
			return skillSetResolveResp{}, err
		}
		resp, syncErr = syncLatestSkillSet(&st, client, codexHome)
		if syncErr != nil {
			return skillSetResolveResp{}, syncErr
		}
	}
	now := time.Now().UTC()
	return skillSetResolveResp{
		Action:         "accept-remote",
		Status:         "resolved",
		BundleName:     resp.BundleName,
		BundlePath:     resp.BundlePath,
		AppliedVersion: resp.AppliedVersion,
		CompiledHash:   resp.CompiledHash,
		ResolvedAt:     cloneTime(&now),
	}, nil
}

func resolveBackupAndSync(current skillSetLocalState, livePath, codexRoot, codexHome string) error {
	result, err := doResolveBackupAndSync(current, livePath, codexRoot, codexHome)
	if err != nil {
		return err
	}
	return prettyPrint(result)
}

func doResolveBackupAndSync(current skillSetLocalState, livePath, codexRoot, codexHome string) (skillSetResolveResp, error) {
	backupLabel := strings.TrimSpace(current.AppliedVersion) + "-local-" + time.Now().UTC().Format("20060102T150405")
	backupDest, err := skillSetBackupVersionPath(backupLabel)
	if err != nil {
		return skillSetResolveResp{}, err
	}
	if fileExists(livePath) {
		if err := copyDir(livePath, backupDest); err != nil {
			return skillSetResolveResp{}, err
		}
	}

	current.AppliedHash = ""
	if err := saveSkillSetState(current); err != nil {
		return skillSetResolveResp{}, err
	}

	st, loadErr := loadWorkspaceState()
	if loadErr != nil {
		return skillSetResolveResp{}, fmt.Errorf("cannot fetch remote bundle: %w", loadErr)
	}
	client := newStateAPIClient(&st)
	resp, syncErr := syncLatestSkillSet(&st, client, codexHome)
	if syncErr != nil {
		return skillSetResolveResp{}, syncErr
	}

	now := time.Now().UTC()
	return skillSetResolveResp{
		Action:         "backup-and-sync",
		Status:         "resolved",
		BundleName:     resp.BundleName,
		BundlePath:     resp.BundlePath,
		AppliedVersion: resp.AppliedVersion,
		CompiledHash:   resp.CompiledHash,
		BackupPath:     backupDest,
		ResolvedAt:     cloneTime(&now),
	}, nil
}

// diffManagedSkillBundle compares the live bundle on disk against its last-applied
// backup to produce a human-readable summary of local modifications.
func diffManagedSkillBundle(codexHome string) (skillSetConflictDiff, error) {
	current, _, err := loadSkillSetState()
	if err != nil {
		return skillSetConflictDiff{}, err
	}
	codexRoot, err := codexHomePath(codexHome)
	if err != nil {
		return skillSetConflictDiff{}, err
	}
	livePath := skillSetLiveBundlePath(codexRoot, current.BundleName)

	result := skillSetConflictDiff{
		BundlePath:     livePath,
		AppliedVersion: current.AppliedVersion,
	}

	if err := assertManagedSkillBundleUntouched(livePath, current); err == nil {
		result.HasConflict = false
		return result, nil
	}
	result.HasConflict = true

	// Try to load the backup of the currently applied version.
	backupPath, err := skillSetBackupVersionPath(current.AppliedVersion)
	if err != nil || !fileExists(backupPath) {
		// No backup available — just list current files as unknown modifications.
		result.ModifiedFiles = []skillSetFileDiffSummary{{Path: "(backup unavailable — cannot compute diff)"}}
		return result, nil
	}

	liveFiles, err := readBundleFiles(livePath)
	if err != nil {
		return skillSetConflictDiff{}, err
	}
	backupFiles, err := readBundleFiles(backupPath)
	if err != nil {
		return skillSetConflictDiff{}, err
	}

	// Find modified and added files.
	for path, liveContent := range liveFiles {
		backupContent, exists := backupFiles[path]
		if !exists {
			result.AddedFiles = append(result.AddedFiles, path)
			continue
		}
		if liveContent != backupContent {
			added, removed := countLineDiffs(backupContent, liveContent)
			result.ModifiedFiles = append(result.ModifiedFiles, skillSetFileDiffSummary{
				Path:         path,
				AddedLines:   added,
				RemovedLines: removed,
			})
		}
	}
	// Find removed files.
	for path := range backupFiles {
		if _, exists := liveFiles[path]; !exists {
			result.RemovedFiles = append(result.RemovedFiles, path)
		}
	}

	sort.Strings(result.AddedFiles)
	sort.Strings(result.RemovedFiles)
	sort.Slice(result.ModifiedFiles, func(i, j int) bool {
		return result.ModifiedFiles[i].Path < result.ModifiedFiles[j].Path
	})

	return result, nil
}

// readBundleFiles reads all non-manifest files from a bundle directory into a map.
func readBundleFiles(root string) (map[string]string, error) {
	files := make(map[string]string)
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
		files[rel] = string(content)
		return nil
	})
	return files, err
}

// countLineDiffs counts the number of added and removed lines between two texts.
func countLineDiffs(old, new string) (added, removed int) {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	oldSet := make(map[string]int, len(oldLines))
	for _, line := range oldLines {
		oldSet[line]++
	}
	newSet := make(map[string]int, len(newLines))
	for _, line := range newLines {
		newSet[line]++
	}

	for line, count := range newSet {
		if oldCount, ok := oldSet[line]; ok {
			if count > oldCount {
				added += count - oldCount
			}
		} else {
			added += count
		}
	}
	for line, count := range oldSet {
		if newCount, ok := newSet[line]; ok {
			if count > newCount {
				removed += count - newCount
			}
		} else {
			removed += count
		}
	}
	return added, removed
}

// resolveDirectiveFromBundle extracts a pending resolve directive from the
// server-supplied bundle response. The directive is set by the web dashboard.
func resolveDirectiveFromBundle(bundle *response.SkillSetBundleResp) string {
	if bundle == nil || bundle.ClientState == nil {
		return ""
	}
	return strings.TrimSpace(bundle.ClientState.ResolveDirective)
}

// executeResolveDirective runs a resolve action triggered by a dashboard directive
// during an automatic sync. It uses the do* functions to avoid printing to stdout.
func executeResolveDirective(directive string, current skillSetLocalState, livePath, codexRoot, codexHome string, st *state, client *apiClient) (skillSetSyncResp, error) {
	var resolveResult skillSetResolveResp
	var resolveErr error

	switch directive {
	case "keep-local":
		resolveResult, resolveErr = doResolveKeepLocal(current, livePath)
	case "accept-remote":
		resolveResult, resolveErr = doResolveAcceptRemote(current, codexRoot, codexHome)
	case "backup-and-sync":
		resolveResult, resolveErr = doResolveBackupAndSync(current, livePath, codexRoot, codexHome)
	default:
		return skillSetSyncResp{}, fmt.Errorf("unknown resolve directive %q from server", directive)
	}
	if resolveErr != nil {
		return skillSetSyncResp{}, resolveErr
	}

	// Reload state after resolve.
	resolved, _, err := loadSkillSetState()
	if err != nil {
		return skillSetSyncResp{}, err
	}

	now := time.Now().UTC()
	resp := skillSetSyncResp{
		Status:         "resolved_" + directive,
		Mode:           resolved.Mode,
		BundleName:     resolved.BundleName,
		BundlePath:     resolveResult.BundlePath,
		StatePath:      mustSkillSetStatePath(),
		AppliedVersion: resolved.AppliedVersion,
		CompiledHash:   resolved.AppliedHash,
		LastSyncedAt:   cloneTime(&now),
		PausedAt:       cloneTime(resolved.PausedAt),
		BackupPath:     resolveResult.BackupPath,
	}

	// Report the resolved state to the server so the directive gets cleared.
	if reportErr := reportSkillSetClientState(st, client, resolved); reportErr != nil {
		return skillSetSyncResp{}, reportErr
	}
	return resp, nil
}

func syncLatestSkillSet(st *state, client *apiClient, codexHome string) (skillSetSyncResp, error) {
	current, _, err := loadSkillSetState()
	if err != nil {
		return skillSetSyncResp{}, err
	}
	codexRoot, err := codexHomePath(codexHome)
	if err != nil {
		return skillSetSyncResp{}, err
	}

	livePath := skillSetLiveBundlePath(codexRoot, current.BundleName)
	resp := skillSetSyncResp{
		Mode:       current.Mode,
		BundleName: current.BundleName,
		BundlePath: livePath,
		StatePath:  mustSkillSetStatePath(),
		PausedAt:   cloneTime(current.PausedAt),
	}

	if current.Mode == skillSetModeFrozen {
		resp.Status = "paused"
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		if err := reportSkillSetClientState(st, client, current); err != nil {
			return skillSetSyncResp{}, err
		}
		return resp, nil
	}

	bundle, fetchErr := fetchLatestSkillSetBundle(client, st.workspaceID())
	if fetchErr != nil {
		resp.Status = "failed"
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		resp.Error = fetchErr.Error()
		if saveErr := saveSkillSetSyncOutcome(&current, resp.Status, resp.Error); saveErr != nil {
			return skillSetSyncResp{}, saveErr
		}
		if reportErr := reportSkillSetClientState(st, client, current); reportErr != nil {
			return skillSetSyncResp{}, reportErr
		}
		return resp, nil
	}

	resp.BundleName = firstNonEmpty(strings.TrimSpace(bundle.BundleName), current.BundleName, managedSkillBundleName)
	resp.BundlePath = skillSetLiveBundlePath(codexRoot, resp.BundleName)
	current.BundleName = resp.BundleName

	switch strings.TrimSpace(bundle.Status) {
	case "", "ready":
	case "no_reports", "no_candidate":
		resp.Status = bundle.Status
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		resp.Summary = cloneStringSlice(bundle.Summary)
		resp.BasedOnReportIDs = cloneStringSlice(bundle.BasedOnReportIDs)
		if saveErr := saveSkillSetSyncOutcome(&current, resp.Status, ""); saveErr != nil {
			return skillSetSyncResp{}, saveErr
		}
		if reportErr := reportSkillSetClientState(st, client, current); reportErr != nil {
			return skillSetSyncResp{}, reportErr
		}
		return resp, nil
	case "unsupported":
		resp.Status = "unsupported"
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		if saveErr := saveSkillSetSyncOutcome(&current, resp.Status, ""); saveErr != nil {
			return skillSetSyncResp{}, saveErr
		}
		if reportErr := reportSkillSetClientState(st, client, current); reportErr != nil {
			return skillSetSyncResp{}, reportErr
		}
		return resp, nil
	default:
		resp.Status = bundle.Status
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		resp.Error = "server returned an unsupported skill set status"
		if saveErr := saveSkillSetSyncOutcome(&current, resp.Status, resp.Error); saveErr != nil {
			return skillSetSyncResp{}, saveErr
		}
		if reportErr := reportSkillSetClientState(st, client, current); reportErr != nil {
			return skillSetSyncResp{}, reportErr
		}
		return resp, nil
	}

	if strings.TrimSpace(bundle.Version) == "" {
		resp.Status = "failed"
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		resp.Error = "server returned an empty managed skill version"
		if saveErr := saveSkillSetSyncOutcome(&current, resp.Status, resp.Error); saveErr != nil {
			return skillSetSyncResp{}, saveErr
		}
		if reportErr := reportSkillSetClientState(st, client, current); reportErr != nil {
			return skillSetSyncResp{}, reportErr
		}
		return resp, nil
	}

	if err := assertManagedSkillBundleUntouched(resp.BundlePath, current); err != nil {
		// Check if the server has a pending resolve directive from the dashboard.
		directive := resolveDirectiveFromBundle(bundle)
		if directive != "" {
			fmt.Fprintf(os.Stderr, "Resolve directive %q received from dashboard, executing automatically.\n", directive)
			return executeResolveDirective(directive, current, resp.BundlePath, codexRoot, codexHome, st, client)
		}
		resp.Status = "conflict"
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		resp.Error = err.Error()
		if saveErr := saveSkillSetSyncOutcome(&current, resp.Status, resp.Error); saveErr != nil {
			return skillSetSyncResp{}, saveErr
		}
		if reportErr := reportSkillSetClientState(st, client, current); reportErr != nil {
			return skillSetSyncResp{}, reportErr
		}
		return resp, nil
	}

	if current.AppliedVersion == strings.TrimSpace(bundle.Version) && current.AppliedHash == strings.TrimSpace(bundle.CompiledHash) && fileExists(resp.BundlePath) {
		now := time.Now().UTC()
		current.LastSyncAt = cloneTime(&now)
		current.LastSyncStatus = "unchanged"
		current.LastError = ""
		if err := saveSkillSetState(current); err != nil {
			return skillSetSyncResp{}, err
		}
		if err := reportSkillSetClientState(st, client, current); err != nil {
			return skillSetSyncResp{}, err
		}
		resp.Status = "unchanged"
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		resp.LastSyncedAt = cloneTime(&now)
		resp.Summary = cloneStringSlice(bundle.Summary)
		resp.BasedOnReportIDs = cloneStringSlice(bundle.BasedOnReportIDs)
		return resp, nil
	}

	deployResult, err := deployManagedSkillSetBundle(bundle, codexRoot, &current)
	if err != nil {
		resp.Status = "failed"
		resp.AppliedVersion = current.AppliedVersion
		resp.CompiledHash = current.AppliedHash
		resp.Error = err.Error()
		if saveErr := saveSkillSetSyncOutcome(&current, resp.Status, resp.Error); saveErr != nil {
			return skillSetSyncResp{}, saveErr
		}
		return resp, nil
	}
	resp.Status = "synced"
	resp.BundleName = current.BundleName
	resp.BundlePath = deployResult.BundlePath
	resp.AppliedVersion = current.AppliedVersion
	resp.PreviousVersion = deployResult.PreviousVersion
	resp.CompiledHash = current.AppliedHash
	resp.LastSyncedAt = cloneTime(current.LastSyncAt)
	resp.Summary = cloneStringSlice(bundle.Summary)
	resp.BasedOnReportIDs = cloneStringSlice(bundle.BasedOnReportIDs)
	resp.BackupPath = deployResult.BackupPath
	if err := reportSkillSetClientState(st, client, current); err != nil {
		return skillSetSyncResp{}, err
	}
	return resp, nil
}

func fetchLatestSkillSetBundle(client *apiClient, workspaceID string) (*response.SkillSetBundleResp, error) {
	path := "/api/v1/skill-sets/latest?project_id=" + url.QueryEscape(strings.TrimSpace(workspaceID))
	var bundle response.SkillSetBundleResp
	if err := client.doJSON(http.MethodGet, path, nil, &bundle); err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed) {
			return &response.SkillSetBundleResp{
				SchemaVersion: skillSetBundleSchemaVersion,
				ProjectID:     strings.TrimSpace(workspaceID),
				Status:        "unsupported",
				BundleName:    managedSkillBundleName,
			}, nil
		}
		return nil, err
	}
	if strings.TrimSpace(bundle.SchemaVersion) == "" {
		bundle.SchemaVersion = skillSetBundleSchemaVersion
	}
	if strings.TrimSpace(bundle.BundleName) == "" {
		bundle.BundleName = managedSkillBundleName
	}
	return &bundle, nil
}

type managedSkillSetDeployResult struct {
	BundlePath      string
	PreviousVersion string
	BackupPath      string
}

func deployManagedSkillSetBundle(bundle *response.SkillSetBundleResp, codexRoot string, current *skillSetLocalState) (managedSkillSetDeployResult, error) {
	if bundle == nil {
		return managedSkillSetDeployResult{}, errors.New("managed skill bundle payload is required")
	}
	if len(bundle.Files) == 0 {
		return managedSkillSetDeployResult{}, errors.New("managed skill bundle did not include any files")
	}

	current.BundleName = firstNonEmpty(strings.TrimSpace(bundle.BundleName), current.BundleName, managedSkillBundleName)
	livePath := skillSetLiveBundlePath(codexRoot, current.BundleName)
	if err := os.MkdirAll(filepath.Dir(livePath), 0o755); err != nil {
		return managedSkillSetDeployResult{}, err
	}

	previousVersion := strings.TrimSpace(current.AppliedVersion)
	if previousVersion == "" {
		if manifest, err := readLocalSkillSetManifest(livePath); err == nil {
			previousVersion = strings.TrimSpace(manifest.Version)
			if current.AppliedHash == "" {
				current.AppliedHash = strings.TrimSpace(manifest.CompiledHash)
			}
		}
	}

	backupPath := ""
	if fileExists(livePath) && previousVersion != "" && previousVersion != strings.TrimSpace(bundle.Version) {
		var err error
		backupPath, err = backupLiveSkillSetBundle(livePath, previousVersion)
		if err != nil {
			return managedSkillSetDeployResult{}, err
		}
	}

	tempPath, err := stageManagedSkillSetBundle(bundle, filepath.Dir(livePath))
	if err != nil {
		return managedSkillSetDeployResult{}, err
	}
	defer os.RemoveAll(tempPath)

	if fileExists(livePath) {
		if err := os.RemoveAll(livePath); err != nil {
			return managedSkillSetDeployResult{}, err
		}
	}
	if err := os.Rename(tempPath, livePath); err != nil {
		return managedSkillSetDeployResult{}, err
	}

	now := time.Now().UTC()
	current.AppliedVersion = strings.TrimSpace(bundle.Version)
	current.AppliedHash = strings.TrimSpace(bundle.CompiledHash)
	current.LastSyncAt = cloneTime(&now)
	current.LastSyncStatus = "synced"
	current.LastError = ""
	if err := saveSkillSetState(*current); err != nil {
		return managedSkillSetDeployResult{}, err
	}
	return managedSkillSetDeployResult{
		BundlePath:      livePath,
		PreviousVersion: previousVersion,
		BackupPath:      backupPath,
	}, nil
}

func stageManagedSkillSetBundle(bundle *response.SkillSetBundleResp, parentDir string) (string, error) {
	tempPath, err := os.MkdirTemp(parentDir, "."+sanitizeID(strings.TrimSpace(bundle.BundleName))+".tmp-")
	if err != nil {
		return "", err
	}
	for _, file := range bundle.Files {
		relativePath, err := cleanManagedSkillPath(file.Path)
		if err != nil {
			return "", err
		}
		targetPath := filepath.Join(tempPath, relativePath)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(targetPath, []byte(file.Content), 0o644); err != nil {
			return "", err
		}
	}
	return tempPath, nil
}

func stageCopiedSkillSetBundle(sourcePath, parentDir, bundleName string) (string, error) {
	tempPath, err := os.MkdirTemp(parentDir, "."+sanitizeID(strings.TrimSpace(bundleName))+".rollback-")
	if err != nil {
		return "", err
	}
	if err := copyDir(sourcePath, tempPath); err != nil {
		return "", err
	}
	return tempPath, nil
}

func currentSkillSetStatus(codexHome string) (skillSetStatusResp, error) {
	current, _, err := loadSkillSetState()
	if err != nil {
		return skillSetStatusResp{}, err
	}
	codexRoot, err := codexHomePath(codexHome)
	if err != nil {
		return skillSetStatusResp{}, err
	}

	backups, err := listSkillSetBackups()
	if err != nil {
		return skillSetStatusResp{}, err
	}

	status := skillSetStatusResp{
		SchemaVersion:    skillSetLocalStateSchemaVersion,
		Mode:             current.Mode,
		BundleName:       current.BundleName,
		BundlePath:       skillSetLiveBundlePath(codexRoot, current.BundleName),
		StatePath:        mustSkillSetStatePath(),
		AppliedVersion:   current.AppliedVersion,
		AppliedHash:      current.AppliedHash,
		LastSyncAt:       cloneTime(current.LastSyncAt),
		LastSyncStatus:   current.LastSyncStatus,
		LastError:        current.LastError,
		PausedAt:         cloneTime(current.PausedAt),
		AvailableBackups: backups,
	}

	if status.AppliedVersion == "" && fileExists(status.BundlePath) {
		if manifest, err := readLocalSkillSetManifest(status.BundlePath); err == nil {
			status.AppliedVersion = manifest.Version
			if status.AppliedHash == "" {
				status.AppliedHash = manifest.CompiledHash
			}
		}
	}

	// When the bundle has been modified locally, include a conflict diff summary.
	if assertManagedSkillBundleUntouched(status.BundlePath, current) != nil {
		diff, diffErr := diffManagedSkillBundle(codexHome)
		if diffErr == nil && diff.HasConflict {
			status.ConflictDiff = &diff
			status.ResolveHint = "run \"crux skills resolve\" to view options and resolve the conflict"
		}
	}

	return status, nil
}

func assertManagedSkillBundleUntouched(livePath string, current skillSetLocalState) error {
	if !fileExists(livePath) || strings.TrimSpace(current.AppliedHash) == "" {
		return nil
	}
	liveHash, err := hashManagedSkillBundle(livePath)
	if err != nil {
		return err
	}
	if liveHash != strings.TrimSpace(current.AppliedHash) {
		return fmt.Errorf("managed skill bundle at %s was modified locally; run \"crux skills resolve\" to view changes and choose how to proceed", livePath)
	}
	return nil
}

func hashManagedSkillBundle(root string) (string, error) {
	type hashedFile struct {
		Path    string
		Content []byte
	}

	files := make([]hashedFile, 0, 8)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		if strings.EqualFold(relativePath, "00-manifest.json") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, hashedFile{Path: relativePath, Content: content})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	hasher := sha256.New()
	for _, file := range files {
		_, _ = hasher.Write([]byte(strings.TrimSpace(file.Path)))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(file.Content)
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func cleanManagedSkillPath(path string) (string, error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return "", errors.New("managed skill bundle included an empty path")
	}
	if strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("managed skill bundle path %q must be relative", path)
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return "", fmt.Errorf("managed skill bundle path %q is invalid", path)
	}
	if strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return "", fmt.Errorf("managed skill bundle path %q escapes the target directory", path)
	}
	return cleaned, nil
}

func saveSkillSetSyncOutcome(current *skillSetLocalState, status, errText string) error {
	now := time.Now().UTC()
	current.LastSyncAt = cloneTime(&now)
	current.LastSyncStatus = strings.TrimSpace(status)
	current.LastError = strings.TrimSpace(errText)
	return saveSkillSetState(*current)
}

func reportSkillSetClientStateIfPossible(current skillSetLocalState) error {
	st, err := loadWorkspaceState()
	if err != nil {
		if errors.Is(err, errStateNotFound) || errors.Is(err, errWorkspaceNotConnected) {
			return nil
		}
		return err
	}
	return reportSkillSetClientState(&st, newStateAPIClient(&st), current)
}

func reportSkillSetClientState(st *state, client *apiClient, current skillSetLocalState) error {
	if st == nil || client == nil || strings.TrimSpace(st.workspaceID()) == "" {
		return nil
	}
	req := request.SkillSetClientStateUpsertReq{
		ProjectID:      st.workspaceID(),
		BundleName:     firstNonEmpty(strings.TrimSpace(current.BundleName), managedSkillBundleName),
		Mode:           strings.TrimSpace(current.Mode),
		SyncStatus:     strings.TrimSpace(current.LastSyncStatus),
		AppliedVersion: strings.TrimSpace(current.AppliedVersion),
		AppliedHash:    strings.TrimSpace(current.AppliedHash),
		LastSyncedAt:   cloneTime(current.LastSyncAt),
		PausedAt:       cloneTime(current.PausedAt),
		LastError:      strings.TrimSpace(current.LastError),
	}

	var stored response.SkillSetClientStateResp
	if err := client.doJSON(http.MethodPost, "/api/v1/skill-sets/client-state", req, &stored); err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed) {
			return nil
		}
		return err
	}
	return nil
}

func loadSkillSetState() (skillSetLocalState, bool, error) {
	path, err := skillSetStatePath()
	if err != nil {
		return skillSetLocalState{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return defaultSkillSetLocalState(), false, nil
		}
		return skillSetLocalState{}, false, err
	}

	var current skillSetLocalState
	if err := json.Unmarshal(data, &current); err != nil {
		return skillSetLocalState{}, false, err
	}
	return normalizeSkillSetLocalState(current), true, nil
}

func saveSkillSetState(current skillSetLocalState) error {
	path, err := skillSetStatePath()
	if err != nil {
		return err
	}
	current = normalizeSkillSetLocalState(current)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func defaultSkillSetLocalState() skillSetLocalState {
	return skillSetLocalState{
		SchemaVersion: skillSetLocalStateSchemaVersion,
		Mode:          skillSetModeAutopilot,
		BundleName:    managedSkillBundleName,
	}
}

func normalizeSkillSetLocalState(current skillSetLocalState) skillSetLocalState {
	current.SchemaVersion = skillSetLocalStateSchemaVersion
	current.Mode = strings.TrimSpace(current.Mode)
	if current.Mode == "" {
		current.Mode = skillSetModeAutopilot
	}
	current.BundleName = firstNonEmpty(strings.TrimSpace(current.BundleName), managedSkillBundleName)
	current.AppliedVersion = strings.TrimSpace(current.AppliedVersion)
	current.AppliedHash = strings.TrimSpace(current.AppliedHash)
	current.LastSyncStatus = strings.TrimSpace(current.LastSyncStatus)
	current.LastError = strings.TrimSpace(current.LastError)
	current.LastSyncAt = cloneTime(current.LastSyncAt)
	current.PausedAt = cloneTime(current.PausedAt)
	if current.Mode != skillSetModeFrozen {
		current.PausedAt = nil
	}
	return current
}

func skillSetStatePath() (string, error) {
	homeDir, err := cruxHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, "skillset-state.json"), nil
}

func mustSkillSetStatePath() string {
	path, err := skillSetStatePath()
	if err != nil {
		return ""
	}
	return path
}

func skillSetLiveBundlePath(codexRoot, bundleName string) string {
	return filepath.Join(strings.TrimSpace(codexRoot), "skills", firstNonEmpty(strings.TrimSpace(bundleName), managedSkillBundleName))
}

func skillSetBackupRoot() (string, error) {
	homeDir, err := cruxHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, "skillsets"), nil
}

func skillSetBackupVersionPath(version string) (string, error) {
	root, err := skillSetBackupRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, sanitizeID(strings.TrimSpace(version))), nil
}

func backupLiveSkillSetBundle(livePath, version string) (string, error) {
	targetPath, err := skillSetBackupVersionPath(version)
	if err != nil {
		return "", err
	}
	if fileExists(targetPath) {
		return targetPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", err
	}
	if err := copyDir(livePath, targetPath); err != nil {
		return "", err
	}
	return targetPath, nil
}

func listSkillSetBackups() ([]string, error) {
	root, err := skillSetBackupRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		versions = append(versions, entry.Name())
	}
	sort.Strings(versions)
	return versions, nil
}

func readLocalSkillSetManifest(bundlePath string) (*localSkillSetManifest, error) {
	data, err := os.ReadFile(filepath.Join(bundlePath, "00-manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest localSkillSetManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func copyDir(sourcePath, targetPath string) error {
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(sourcePath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		destinationPath := filepath.Join(targetPath, relativePath)
		if d.IsDir() {
			if relativePath == "." {
				return nil
			}
			return os.MkdirAll(destinationPath, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destinationPath, content, 0o644)
	})
}

// cruxSkillSetAgentsSection is the marker-delimited block injected into the
// global AGENTS.md so that every query consults crux-personal-skillset first.
const cruxSkillSetSectionStart = "<!-- crux-personal-skillset:start -->"
const cruxSkillSetSectionEnd = "<!-- crux-personal-skillset:end -->"

func cruxSkillSetAgentsSection() string {
	return cruxSkillSetSectionStart + `
## Crux Personal Skillset

Before processing any query, you MUST first review and apply the user's personal skillset located at:

` + "`skills/" + managedSkillBundleName + "/SKILL.md`" + `

This skillset contains standing instructions, collaboration preferences, and workflow rules
that the user has accumulated over time. Treat these as the user's policy layer — they take
precedence over generic defaults but yield to explicit per-query instructions.

**Workflow:**
1. Load ` + "`skills/" + managedSkillBundleName + "/SKILL.md`" + ` at the start of every conversation.
2. Apply only the category documents relevant to the current request.
3. Preserve these rules across follow-up turns unless a newer rule overrides them.
` + cruxSkillSetSectionEnd + "\n"
}

// ensureAgentsMDSkillSetSection ensures the global AGENTS.md in the codex home
// directory contains the crux-personal-skillset instruction section. It is
// idempotent — if the section already exists it returns without modification.
func ensureAgentsMDSkillSetSection(codexRoot string) error {
	agentsPath := filepath.Join(codexRoot, "AGENTS.md")

	existing, err := os.ReadFile(agentsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	content := string(existing)
	if strings.Contains(content, cruxSkillSetSectionStart) {
		return nil // already present
	}

	section := cruxSkillSetAgentsSection()
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if len(content) > 0 {
		content += "\n"
	}
	content += section

	if err := os.MkdirAll(filepath.Dir(agentsPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(agentsPath, []byte(content), 0o644)
}

func skillRollbackHashChange(previousHash, restoredHash string) string {
	previousHash = strings.TrimSpace(previousHash)
	restoredHash = strings.TrimSpace(restoredHash)
	if previousHash == "" || restoredHash == "" || previousHash == restoredHash {
		return ""
	}
	return fmt.Sprintf("managed skill hash changed from %s to %s", previousHash, restoredHash)
}
