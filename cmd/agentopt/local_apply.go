package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Royaltyprogram/aiops/dto/response"
)

type applyBackup struct {
	ApplyID        string            `json:"apply_id"`
	WorkspaceID    string            `json:"workspace_id,omitempty"`
	CodexThreadID  string            `json:"codex_thread_id,omitempty"`
	CodexSummary   string            `json:"codex_summary,omitempty"`
	Files          []applyFileBackup `json:"files"`
	FilePath       string            `json:"file_path"`
	FileKind       string            `json:"file_kind"`
	OriginalExists bool              `json:"original_exists"`
	OriginalJSON   map[string]any    `json:"original_json"`
	OriginalText   string            `json:"original_text"`
}

type applyBackupDisk struct {
	ApplyID         string            `json:"apply_id"`
	WorkspaceID     string            `json:"workspace_id,omitempty"`
	LegacyProjectID string            `json:"project_id,omitempty"`
	CodexThreadID   string            `json:"codex_thread_id,omitempty"`
	CodexSummary    string            `json:"codex_summary,omitempty"`
	Files           []applyFileBackup `json:"files"`
	FilePath        string            `json:"file_path"`
	FileKind        string            `json:"file_kind"`
	OriginalExists  bool              `json:"original_exists"`
	OriginalJSON    map[string]any    `json:"original_json"`
	OriginalText    string            `json:"original_text"`
}

type applyFileBackup struct {
	FilePath       string         `json:"file_path"`
	FileKind       string         `json:"file_kind"`
	OriginalExists bool           `json:"original_exists"`
	OriginalJSON   map[string]any `json:"original_json"`
	OriginalText   string         `json:"original_text"`
}

type localApplyResult struct {
	FilePath        string
	FilePaths       []string
	AppliedSettings map[string]any
	AppliedText     string
}

type codexApplyRequest struct {
	Mode                  string           `json:"mode,omitempty"`
	ApplyID               string           `json:"apply_id"`
	ResumeThreadID        string           `json:"resume_thread_id,omitempty"`
	WorkingDirectory      string           `json:"working_directory"`
	AdditionalDirectories []string         `json:"additional_directories"`
	AllowedFiles          []string         `json:"allowed_files"`
	ModelReasoningEffort  string           `json:"model_reasoning_effort,omitempty"`
	SandboxMode           string           `json:"sandbox_mode"`
	ApprovalPolicy        string           `json:"approval_policy"`
	SkipGitRepoCheck      bool             `json:"skip_git_repo_check"`
	NetworkAccessEnabled  bool             `json:"network_access_enabled"`
	Steps                 []codexApplyStep `json:"steps"`
}

type codexApplyStep struct {
	TargetFile      string         `json:"target_file"`
	Operation       string         `json:"operation"`
	Summary         string         `json:"summary"`
	SettingsUpdates map[string]any `json:"settings_updates,omitempty"`
	ContentPreview  string         `json:"content_preview,omitempty"`
}

type codexApplyResponse struct {
	ThreadID         string                 `json:"thread_id"`
	Status           string                 `json:"status"`
	Summary          string                 `json:"summary"`
	FinalResponse    string                 `json:"final_response"`
	ChangedFiles     []string               `json:"changed_files"`
	ExecutedCommands []codexExecutedCommand `json:"executed_commands"`
}

type codexExecutedCommand struct {
	Command string `json:"command"`
	Status  string `json:"status"`
}

type preflightResult struct {
	ApplyID string          `json:"apply_id"`
	Allowed bool            `json:"allowed"`
	Reason  string          `json:"reason"`
	Steps   []preflightStep `json:"steps"`
}

type preflightStep struct {
	TargetFile   string `json:"target_file"`
	Operation    string `json:"operation"`
	PreviewFile  string `json:"preview_file"`
	Guard        string `json:"guard"`
	Reason       string `json:"reason"`
	TargetSource string `json:"target_source"`
	Allowed      bool   `json:"allowed"`
}

var rollbackRestoreExecutor = rollbackAppliedSteps

