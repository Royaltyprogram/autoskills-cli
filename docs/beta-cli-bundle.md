# Crux Closed Beta CLI Bundle

Build metadata:

- Version: `__VERSION__`
- Commit: `__COMMIT__`
- Build date: `__BUILD_DATE__`

This bundle includes:

- `crux`

One-command install is available for GitHub Releases:

```bash
curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/crux-cli/main/scripts/install.sh | sh
CRUX_VERSION=0.1.0-beta.1 curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/crux-cli/main/scripts/install.sh | sh
```

The installer downloads the matching release bundle for the current OS and architecture, installs it under `~/.local/share/crux/<version>`, and writes a wrapper to `~/.local/bin/crux`.
The release install uses a prebuilt binary, so Go is not required.

After install, run the CLI directly:

```bash
crux version
crux setup
crux reports
crux audit
crux collect --reset-sessions
```

`crux setup` prompts for the issued CLI token if you omit `--token`, connects the current repo to the shared workspace, uploads an initial snapshot plus local Codex session history on first setup, and enrolls background collection automatically on supported installed macOS environments.
If background enrollment is not supported on the machine, setup returns the manual fallback command to run instead.
After setup, plain `crux` prints the current shared-workspace status.

If your shell cannot find `crux`, add `~/.local/bin` to `PATH`.
