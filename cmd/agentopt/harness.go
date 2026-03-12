package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Royaltyprogram/aiops/dto/request"
	"github.com/Royaltyprogram/aiops/dto/response"
)

const (
	defaultHarnessSpecVersion  = 1
	defaultHarnessCommandLimit = 4000
)

type harnessSpec struct {
	Version       int                `json:"version"`
	Name          string             `json:"name"`
	Goal          string             `json:"goal"`
	SetupCommands []string           `json:"setup_commands,omitempty"`
	TestCommands  []string           `json:"test_commands"`
	Assertions    []harnessAssertion `json:"assertions,omitempty"`
}

type harnessAssertion struct {
	Kind        string `json:"kind"`
	Equals      int    `json:"equals,omitempty"`
	Contains    string `json:"contains,omitempty"`
	NotContains string `json:"not_contains,omitempty"`
}

type harnessCommandResult struct {
	Phase      string `json:"phase"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output,omitempty"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
}

type harnessSpecResult struct {
	File        string                 `json:"file"`
	Name        string                 `json:"name"`
	Goal        string                 `json:"goal,omitempty"`
	Passed      bool                   `json:"passed"`
	Reason      string                 `json:"reason,omitempty"`
	DurationMS  int64                  `json:"duration_ms"`
	StartedAt   time.Time              `json:"started_at"`
	CompletedAt time.Time              `json:"completed_at"`
	Commands    []harnessCommandResult `json:"commands"`
}

type harnessRunResponse struct {
	Root    string                    `json:"root"`
	Files   int                       `json:"files"`
	Passed  bool                      `json:"passed"`
	Uploads []response.HarnessRunResp `json:"uploads,omitempty"`
	Results []harnessSpecResult       `json:"results"`
}

type harnessUploadOptions struct {
	Disabled         bool
	Client           *apiClient
	ProjectID        string
	RecommendationID string
	ApplyID          string
	TriggeredBy      string
}

type harnessGateResult struct {
	Root     string
	Executed bool
	Passed   bool
	Results  []harnessSpecResult
	Uploads  []response.HarnessRunResp
}

func runHarness(args []string) error {
	if len(args) == 0 || args[0] == "run" {
		runArgs := args
		if len(runArgs) > 0 && runArgs[0] == "run" {
			runArgs = runArgs[1:]
		}
		return runHarnessRun(runArgs)
	}
	return fmt.Errorf("unknown harness command %q", args[0])
}

func runHarnessRun(args []string) error {
	fs := flag.NewFlagSet("harness run", flag.ContinueOnError)
	specFile := fs.String("file", "", "specific harness spec JSON file")
	rootDir := fs.String("dir", "", "repo root override")
	timeout := fs.Duration("timeout", 10*time.Minute, "per-command timeout")
	noUpload := fs.Bool("no-upload", false, "skip uploading harness results to the AgentOpt server")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("harness --timeout must be greater than zero")
	}

	root, files, err := resolveHarnessFiles(*rootDir, *specFile)
	if err != nil {
		return err
	}

	results := make([]harnessSpecResult, 0, len(files))
	allPassed := true
	for _, path := range files {
		spec, err := loadHarnessSpec(path)
		if err != nil {
			return err
		}
		result := executeHarnessSpec(root, path, spec, *timeout)
		if !result.Passed {
			allPassed = false
		}
		results = append(results, result)
	}

	uploads, err := maybeUploadHarnessResults(root, results, harnessUploadOptions{Disabled: *noUpload})
	if err != nil {
		return err
	}

	resp := harnessRunResponse{
		Root:    root,
		Files:   len(files),
		Passed:  allPassed,
		Uploads: uploads,
		Results: results,
	}
	if err := prettyPrint(resp); err != nil {
		return err
	}
	if !allPassed {
		return errors.New("one or more harness specs failed")
	}
	return nil
}

func resolveHarnessFiles(rootOverride, specFile string) (string, []string, error) {
	if strings.TrimSpace(specFile) != "" {
		path, err := filepath.Abs(strings.TrimSpace(specFile))
		if err != nil {
			return "", nil, err
		}
		path = canonicalizePath(path)
		root := findHarnessRoot(filepath.Dir(path))
		if root == "" {
			root = filepath.Dir(path)
		}
		return root, []string{path}, nil
	}

	start := strings.TrimSpace(rootOverride)
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", nil, err
		}
		start = cwd
	}
	start = canonicalizePath(start)
	root := findHarnessRoot(start)
	if root == "" {
		root = start
	}
	harnessDir := filepath.Join(root, ".agentopt", "harness")
	entries, err := os.ReadDir(harnessDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, fmt.Errorf("no AgentOpt harness specs found under %s", harnessDir)
		}
		return "", nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		files = append(files, filepath.Join(harnessDir, entry.Name()))
	}
	sort.Strings(files)
	if len(files) == 0 {
		return "", nil, fmt.Errorf("no AgentOpt harness specs found under %s", harnessDir)
	}
	return root, files, nil
}

func findHarnessRoot(start string) string {
	current := canonicalizePath(start)
	for current != "" {
		candidate := filepath.Join(current, ".agentopt", "harness")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
	return ""
}

func loadHarnessSpec(path string) (harnessSpec, error) {
	var spec harnessSpec
	if err := loadJSONFile(path, &spec); err != nil {
		return harnessSpec{}, err
	}
	if spec.Version == 0 {
		spec.Version = defaultHarnessSpecVersion
	}
	if spec.Version != defaultHarnessSpecVersion {
		return harnessSpec{}, fmt.Errorf("unsupported harness version %d in %s", spec.Version, path)
	}
	if strings.TrimSpace(spec.Name) == "" {
		spec.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if len(spec.TestCommands) == 0 {
		return harnessSpec{}, fmt.Errorf("harness %s has no test_commands", path)
	}
	return spec, nil
}

func executeHarnessSpec(root, path string, spec harnessSpec, timeout time.Duration) (result harnessSpecResult) {
	result = harnessSpecResult{
		File:      path,
		Name:      spec.Name,
		Goal:      spec.Goal,
		Passed:    true,
		Reason:    "all commands passed",
		StartedAt: time.Now().UTC(),
		Commands:  make([]harnessCommandResult, 0, len(spec.SetupCommands)+len(spec.TestCommands)),
	}
	defer func() {
		result.CompletedAt = time.Now().UTC()
		result.DurationMS = result.CompletedAt.Sub(result.StartedAt).Milliseconds()
	}()

	for _, command := range spec.SetupCommands {
		commandResult := runHarnessCommand(root, "setup", command, timeout)
		result.Commands = append(result.Commands, commandResult)
		if !commandResult.Passed {
			result.Passed = false
			result.Reason = firstNonEmpty(commandResult.Error, "setup command failed")
			return result
		}
	}

	testResults := make([]harnessCommandResult, 0, len(spec.TestCommands))
	for _, command := range spec.TestCommands {
		commandResult := runHarnessCommand(root, "test", command, timeout)
		result.Commands = append(result.Commands, commandResult)
		testResults = append(testResults, commandResult)
		if !commandResult.Passed {
			result.Passed = false
			result.Reason = firstNonEmpty(commandResult.Error, "test command failed")
			return result
		}
	}

	passed, reason := evaluateHarnessAssertions(spec.Assertions, testResults)
	result.Passed = passed
	result.Reason = reason
	return result
}

func maybeUploadHarnessResults(root string, results []harnessSpecResult, options harnessUploadOptions) ([]response.HarnessRunResp, error) {
	if options.Disabled || len(results) == 0 {
		return nil, nil
	}

	client := options.Client
	projectID := strings.TrimSpace(options.ProjectID)
	triggeredBy := strings.TrimSpace(options.TriggeredBy)
	recommendationID := strings.TrimSpace(options.RecommendationID)
	applyID := strings.TrimSpace(options.ApplyID)
	if client == nil || projectID == "" {
		st, err := loadState()
		if err != nil {
			if shouldSkipHarnessUpload(err) {
				return nil, nil
			}
			return nil, err
		}
		if strings.TrimSpace(st.ServerURL) == "" || strings.TrimSpace(st.APIToken) == "" {
			return nil, nil
		}
		workspace, ok := st.matchWorkspaceByDir(root)
		if !ok || strings.TrimSpace(workspace.ID) == "" {
			return nil, nil
		}
		st.rememberWorkspace(workspace)
		client = newAPIClient(st.ServerURL, st.APIToken)
		projectID = st.workspaceID()
		if triggeredBy == "" {
			triggeredBy = st.UserID
		}
	}

	uploads := make([]response.HarnessRunResp, 0, len(results))
	for _, item := range results {
		var uploaded response.HarnessRunResp
		if err := client.doJSON(http.MethodPost, "/api/v1/harness-runs", request.HarnessRunReq{
			ProjectID:        projectID,
			RecommendationID: recommendationID,
			ApplyID:          applyID,
			SpecFile:         relativizeHarnessPath(root, item.File),
			Name:             item.Name,
			Goal:             item.Goal,
			Passed:           item.Passed,
			Reason:           item.Reason,
			RootDir:          canonicalizePath(root),
			DurationMS:       item.DurationMS,
			TriggeredBy:      triggeredBy,
			Commands:         toHarnessCommandResultReq(item.Commands),
			StartedAt:        item.StartedAt,
			CompletedAt:      item.CompletedAt,
		}, &uploaded); err != nil {
			return uploads, err
		}
		uploads = append(uploads, uploaded)
	}
	return uploads, nil
}

func runHarnessGate(st state, client *apiClient, applyID, recommendationID string) (harnessGateResult, error) {
	root, files, found, err := discoverHarnessFiles(st.repoPath())
	if err != nil {
		return harnessGateResult{}, err
	}
	if !found {
		return harnessGateResult{}, nil
	}

	results := make([]harnessSpecResult, 0, len(files))
	allPassed := true
	for _, path := range files {
		spec, err := loadHarnessSpec(path)
		if err != nil {
			return harnessGateResult{}, err
		}
		result := executeHarnessSpec(root, path, spec, 10*time.Minute)
		if !result.Passed {
			allPassed = false
		}
		results = append(results, result)
	}
	uploads, err := maybeUploadHarnessResults(root, results, harnessUploadOptions{
		Client:           client,
		ProjectID:        st.workspaceID(),
		RecommendationID: recommendationID,
		ApplyID:          applyID,
		TriggeredBy:      st.UserID,
	})
	if err != nil {
		return harnessGateResult{}, err
	}
	return harnessGateResult{
		Root:     root,
		Executed: true,
		Passed:   allPassed,
		Results:  results,
		Uploads:  uploads,
	}, nil
}

func discoverHarnessFiles(rootOverride string) (string, []string, bool, error) {
	start := strings.TrimSpace(rootOverride)
	if start == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", nil, false, err
		}
		start = cwd
	}
	start = canonicalizePath(start)
	root := findHarnessRoot(start)
	if root == "" {
		return "", nil, false, nil
	}
	harnessDir := filepath.Join(root, ".agentopt", "harness")
	entries, err := os.ReadDir(harnessDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, false, nil
		}
		return "", nil, false, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		files = append(files, filepath.Join(harnessDir, entry.Name()))
	}
	sort.Strings(files)
	if len(files) == 0 {
		return root, nil, false, nil
	}
	return root, files, true, nil
}

func summarizeHarnessFailure(results []harnessSpecResult) string {
	for _, item := range results {
		if item.Passed {
			continue
		}
		return strings.TrimSpace(fmt.Sprintf("%s: %s", firstNonEmpty(item.Name, filepath.Base(item.File)), firstNonEmpty(item.Reason, "failed")))
	}
	if len(results) == 0 {
		return "no harness results"
	}
	return "one or more harness specs failed"
}

func shouldSkipHarnessUpload(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "run `agentopt login` first")
}

func relativizeHarnessPath(root, path string) string {
	root = canonicalizePath(root)
	path = canonicalizePath(path)
	if root == "" || path == "" {
		return filepath.Clean(path)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(relative)
}

func toHarnessCommandResultReq(items []harnessCommandResult) []request.HarnessCommandResultReq {
	out := make([]request.HarnessCommandResultReq, 0, len(items))
	for _, item := range items {
		out = append(out, request.HarnessCommandResultReq{
			Phase:      item.Phase,
			Command:    item.Command,
			ExitCode:   item.ExitCode,
			DurationMS: item.DurationMS,
			Output:     item.Output,
			Passed:     item.Passed,
			Error:      item.Error,
		})
	}
	return out
}

func runHarnessCommand(root, phase, raw string, timeout time.Duration) harnessCommandResult {
	result := harnessCommandResult{
		Phase:   phase,
		Command: strings.TrimSpace(raw),
		Passed:  false,
	}
	if result.Command == "" {
		result.Error = "empty command"
		return result
	}

	args := strings.Fields(result.Command)
	if len(args) == 0 {
		result.Error = "empty command"
		return result
	}
	if !isAllowedHarnessCommand(args[0]) {
		result.Error = fmt.Sprintf("command %q is outside the harness allowlist", args[0])
		return result
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	startedAt := time.Now()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	result.DurationMS = time.Since(startedAt).Milliseconds()
	result.Output = truncateHarnessOutput(string(output))
	result.ExitCode = 0

	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.ExitCode = -1
			result.Error = fmt.Sprintf("command timed out after %s", timeout)
			return result
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			result.Error = fmt.Sprintf("command exited with code %d", exitErr.ExitCode())
			return result
		}
		result.ExitCode = -1
		result.Error = err.Error()
		return result
	}

	result.Passed = true
	return result
}

func evaluateHarnessAssertions(assertions []harnessAssertion, commands []harnessCommandResult) (bool, string) {
	if len(commands) == 0 {
		return false, "no test commands were executed"
	}
	for _, command := range commands {
		if !command.Passed || command.ExitCode != 0 {
			return false, firstNonEmpty(command.Error, "test command failed")
		}
	}
	if len(assertions) == 0 {
		return true, "all commands passed"
	}

	outputs := make([]string, 0, len(commands))
	for _, command := range commands {
		outputs = append(outputs, command.Output)
	}
	combinedOutput := strings.Join(outputs, "\n")

	for _, assertion := range assertions {
		switch strings.TrimSpace(assertion.Kind) {
		case "exit_code":
			for _, command := range commands {
				if command.ExitCode != assertion.Equals {
					return false, fmt.Sprintf("expected exit code %d, got %d for %s", assertion.Equals, command.ExitCode, command.Command)
				}
			}
		case "output_contains":
			if !strings.Contains(combinedOutput, assertion.Contains) {
				return false, fmt.Sprintf("expected output to contain %q", assertion.Contains)
			}
		case "output_not_contains":
			if assertion.NotContains != "" && strings.Contains(combinedOutput, assertion.NotContains) {
				return false, fmt.Sprintf("expected output not to contain %q", assertion.NotContains)
			}
		default:
			return false, fmt.Sprintf("unsupported assertion kind %q", assertion.Kind)
		}
	}
	return true, "assertions passed"
}

func isAllowedHarnessCommand(program string) bool {
	switch filepath.Base(strings.TrimSpace(program)) {
	case "go", "make", "pytest", "python", "python3", "npm", "pnpm", "yarn", "npx", "uv", "cargo", "node", "jest", "vitest", "bundle", "rspec":
		return true
	default:
		return false
	}
}

func truncateHarnessOutput(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= defaultHarnessCommandLimit {
		return raw
	}
	return raw[:defaultHarnessCommandLimit] + "\n...[truncated]"
}
