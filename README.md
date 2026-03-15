# Crux CLI

Public distribution repo for the Crux CLI build artifacts and mirrored CLI source snapshots.

Install the latest GitHub release:

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

Current mirrored build:

- source repository: `Royaltyprogram/aiops`
- source commit: `a9f2436`
- mirrored version: `0.1.0-beta.2-a9f2436`
- artifact path: `artifacts/crux-0.1.0-beta.2-a9f2436-darwin-arm64.tar.gz`

Mirrored CLI sources from the same `aiops` commit live under `source/`.