func executeLocalApply(st state, applyID string, previews []response.PatchPreviewItem, targetOverride, reasoningEffort string) (localApplyResult, error) {
	preflight, err := preflightLocalApply(st, applyID, previews, targetOverride)
	if err != nil {
		return localApplyResult{}, err
	}
	if !preflight.Allowed {
		return localApplyResult{}, fmt.Errorf("local guard rejected apply %s: %s", applyID, preflight.Reason)
	}

	backups, err := createApplyBackups(preflight, previews)
	if err != nil {
		return localApplyResult{}, err
	}

	req, err := newCodexApplyRequest(applyID, preflight, previews, reasoningEffort)
	if err != nil {
		return localApplyResult{}, err
	}
	codexResp, err := runCodexApply(req)
	if err != nil {
		restoreErr := rollbackAppliedSteps(backups)
		if restoreErr != nil {
			return localApplyResult{}, fmt.Errorf("apply failed: %v; rollback failed: %w", err, restoreErr)
		}
		return localApplyResult{}, err
	}
	if err := validateCodexApply(req, preflight, previews, codexResp); err != nil {
		restoreErr := rollbackAppliedSteps(backups)
		if restoreErr != nil {
			return localApplyResult{}, fmt.Errorf("apply validation failed: %v; rollback failed: %w", err, restoreErr)
		}
		return localApplyResult{}, err
	}

	if err := saveApplyBackup(applyBackup{
		ApplyID:       applyID,
		WorkspaceID:   st.workspaceID(),
		CodexThreadID: strings.TrimSpace(codexResp.ThreadID),
		CodexSummary:  strings.TrimSpace(firstNonEmpty(codexResp.Summary, codexResp.FinalResponse)),
		Files:         backups,
	}); err != nil {
		restoreErr := rollbackAppliedSteps(backups)
		if restoreErr != nil {
			return localApplyResult{}, fmt.Errorf("save backup failed: %v; rollback failed: %w", err, restoreErr)
		}
		return localApplyResult{}, err
	}

	return localApplyResult{
		FilePath:        appliedFileSummary(req.AllowedFiles),
		FilePaths:       append([]string(nil), req.AllowedFiles...),
		AppliedSettings: plannedAppliedSettings(previews),
		AppliedText:     plannedAppliedText(previews),
	}, nil
}

func executeLocalRollback(applyID string) (localApplyResult, error) {
	backup, err := loadApplyBackup(applyID)
	if err != nil {
		return localApplyResult{}, err
	}

	files := normalizeApplyBackupFiles(backup)
	if err := rollbackRestoreExecutor(files); err != nil {
		if strings.TrimSpace(backup.CodexThreadID) == "" {
			return localApplyResult{}, err
		}
		fallbackResult, fallbackErr := runCodexRollback(backup, "")
		if fallbackErr != nil {
			return localApplyResult{}, fmt.Errorf("local rollback failed: %v; Codex rollback fallback failed: %w", err, fallbackErr)
		}
		return fallbackResult, nil
	}

	return localApplyResult{
		FilePath:        rollbackAppliedFile(files),
		FilePaths:       backupFilePaths(files),
		AppliedSettings: rollbackAppliedSettings(files),
		AppliedText:     rollbackAppliedText(files),
	}, nil
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

func createApplyBackups(preflight preflightResult, previews []response.PatchPreviewItem) ([]applyFileBackup, error) {
	backups := make([]applyFileBackup, 0, len(previews))
	for index, preview := range previews {
		backup, err := snapshotApplyTarget(preflight.Steps[index].TargetFile, preview)
		if err != nil {
			return nil, err
		}
		backups = append(backups, backup)
	}
	return backups, nil
}

func snapshotApplyTarget(filePath string, preview response.PatchPreviewItem) (applyFileBackup, error) {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return applyFileBackup{}, err
	}
	if isTextApplyOperation(preview.Operation) {
		originalBytes, err := os.ReadFile(filePath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return applyFileBackup{}, err
		}
		return applyFileBackup{
			FilePath:       filePath,
			FileKind:       textFileKind(preview.Operation),
			OriginalExists: err == nil,
			OriginalText:   string(originalBytes),
		}, nil
	}

	config := map[string]any{}
	originalExists, err := readOptionalJSONMap(filePath, &config)
	if err != nil {
		return applyFileBackup{}, err
	}
	return applyFileBackup{
		FilePath:       filePath,
		FileKind:       "json_merge",
		OriginalExists: originalExists,
		OriginalJSON:   cloneAnyMap(config),
	}, nil
}

