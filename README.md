# Autoskills CLI

Public distribution repository for the Autoskills CLI. Pre-built binaries are published via GitHub Releases.

## Installation

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/autoskills-cli/main/scripts/install.sh | sh
```

Install a specific release:

```bash
AUTOSKILLS_VERSION=<release-tag> curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/autoskills-cli/main/scripts/install.sh | sh
```

The installer selects the newest published release that has a bundle for the current platform (macOS and Linux supported).

## Usage

Re-upload all local Codex sessions after clearing the saved upload cursor:

```bash
autoskills collect --reset-sessions
```

If one local session is rejected with `Invalid Params`, the collector skips that session, records it in the JSON result, and continues with the rest of the backlog.
