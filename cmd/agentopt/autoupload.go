package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type autouploadMetadata struct {
	Label           string    `json:"label"`
	Platform        string    `json:"platform"`
	IntervalSeconds int       `json:"interval_seconds"`
	Recent          int       `json:"recent"`
	Tool            string    `json:"tool"`
	CodexHome       string    `json:"codex_home,omitempty"`
	SnapshotMode    string    `json:"snapshot_mode"`
	SnapshotFile    string    `json:"snapshot_file,omitempty"`
	ProfileID       string    `json:"profile_id"`
	PlistPath       string    `json:"plist_path"`
	ScriptPath      string    `json:"script_path"`
	LogPath         string    `json:"log_path"`
	EnabledAt       time.Time `json:"enabled_at"`
}

type autouploadStatusResp struct {
	Enabled  bool                `json:"enabled"`
	Loaded   bool                `json:"loaded"`
	Metadata *autouploadMetadata `json:"metadata,omitempty"`
}

type autouploadDisableResp struct {
	Disabled bool   `json:"disabled"`
	Label    string `json:"label"`
}

type autouploadBaseCommand struct {
	Program string
	Args    []string
	Workdir string
}

func runAutoupload(args []string) error {
	if len(args) == 0 {
		return errors.New("autoupload requires a subcommand: enable, disable, status")
	}

	switch args[0] {
	case "enable":
		return runAutouploadEnable(args[1:])
	case "disable":
		return runAutouploadDisable(args[1:])
	case "status":
		return runAutouploadStatus(args[1:])
	default:
		return fmt.Errorf("unknown autoupload subcommand %q", args[0])
	}
}

func runAutouploadEnable(args []string) error {
	if err := ensureAutouploadPlatform(); err != nil {
		return err
	}

	fs := flag.NewFlagSet("autoupload enable", flag.ContinueOnError)
	interval := fs.Duration("interval", 30*time.Minute, "background upload interval")
	recent := fs.Int("recent", 1, "number of recent local Codex session JSONL files to upload per run")
	tool := fs.String("tool", "codex", "tool name")
	codexHome := fs.String("codex-home", "", "override Codex home used for automatic session collection")
	snapshotMode := fs.String("snapshot-mode", collectSnapshotModeChanged, "snapshot upload mode: changed, always, skip")
	snapshotFile := fs.String("snapshot-file", "", "snapshot JSON file path")
	profileID := fs.String("profile-id", "default", "profile identifier for snapshot uploads")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval <= 0 {
		return errors.New("autoupload enable --interval must be greater than zero")
	}
	if *recent < 1 {
		return errors.New("autoupload enable --recent must be at least 1")
	}
	resolvedSnapshotMode, err := parseCollectSnapshotMode(*snapshotMode)
	if err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	meta, err := buildAutouploadMetadata(st, *interval, *recent, *tool, *codexHome, resolvedSnapshotMode, *snapshotFile, *profileID)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(meta.ScriptPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(meta.LogPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(meta.PlistPath), 0o755); err != nil {
		return err
	}

	script, err := renderAutouploadScript(*meta)
	if err != nil {
		return err
	}
	if err := os.WriteFile(meta.ScriptPath, []byte(script), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(meta.PlistPath, []byte(renderLaunchdPlist(*meta)), 0o644); err != nil {
		return err
	}
	if err := saveAutouploadMetadata(*meta); err != nil {
		return err
	}

	_ = runLaunchctl("unload", meta.PlistPath)
	if err := runLaunchctl("load", "-w", meta.PlistPath); err != nil {
		return err
	}

	return prettyPrint(autouploadStatusResp{
		Enabled:  true,
		Loaded:   true,
		Metadata: meta,
	})
}

func runAutouploadDisable(args []string) error {
	if err := ensureAutouploadPlatform(); err != nil {
		return err
	}

	fs := flag.NewFlagSet("autoupload disable", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	label := autouploadLabel(st)
	meta, _ := loadAutouploadMetadata(st)
	plistPath, err := autouploadPlistPath(st)
	if err != nil {
		return err
	}
	scriptPath, err := autouploadScriptPath(st)
	if err != nil {
		return err
	}
	if meta != nil {
		plistPath = meta.PlistPath
		scriptPath = meta.ScriptPath
	}

	_ = runLaunchctl("unload", plistPath)
	_ = os.Remove(plistPath)
	_ = os.Remove(scriptPath)
	_ = os.Remove(autouploadMetadataPath(st))

	return prettyPrint(autouploadDisableResp{
		Disabled: true,
		Label:    label,
	})
}

func runAutouploadStatus(args []string) error {
	if err := ensureAutouploadPlatform(); err != nil {
		return err
	}

	fs := flag.NewFlagSet("autoupload status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := loadWorkspaceState()
	if err != nil {
		return err
	}
	meta, err := loadAutouploadMetadata(st)
	if err != nil {
		return err
	}
	if meta == nil {
		return prettyPrint(autouploadStatusResp{Enabled: false, Loaded: false})
	}

	enabled := fileExists(meta.PlistPath) && fileExists(meta.ScriptPath)
	loaded := false
	if enabled {
		loaded = autouploadLoaded(meta.Label)
	}
	return prettyPrint(autouploadStatusResp{
		Enabled:  enabled,
		Loaded:   loaded,
		Metadata: meta,
	})
}

func buildAutouploadMetadata(st state, interval time.Duration, recent int, tool, codexHome, snapshotMode, snapshotFile, profileID string) (*autouploadMetadata, error) {
	plistPath, err := autouploadPlistPath(st)
	if err != nil {
		return nil, err
	}
	scriptPath, err := autouploadScriptPath(st)
	if err != nil {
		return nil, err
	}
	logPath, err := autouploadLogPath(st)
	if err != nil {
		return nil, err
	}

	return &autouploadMetadata{
		Label:           autouploadLabel(st),
		Platform:        "launchd",
		IntervalSeconds: durationToLaunchdSeconds(interval),
		Recent:          recent,
		Tool:            tool,
		CodexHome:       codexHome,
		SnapshotMode:    snapshotMode,
		SnapshotFile:    snapshotFile,
		ProfileID:       profileID,
		PlistPath:       plistPath,
		ScriptPath:      scriptPath,
		LogPath:         logPath,
		EnabledAt:       time.Now().UTC(),
	}, nil
}

func renderAutouploadScript(meta autouploadMetadata) (string, error) {
	command, err := resolveAutouploadBaseCommand()
	if err != nil {
		return "", err
	}

	var lines []string
	lines = append(lines, "#!/bin/sh", "set -eu")
	if root := strings.TrimSpace(os.Getenv("AGENTOPT_HOME")); root != "" {
		lines = append(lines, "export AGENTOPT_HOME="+shellQuote(root))
	}
	if command.Workdir != "" {
		lines = append(lines, "cd "+shellQuote(command.Workdir))
	}

	args := append([]string{command.Program}, command.Args...)
	args = append(args,
		"collect",
		"--recent", strconv.Itoa(meta.Recent),
		"--tool", meta.Tool,
		"--snapshot-mode", meta.SnapshotMode,
		"--profile-id", meta.ProfileID,
	)
	if meta.CodexHome != "" {
		args = append(args, "--codex-home", meta.CodexHome)
	}
	if meta.SnapshotFile != "" {
		args = append(args, "--snapshot-file", meta.SnapshotFile)
	}

	lines = append(lines, "exec "+joinShellArgs(args))
	return strings.Join(lines, "\n") + "\n", nil
}

func renderLaunchdPlist(meta autouploadMetadata) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>StartInterval</key>
  <integer>%d</integer>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, xmlEscape(meta.Label), xmlEscape(meta.ScriptPath), meta.IntervalSeconds, xmlEscape(meta.LogPath), xmlEscape(meta.LogPath))
}

func autouploadLabel(st state) string {
	return "com.agentopt.autoupload." + sanitizeID(st.workspaceID())
}

func autouploadPlistPath(st state) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", autouploadLabel(st)+".plist"), nil
}

func autouploadScriptPath(st state) (string, error) {
	root, err := agentoptHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "autoupload", autouploadLabel(st)+".sh"), nil
}