func newCodexApplyRequest(applyID string, preflight preflightResult, previews []response.PatchPreviewItem, reasoningEffort string) (codexApplyRequest, error) {
	workingDirectory, additionalDirectories, err := chooseApplyWorkspace(preflight.Steps)
	if err != nil {
		return codexApplyRequest{}, err
	}
	allowedFiles := make([]string, 0, len(preflight.Steps))
	steps := make([]codexApplyStep, 0, len(preflight.Steps))
	for index, step := range preflight.Steps {
		preview := previews[index]
		allowedFiles = append(allowedFiles, step.TargetFile)
		steps = append(steps, codexApplyStep{
			TargetFile:      step.TargetFile,
			Operation:       preview.Operation,
			Summary:         preview.Summary,
			SettingsUpdates: cloneAnyMap(preview.SettingsUpdates),
			ContentPreview:  preview.ContentPreview,
		})
	}
	return codexApplyRequest{
		Mode:                  "apply",
		ApplyID:               applyID,
		WorkingDirectory:      workingDirectory,
		AdditionalDirectories: additionalDirectories,
		AllowedFiles:          allowedFiles,
		ModelReasoningEffort:  reasoningEffort,
		SandboxMode:           "workspace-write",
		ApprovalPolicy:        "never",
		SkipGitRepoCheck:      true,
		NetworkAccessEnabled:  false,
		Steps:                 steps,
	}, nil
}

func newCodexRollbackRequest(backup applyBackup, reasoningEffort string) (codexApplyRequest, error) {
	files := normalizeApplyBackupFiles(backup)
	if len(files) == 0 {
		return codexApplyRequest{}, errors.New("rollback backup has no tracked files")
	}

	steps := make([]preflightStep, 0, len(files))
	allowedFiles := make([]string, 0, len(files))
	codexSteps := make([]codexApplyStep, 0, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.FilePath) == "" {
			return codexApplyRequest{}, errors.New("rollback backup has an empty file path")
		}
		steps = append(steps, preflightStep{TargetFile: file.FilePath})
		allowedFiles = append(allowedFiles, file.FilePath)

		step, err := buildCodexRollbackStep(file)
		if err != nil {
			return codexApplyRequest{}, err
		}
		codexSteps = append(codexSteps, step)
	}

	workingDirectory, additionalDirectories, err := chooseApplyWorkspace(steps)
	if err != nil {
		return codexApplyRequest{}, err
	}

	return codexApplyRequest{
		Mode:                  "rollback",
		ApplyID:               backup.ApplyID,
		ResumeThreadID:        strings.TrimSpace(backup.CodexThreadID),
		WorkingDirectory:      workingDirectory,
		AdditionalDirectories: additionalDirectories,
		AllowedFiles:          allowedFiles,
		ModelReasoningEffort:  reasoningEffort,
		SandboxMode:           "workspace-write",
		ApprovalPolicy:        "never",
		SkipGitRepoCheck:      true,
		NetworkAccessEnabled:  false,
		Steps:                 codexSteps,
	}, nil
}

func buildCodexRollbackStep(file applyFileBackup) (codexApplyStep, error) {
	if !file.OriginalExists {
		return codexApplyStep{
			TargetFile: file.FilePath,
			Operation:  "delete_file",
			Summary:    "Remove the file created by the previous apply.",
		}, nil
	}

	content, err := renderBackupOriginalContent(file)
	if err != nil {
		return codexApplyStep{}, err
	}
	return codexApplyStep{
		TargetFile:     file.FilePath,
		Operation:      "text_replace",
		Summary:        "Restore the file to its original contents.",
		ContentPreview: content,
	}, nil
}

