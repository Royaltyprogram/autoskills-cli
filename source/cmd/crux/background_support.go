package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type backgroundBaseCommand struct {
	Program string
	Args    []string
	Workdir string
}

type backgroundSetupResp struct {
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Command    string `json:"command,omitempty"`
	Label      string `json:"label,omitempty"`
	PlistPath  string `json:"plist_path,omitempty"`
	StdoutPath string `json:"stdout_path,omitempty"`
	StderrPath string `json:"stderr_path,omitempty"`
	Interval   string `json:"interval,omitempty"`
}

type backgroundResetResp struct {
	Status          string   `json:"status"`
	Label           string   `json:"label,omitempty"`
	PlistPath       string   `json:"plist_path,omitempty"`
	PlistRemoved    bool     `json:"plist_removed,omitempty"`
	UnloadAttempted bool     `json:"unload_attempted,omitempty"`
	UnloadSucceeded bool     `json:"unload_succeeded,omitempty"`
	LogDir          string   `json:"log_dir,omitempty"`
	LogsRemoved     bool     `json:"logs_removed,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

type backgroundSetupOptions struct {
	Enabled   bool
	CodexHome string
	Recent    int
	Interval  time.Duration
}

func ensureLaunchdPlatform() error {
	if runtime.GOOS == "darwin" {
		return nil
	}
	if strings.TrimSpace(os.Getenv("CRUX_LAUNCHCTL_BIN")) != "" {
		return nil
	}
	return errors.New("daemon is currently supported on macOS launchd only")
}

func launchdLoaded(label string) bool {
	return runLaunchctl("list", label) == nil
}

func runLaunchctl(args ...string) error {
	bin := strings.TrimSpace(os.Getenv("CRUX_LAUNCHCTL_BIN"))
	if bin == "" {
		bin = "launchctl"
	}
	cmd := exec.Command(bin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, text)
	}
	return nil
}

func resolveBackgroundBaseCommand() (backgroundBaseCommand, error) {
	executable, err := os.Executable()
	if err == nil && shouldUseStableExecutable(executable) {
		return backgroundBaseCommand{Program: executable}, nil
	}
	if installed, ok := findInstalledCrux(); ok {
		return backgroundBaseCommand{Program: installed}, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return backgroundBaseCommand{}, err
	}
	repoRoot, ok := detectRepoRoot(cwd)
	if ok {
		goBin, lookErr := exec.LookPath("go")
		if lookErr != nil {
			return backgroundBaseCommand{}, lookErr
		}
		return backgroundBaseCommand{
			Program: goBin,
			Args:    []string{"run", "./cmd/crux"},
			Workdir: repoRoot,
		}, nil
	}
	return backgroundBaseCommand{}, errors.New("unable to infer a stable crux command for daemon; run from the repo root or use a built crux binary")
}

func ensureBackgroundCollection(opts backgroundSetupOptions) backgroundSetupResp {
	command := manualBackgroundCollectCommand(opts.CodexHome, opts.Recent, opts.Interval)
	if !opts.Enabled {
		return backgroundSetupResp{
			Status:  "disabled",
			Command: command,
		}
	}
	if err := ensureLaunchdPlatform(); err != nil {
		return backgroundSetupResp{
			Status:   "manual_only",
			Reason:   err.Error(),
			Command:  command,
			Interval: opts.Interval.String(),
		}
	}

	base, err := resolveBackgroundBaseCommand()
	if err != nil {
		return backgroundSetupResp{
			Status:   "manual_only",
			Reason:   err.Error(),
			Command:  command,
			Interval: opts.Interval.String(),
		}
	}
	if !backgroundCommandIsStable(base) {
		return backgroundSetupResp{
			Status:   "manual_only",
			Reason:   "automatic background setup is only enabled for installed or built crux binaries",
			Command:  command,
			Interval: opts.Interval.String(),
		}
	}

	resp, err := installLaunchdCollector(base, opts)
	if err != nil {
		return backgroundSetupResp{
			Status:   "failed",
			Reason:   err.Error(),
			Command:  command,
			Interval: opts.Interval.String(),
		}
	}
	resp.Command = command
	return resp
}

func installLaunchdCollector(base backgroundBaseCommand, opts backgroundSetupOptions) (backgroundSetupResp, error) {
	homeDir, err := cruxHomeDir()
	if err != nil {
		return backgroundSetupResp{}, err
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return backgroundSetupResp{}, err
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return backgroundSetupResp{}, err
	}
	launchAgentsDir := filepath.Join(userHome, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0o755); err != nil {
		return backgroundSetupResp{}, err
	}

	logDir := filepath.Join(homeDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return backgroundSetupResp{}, err
	}

	label := backgroundLaunchdLabel(homeDir)
	plistPath := filepath.Join(launchAgentsDir, label+".plist")
	stdoutPath := filepath.Join(logDir, "collector.stdout.log")
	stderrPath := filepath.Join(logDir, "collector.stderr.log")
	arguments := append([]string{base.Program}, base.Args...)
	arguments = append(arguments, collectWatchArgs(opts.CodexHome, opts.Recent, opts.Interval)...)
	env := backgroundEnvironment()

	if err := os.WriteFile(plistPath, []byte(renderLaunchdPlist(label, arguments, base.Workdir, stdoutPath, stderrPath, env)), 0o644); err != nil {
		return backgroundSetupResp{}, err
	}

	_ = runLaunchctl("unload", plistPath)
	if err := runLaunchctl("load", "-w", plistPath); err != nil {
		return backgroundSetupResp{}, err
	}
	if !launchdLoaded(label) {
		return backgroundSetupResp{}, errors.New("launchd did not report the collector as loaded")
	}

	return backgroundSetupResp{
		Status:     "enabled",
		Label:      label,
		PlistPath:  plistPath,
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
		Interval:   opts.Interval.String(),
	}, nil
}

func backgroundEnvironment() map[string]string {
	env := map[string]string{}
	if value := strings.TrimSpace(os.Getenv("CRUX_HOME")); value != "" {
		env["CRUX_HOME"] = value
	}
	if value := strings.TrimSpace(os.Getenv("PATH")); value != "" {
		env["PATH"] = value
	}
	return env
}

func backgroundCommandIsStable(base backgroundBaseCommand) bool {
	if strings.TrimSpace(base.Program) == "" {
		return false
	}
	if strings.TrimSpace(base.Workdir) != "" {
		return false
	}
	return filepath.Base(base.Program) != "go"
}

func collectWatchArgs(codexHome string, recent int, interval time.Duration) []string {
	args := []string{
		"collect",
		"--watch",
		"--recent", fmt.Sprintf("%d", recent),
		"--interval", interval.String(),
	}
	if strings.TrimSpace(codexHome) != "" {
		args = append(args, "--codex-home", codexHome)
	}
	return args
}

func manualBackgroundCollectCommand(codexHome string, recent int, interval time.Duration) string {
	args := append([]string{"crux"}, collectWatchArgs(codexHome, recent, interval)...)
	return joinShellArgs(args)
}

func backgroundLaunchdLabel(homeDir string) string {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(filepath.Clean(homeDir)))
	return fmt.Sprintf("io.crux.collect.%08x", hasher.Sum32())
}

func renderLaunchdPlist(label string, arguments []string, workdir, stdoutPath, stderrPath string, env map[string]string) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	builder.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	builder.WriteString(`<plist version="1.0">` + "\n")
	builder.WriteString(`<dict>` + "\n")
	builder.WriteString(`  <key>Label</key>` + "\n")
	builder.WriteString(`  <string>` + xmlEscape(label) + `</string>` + "\n")
	builder.WriteString(`  <key>ProgramArguments</key>` + "\n")
	builder.WriteString(`  <array>` + "\n")
	for _, arg := range arguments {
		builder.WriteString(`    <string>` + xmlEscape(arg) + `</string>` + "\n")
	}
	builder.WriteString(`  </array>` + "\n")
	if strings.TrimSpace(workdir) != "" {
		builder.WriteString(`  <key>WorkingDirectory</key>` + "\n")
		builder.WriteString(`  <string>` + xmlEscape(workdir) + `</string>` + "\n")
	}
	builder.WriteString(`  <key>RunAtLoad</key>` + "\n")
	builder.WriteString(`  <true/>` + "\n")
	builder.WriteString(`  <key>KeepAlive</key>` + "\n")
	builder.WriteString(`  <true/>` + "\n")
	builder.WriteString(`  <key>StandardOutPath</key>` + "\n")
	builder.WriteString(`  <string>` + xmlEscape(stdoutPath) + `</string>` + "\n")
	builder.WriteString(`  <key>StandardErrorPath</key>` + "\n")
	builder.WriteString(`  <string>` + xmlEscape(stderrPath) + `</string>` + "\n")
	if len(env) > 0 {
		builder.WriteString(`  <key>EnvironmentVariables</key>` + "\n")
		builder.WriteString(`  <dict>` + "\n")
		for key, value := range env {
			builder.WriteString(`    <key>` + xmlEscape(key) + `</key>` + "\n")
			builder.WriteString(`    <string>` + xmlEscape(value) + `</string>` + "\n")
		}
		builder.WriteString(`  </dict>` + "\n")
	}
	builder.WriteString(`</dict>` + "\n")
	builder.WriteString(`</plist>` + "\n")
	return builder.String()
}

func findInstalledCrux() (string, bool) {
	if resolved, err := exec.LookPath("crux"); err == nil && shouldUseStableExecutable(resolved) {
		return filepath.Clean(resolved), true
	}

	var candidates []string
	if binDir := strings.TrimSpace(os.Getenv("CRUX_BIN_DIR")); binDir != "" {
		candidates = append(candidates, filepath.Join(binDir, "crux"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "crux"))
	}
	for _, candidate := range candidates {
		if shouldUseStableExecutable(candidate) && fileExists(candidate) {
			return filepath.Clean(candidate), true
		}
	}
	return "", false
}

func detectRepoRoot(start string) (string, bool) {
	dir := filepath.Clean(start)
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "cmd", "crux", "main.go")) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func shouldUseStableExecutable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	cleaned := filepath.Clean(path)
	base := strings.ToLower(filepath.Base(cleaned))
	if !strings.Contains(base, "crux") {
		return false
	}
	tempRoot := filepath.Clean(os.TempDir())
	if isWithinRoot(tempRoot, cleaned) {
		return false
	}
	return !strings.Contains(cleaned, string(os.PathSeparator)+"go-build")
}

func durationToLaunchdSeconds(interval time.Duration) int {
	seconds := int(interval / time.Second)
	if interval%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func cruxHomeDir() (string, error) {
	path, err := stateFilePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func resetBackgroundCollection(homeDir string) backgroundResetResp {
	resp := backgroundResetResp{
		Status: "not_configured",
	}

	trimmedHome := strings.TrimSpace(homeDir)
	if trimmedHome == "" {
		return resp
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		resp.Status = "cleanup_failed"
		resp.Warnings = append(resp.Warnings, err.Error())
		return resp
	}

	label := backgroundLaunchdLabel(trimmedHome)
	plistPath := filepath.Join(userHome, "Library", "LaunchAgents", label+".plist")
	logDir := filepath.Join(trimmedHome, "logs")

	resp.Label = label
	resp.PlistPath = plistPath
	resp.LogDir = logDir

	if fileExists(plistPath) {
		resp.Status = "removed"
		if runtime.GOOS == "darwin" || strings.TrimSpace(os.Getenv("CRUX_LAUNCHCTL_BIN")) != "" {
			resp.UnloadAttempted = true
			if err := runLaunchctl("unload", plistPath); err != nil {
				resp.Warnings = append(resp.Warnings, fmt.Sprintf("failed to unload background collector: %v", err))
			} else {
				resp.UnloadSucceeded = true
			}
		}
		if err := os.Remove(plistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			resp.Status = "cleanup_failed"
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("failed to remove plist: %v", err))
		} else {
			resp.PlistRemoved = true
		}
	}

	if fileExists(logDir) {
		if err := os.RemoveAll(logDir); err != nil {
			resp.Status = "cleanup_failed"
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("failed to remove logs: %v", err))
		} else {
			resp.LogsRemoved = true
			if resp.Status == "not_configured" {
				resp.Status = "removed"
			}
		}
	}

	return resp
}

func joinShellArgs(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, shellQuote(value))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isWithinRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if root == target {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
