# Crux CLI

Public GitHub Releases for the Crux CLI.

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/crux-cli/main/scripts/install.sh | sh
```

Install a specific release:

```bash
CRUX_VERSION=0.1.0-beta.1-52-gf97bdc8 curl -fsSL https://raw.githubusercontent.com/Royaltyprogram/crux-cli/main/scripts/install.sh | sh
```

Re-upload all local Codex sessions after clearing the saved upload cursor:

```bash
crux collect --reset-sessions
```

If one local session is rejected with `Invalid Params`, the collector now skips that session, records it in the JSON result, and continues with the rest of the backlog.

Source build:
- source repository: `Royaltyprogram/aiops`
- source commit: `f97bdc8`
- published version: `0.1.0-beta.1-52-gf97bdc8`