func renderBackupOriginalContent(file applyFileBackup) (string, error) {
	switch file.FileKind {
	case "json_merge":
		data, err := json.MarshalIndent(file.OriginalJSON, "", "  ")
		if err != nil {
			return "", err
		}
		return string(append(data, '\n')), nil
	case "text_append", "text_replace":
		return file.OriginalText, nil
	default:
		if len(file.OriginalJSON) > 0 {
			data, err := json.MarshalIndent(file.OriginalJSON, "", "  ")
			if err != nil {
				return "", err
			}
			return string(append(data, '\n')), nil
		}
		return file.OriginalText, nil
	}
}

func chooseApplyWorkspace(steps []preflightStep) (string, []string, error) {
	if len(steps) == 0 {
		return "", nil, errors.New("no apply steps available")
	}

	dirs := make([]string, 0, len(steps))
	for _, step := range steps {
		dirs = append(dirs, filepath.Dir(step.TargetFile))
	}

	if cwd, err := os.Getwd(); err == nil && allWithinRoot(cwd, steps) {
		return cwd, nil, nil
	}

	root := dirs[0]
	for _, dir := range dirs[1:] {
		root = sharedPathPrefix(root, dir)
	}
	if root != "" {
		for _, dir := range dirs {
			if filepath.Clean(dir) == filepath.Clean(root) {
				return root, collectAdditionalDirectories(root, steps), nil
			}
		}
	}

	workingDirectory := dirs[0]
	return workingDirectory, collectAdditionalDirectories(workingDirectory, steps), nil
}

func sharedPathPrefix(left, right string) string {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	for !isWithinRoot(left, right) {
		parent := filepath.Dir(left)
		if parent == left {
			return left
		}
		left = parent
	}
	return left
}

func collectAdditionalDirectories(workingDirectory string, steps []preflightStep) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, step := range steps {
		dir := filepath.Dir(step.TargetFile)
		if isWithinRoot(workingDirectory, dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func allWithinRoot(root string, steps []preflightStep) bool {
	for _, step := range steps {
		if !isWithinRoot(root, step.TargetFile) {
			return false
		}
	}
	return true
}

func runCodexApply(req codexApplyRequest) (codexApplyResponse, error) {
	requestFile, err := writeCodexApplyRequest(req)
	if err != nil {
		return codexApplyResponse{}, err
	}
	defer os.Remove(requestFile)

	command, args, err := codexRunnerCommand(requestFile)
	if err != nil {
		return codexApplyResponse{}, err
	}

	timeout, err := codexApplyTimeout()
	if err != nil {
		return codexApplyResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return codexApplyResponse{}, fmt.Errorf("codex runner timed out after %s", timeout)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(string(output))
		}
		if detail == "" {
			detail = err.Error()
		}
		return codexApplyResponse{}, fmt.Errorf("codex runner failed: %s", detail)
	}

	var resp codexApplyResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return codexApplyResponse{}, fmt.Errorf("parse codex runner output: %w", err)
	}
	return resp, nil
}

func codexApplyTimeout() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("AGENTOPT_CODEX_TIMEOUT"))
	if raw == "" {
		return 10 * time.Minute, nil
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid AGENTOPT_CODEX_TIMEOUT %q: %w", raw, err)
	}
	if timeout <= 0 {
		return 0, errors.New("AGENTOPT_CODEX_TIMEOUT must be greater than zero")
	}
	return timeout, nil
}

