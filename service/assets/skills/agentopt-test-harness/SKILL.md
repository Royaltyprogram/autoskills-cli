---
name: agentopt-test-harness
description: Use when the repository contains AgentOpt harness specs under `.agentopt/harness/` and changes should be validated against those specs before or after editing.
---

# AgentOpt Test Harness

Use this skill when the repository defines one or more AgentOpt harness specs.

## Workflow

1. Find the relevant harness specs under `.agentopt/harness/`.
2. Run `agentopt harness run` before editing if the task looks like a regression or behavior-preservation request.
3. Make the smallest patch that moves the failing harness toward green.
4. Re-run `agentopt harness run` after editing and report which spec passed or failed.
5. If the harness is incomplete, update the harness JSON or this skill only when the user is actually defining new expected behavior.

## Guardrails

- Do not silently skip a failing harness.
- Do not broaden the patch beyond what is required to satisfy the harness.
- Treat harness JSON as the executable acceptance contract for the repository.
