---
name: crux-repo-discovery
description: Use when a task starts in an unfamiliar repository or the user keeps asking to inspect control flow, locate files, compare nearby contracts, and finish with targeted verification.
---

# Repo Discovery Baseline

Use this skill when the workspace still needs discovery before editing.

## Workflow

1. Locate the relevant entrypoints, touched files, and nearest tests before proposing code changes.
2. Summarize the current control flow in a few bullets so the diagnosis is explicit before edits.
3. Check neighboring contracts, shared helpers, and config files before changing behavior.
4. Prefer the smallest patch that solves the request; call out unrelated risks instead of folding them into the patch.
5. End with the exact verification commands or tests that should run next.

## Response Shape

- Current flow
- Smallest change
- Verification plan

## Guardrails

- Do not start editing until the relevant files and shared interfaces are named.
- If the task is still ambiguous, search and read more context instead of guessing.
- Keep repo discovery concise; avoid long rewrites when a short map of the codebase is enough.