func writeCodexApplyRequest(req codexApplyRequest) (string, error) {
	file, err := os.CreateTemp("", "agentopt-codex-apply-*.json")
	if err != nil {
		return "", err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(req); err != nil {
		return "", err
	}
	return file.Name(), nil
}

func codexRunnerCommand(requestFile string) (string, []string, error) {
	if override := strings.TrimSpace(os.Getenv("AGENTOPT_CODEX_RUNNER")); override != "" {
		return override, []string{requestFile}, nil
	}

	script, err := locateCodexRunnerScript()
	if err != nil {
		return "", nil, err
	}
	return "node", []string{script, requestFile}, nil
}

func locateCodexRunnerScript() (string, error) {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "tools", "codex-runner", "run.mjs"))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "tools", "codex-runner", "run.mjs"),
			filepath.Join(exeDir, "..", "tools", "codex-runner", "run.mjs"),
		)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Clean(candidate), nil
		}
	}
	return "", errors.New("codex runner not found; install tools/codex-runner dependencies first")
}

func validateCodexApply(req codexApplyRequest, preflight preflightResult, previews []response.PatchPreviewItem, resp codexApplyResponse) error {
	status := strings.TrimSpace(resp.Status)
	switch status {
	case "", "applied", "completed", "ok":
	default:
		return fmt.Errorf("codex apply returned status %q: %s", status, firstNonEmpty(resp.Summary, resp.FinalResponse))
	}

	if err := validateChangedFiles(req, resp.ChangedFiles); err != nil {
		return err
	}
	for index, preview := range previews {
		if err := validateAppliedStep(preflight.Steps[index].TargetFile, preview); err != nil {
			return err
		}
	}
	return nil
}

func validateChangedFiles(req codexApplyRequest, changedFiles []string) error {
	if len(changedFiles) == 0 {
		return nil
	}
	allowed := map[string]struct{}{}
	for _, file := range req.AllowedFiles {
		allowed[filepath.Clean(file)] = struct{}{}
	}
	for _, file := range changedFiles {
		resolved := filepath.Clean(file)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Clean(filepath.Join(req.WorkingDirectory, resolved))
		}
		if _, ok := allowed[resolved]; ok {
			continue
		}
		return fmt.Errorf("codex changed unexpected file %s", file)
	}
	return nil
}