func autouploadMetadataPath(st state) string {
	root, err := agentoptHomeDir()
	if err != nil {
		return filepath.Join(".agentopt", "autoupload", autouploadLabel(st)+".json")
	}
	return filepath.Join(root, "autoupload", autouploadLabel(st)+".json")
}

func autouploadLogPath(st state) (string, error) {
	root, err := agentoptHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "logs", autouploadLabel(st)+".log"), nil
}

func saveAutouploadMetadata(meta autouploadMetadata) error {
	path := filepath.Join(filepath.Dir(meta.ScriptPath), meta.Label+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func loadAutouploadMetadata(st state) (*autouploadMetadata, error) {
	path := autouploadMetadataPath(st)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var meta autouploadMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func autouploadLoaded(label string) bool {
	return runLaunchctl("list", label) == nil
}

func ensureAutouploadPlatform() error {
	if runtime.GOOS == "darwin" {
		return nil
	}
	if strings.TrimSpace(os.Getenv("AGENTOPT_LAUNCHCTL_BIN")) != "" {
		return nil
	}
	return errors.New("autoupload is currently supported on macOS launchd only")
}

func runLaunchctl(args ...string) error {
	bin := strings.TrimSpace(os.Getenv("AGENTOPT_LAUNCHCTL_BIN"))
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

func resolveAutouploadBaseCommand() (autouploadBaseCommand, error) {
	executable, err := os.Executable()
	if err == nil && shouldUseStableExecutable(executable) {
		return autouploadBaseCommand{Program: executable}, nil
	}
	if installed, ok := findInstalledAgentopt(); ok {
		return autouploadBaseCommand{Program: installed}, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return autouploadBaseCommand{}, err
	}
	repoRoot, ok := detectRepoRoot(cwd)
	if ok {
		return autouploadBaseCommand{
			Program: "go",
			Args:    []string{"run", "./cmd/agentopt"},
			Workdir: repoRoot,
		}, nil
	}
	return autouploadBaseCommand{}, errors.New("unable to infer a stable agentopt command for autoupload; run from the repo root or use a built agentopt binary")
}

func findInstalledAgentopt() (string, bool) {
	if resolved, err := exec.LookPath("agentopt"); err == nil && shouldUseStableExecutable(resolved) {
		return filepath.Clean(resolved), true
	}

	var candidates []string
	if binDir := strings.TrimSpace(os.Getenv("AGENTOPT_BIN_DIR")); binDir != "" {
		candidates = append(candidates, filepath.Join(binDir, "agentopt"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "agentopt"))
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
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "cmd", "agentopt", "main.go")) {
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
	if !strings.Contains(base, "agentopt") {
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

func agentoptHomeDir() (string, error) {
	path, err := stateFilePath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
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
