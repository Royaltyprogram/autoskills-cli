# Autoskills CLI
<img width="1536" height="1024" alt="image" src="https://github.com/user-attachments/assets/ce24943d-8f96-4bde-ab93-3a701db9aebf" />

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


## Note 

If there is no command awared at the terminal "autoskills" after running the script, run the following commands.

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zprofile
exec zsh -l
```

## How it works

<img width="943" height="446" alt="image" src="https://github.com/user-attachments/assets/ca56990a-29f3-4d2e-bd05-008ad4487341" />

## Usage

Re-upload all local Codex sessions after clearing the saved upload cursor:

```bash
autoskills collect --reset-sessions
```

If one local session is rejected with `Invalid Params`, the collector skips that session, records it in the JSON result, and continues with the rest of the backlog.