func validateAppliedStep(filePath string, preview response.PatchPreviewItem) error {
	if preview.Operation == "delete_file" {
		if _, err := os.Stat(filePath); err == nil {
			return fmt.Errorf("applied file %s still exists after approved delete", filePath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if isTextApplyOperation(preview.Operation) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		content := string(data)
		if preview.Operation == "text_replace" {
			if content != preview.ContentPreview {
				return fmt.Errorf("applied file %s does not match approved content", filePath)
			}
			return nil
		}
		if !strings.Contains(content, preview.ContentPreview) {
			return fmt.Errorf("applied file %s does not contain approved content", filePath)
		}
		return nil
	}

	current := map[string]any{}
	if _, err := readOptionalJSONMap(filePath, &current); err != nil {
		return err
	}
	for key, expected := range preview.SettingsUpdates {
		value, ok := current[key]
		if !ok {
			return fmt.Errorf("applied file %s is missing approved key %s", filePath, key)
		}
		if !sameJSONValue(value, expected) {
			return fmt.Errorf("applied file %s has unexpected value for %s", filePath, key)
		}
	}
	return nil
}

func sameJSONValue(left, right any) bool {
	leftData, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightData, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return bytes.Equal(leftData, rightData)
}

func plannedAppliedSettings(previews []response.PatchPreviewItem) map[string]any {
	out := map[string]any{}
	for _, preview := range previews {
		if isTextApplyOperation(preview.Operation) {
			continue
		}
		mergeMap(out, preview.SettingsUpdates)
	}
	return out
}

func plannedAppliedText(previews []response.PatchPreviewItem) string {
	texts := make([]string, 0, len(previews))
	for _, preview := range previews {
		if !isTextApplyOperation(preview.Operation) {
			continue
		}
		if strings.TrimSpace(preview.ContentPreview) == "" {
			continue
		}
		texts = append(texts, preview.ContentPreview)
	}
	return strings.Join(texts, "\n")
}

func isTextApplyOperation(operation string) bool {
	switch operation {
	case "append_block", "text_append", "text_replace":
		return true
	default:
		return false
	}
}

func textFileKind(operation string) string {
	if operation == "text_replace" {
		return "text_replace"
	}
	return "text_append"
}

func rollbackAppliedSteps(files []applyFileBackup) error {
	for i := len(files) - 1; i >= 0; i-- {
		file := files[i]
		if file.OriginalExists {
			if err := os.MkdirAll(filepath.Dir(file.FilePath), 0o755); err != nil {
				return err
			}
			switch file.FileKind {
			case "text_append", "text_replace":
				if err := os.WriteFile(file.FilePath, []byte(file.OriginalText), 0o644); err != nil {
					return err
				}
			default:
				data, err := json.MarshalIndent(file.OriginalJSON, "", "  ")
				if err != nil {
					return err
				}
				data = append(data, '\n')
				if err := os.WriteFile(file.FilePath, data, 0o644); err != nil {
					return err
				}
			}
			continue
		}
		if err := os.Remove(file.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func runCodexRollback(backup applyBackup, reasoningEffort string) (localApplyResult, error) {
	req, err := newCodexRollbackRequest(backup, reasoningEffort)
	if err != nil {
		return localApplyResult{}, err
	}
	if strings.TrimSpace(req.ResumeThreadID) == "" {
		return localApplyResult{}, errors.New("rollback fallback requires a stored Codex thread id")
	}

	resp, err := runCodexApply(req)
	if err != nil {
		return localApplyResult{}, err
	}

	previews := make([]response.PatchPreviewItem, 0, len(req.Steps))
	for _, step := range req.Steps {
		previews = append(previews, response.PatchPreviewItem{
			FilePath:       step.TargetFile,
			Operation:      step.Operation,
			ContentPreview: step.ContentPreview,
		})
	}
	preflight := preflightResult{
		ApplyID: backup.ApplyID,
		Allowed: true,
		Steps:   make([]preflightStep, 0, len(req.Steps)),
	}
	for _, step := range req.Steps {
		preflight.Steps = append(preflight.Steps, preflightStep{
			TargetFile: step.TargetFile,
			Allowed:    true,
		})
	}
	if err := validateCodexApply(req, preflight, previews, resp); err != nil {
		return localApplyResult{}, err
	}

	files := normalizeApplyBackupFiles(backup)
	return localApplyResult{
		FilePath:        rollbackAppliedFile(files),
		FilePaths:       backupFilePaths(files),
		AppliedSettings: rollbackAppliedSettings(files),
		AppliedText:     rollbackAppliedText(files),
	}, nil
}

func normalizeApplyBackupFiles(backup applyBackup) []applyFileBackup {
	if len(backup.Files) > 0 {
		out := make([]applyFileBackup, 0, len(backup.Files))
		for _, file := range backup.Files {
			out = append(out, applyFileBackup{
				FilePath:       file.FilePath,
				FileKind:       file.FileKind,
				OriginalExists: file.OriginalExists,
				OriginalJSON:   cloneAnyMap(file.OriginalJSON),
				OriginalText:   file.OriginalText,
			})
		}
		return out
	}
	if strings.TrimSpace(backup.FilePath) == "" {
		return nil
	}
	return []applyFileBackup{{
		FilePath:       backup.FilePath,
		FileKind:       backup.FileKind,
		OriginalExists: backup.OriginalExists,
		OriginalJSON:   cloneAnyMap(backup.OriginalJSON),
		OriginalText:   backup.OriginalText,
	}}
}

func rollbackAppliedFile(files []applyFileBackup) string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.FilePath)
	}
	return appliedFileSummary(paths)
}

func backupFilePaths(files []applyFileBackup) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.FilePath)
	}
	return paths
}

func rollbackAppliedSettings(files []applyFileBackup) map[string]any {
	combined := map[string]any{}
	for _, file := range files {
		if file.FileKind == "json_merge" {
			combined[file.FilePath] = cloneAnyMap(file.OriginalJSON)
		}
	}
	return combined
}

func rollbackAppliedText(files []applyFileBackup) string {
	texts := make([]string, 0, len(files))
	for _, file := range files {
		if file.FileKind == "text_append" || file.FileKind == "text_replace" {
			texts = append(texts, file.OriginalText)
		}
	}
	return strings.Join(texts, "\n")
}

func appliedFileSummary(paths []string) string {
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return paths[0]
	default:
		return strings.Join(paths, ",")
	}
}

func applyBackupPath(applyID string) (string, error) {
	root, err := stateFilePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(root), "applies", applyID+".json"), nil
}

func saveApplyBackup(backup applyBackup) error {
	path, err := applyBackupPath(backup.ApplyID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(applyBackupDisk{
		ApplyID:        backup.ApplyID,
		WorkspaceID:    backup.WorkspaceID,
		CodexThreadID:  backup.CodexThreadID,
		CodexSummary:   backup.CodexSummary,
		Files:          backup.Files,
		FilePath:       backup.FilePath,
		FileKind:       backup.FileKind,
		OriginalExists: backup.OriginalExists,
		OriginalJSON:   backup.OriginalJSON,
		OriginalText:   backup.OriginalText,
	}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func loadApplyBackup(applyID string) (applyBackup, error) {
	path, err := applyBackupPath(applyID)
	if err != nil {
		return applyBackup{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return applyBackup{}, fmt.Errorf("backup for apply %s not found", applyID)
		}
		return applyBackup{}, err
	}
	var disk applyBackupDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return applyBackup{}, err
	}
	backup := applyBackup{
		ApplyID:        disk.ApplyID,
		WorkspaceID:    firstNonEmpty(disk.WorkspaceID, disk.LegacyProjectID),
		CodexThreadID:  disk.CodexThreadID,
		CodexSummary:   disk.CodexSummary,
		Files:          disk.Files,
		FilePath:       disk.FilePath,
		FileKind:       disk.FileKind,
		OriginalExists: disk.OriginalExists,
		OriginalJSON:   disk.OriginalJSON,
		OriginalText:   disk.OriginalText,
	}
	backup.Files = normalizeApplyBackupFiles(backup)
	return backup, nil
}

func deleteApplyBackup(applyID string) error {
	path, err := applyBackupPath(applyID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func preflightLocalApply(st state, applyID string, previews []response.PatchPreviewItem, targetOverride string) (preflightResult, error) {
	if len(previews) == 0 {
		return preflightResult{}, errors.New("no patch preview available for apply")
	}

	result := preflightResult{
		ApplyID: applyID,
		Allowed: true,
		Reason:  "preflight passed",
		Steps:   make([]preflightStep, 0, len(previews)),
	}
	projectRoot := st.repoPath()
	resolvedSeen := map[string]struct{}{}
	for index, preview := range previews {
		resolvedPath, source, err := resolveApplyTarget(preview.FilePath, stepTargetOverride(targetOverride, index), projectRoot)
		if err != nil {
			return preflightResult{}, err
		}
		step := preflightStep{
			TargetFile:   resolvedPath,
			Operation:    preview.Operation,
			PreviewFile:  preview.FilePath,
			TargetSource: source,
			Allowed:      true,
			Guard:        "strict",
			Reason:       "preflight passed",
		}
		switch {
		case !isAllowedOperation(preview.Operation):
			step.Allowed = false
			step.Guard = "operation"
			step.Reason = "unsupported patch operation"
		case !isAllowedTarget(preview.FilePath, resolvedPath, projectRoot):
			step.Allowed = false
			step.Guard = "file_scope"
			step.Reason = "target file is outside the local guard allowlist"
		default:
			if _, exists := resolvedSeen[resolvedPath]; exists {
				step.Allowed = false
				step.Guard = "duplicate_target"
				step.Reason = "multiple change plan steps target the same file"
			}
		}
		if step.Allowed {
			resolvedSeen[resolvedPath] = struct{}{}
		} else {
			result.Allowed = false
			result.Reason = "one or more change plan steps failed preflight"
		}
		result.Steps = append(result.Steps, step)
	}
	return result, nil
}
