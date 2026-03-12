# Codebase Documentation

## Overview

This repository is a prototype for an `AI coding configuration operations platform`.

It has four runtime surfaces:

1. `Local CLI Agent`
   - registers a local device
   - uploads config snapshots and session summaries
   - pulls only approved change plans
   - applies or rolls back local configuration safely

2. `Cloud Research Agent`
   - lives in the server process for now
   - samples uploaded raw query history and synthesizes workflow recommendations with the OpenAI Responses API
   - emits ranked harness-aware recommendations with structured change plans
   - stays disabled when the OpenAI API is not configured

3. `AIops Server`
   - exposes auth, ingestion, review, execution, dashboard, and audit APIs
   - persists runtime state to a database-backed analytics store
   - can import once from an older JSON store path during migration
   - tracks rollout state and token-based impact metrics

4. `Web Dashboard`
   - presents a user-facing approval and outcome view
   - approves or rejects change plans
   - shows rollout queue, workspace state, and measured impact with less low-level detail

## High-Level Architecture

```text
CLI agentopt
  -> /api/v1/agents/register
  -> /api/v1/projects/register
  -> /api/v1/config-snapshots
  -> /api/v1/session-summaries
  -> /api/v1/harness-runs
  -> /api/v1/recommendations/apply
  -> /api/v1/change-plans/review
  -> /api/v1/applies/pending
  -> /api/v1/applies/result

Server
  -> routes/controller/*
  -> service/analytics.go
  -> service/research_agent.go
  -> service/analytics_store.go
  -> DB.DSN
  -> optionally migrate from App.StorePath legacy JSON

Dashboard
  -> GET /
  -> GET /dashboard
  -> fetch /api/v1/*
```

## Main Objects

- `Device`
  - local CLI installation
- `Project`
  - repo or working environment connected to a device
- `Config Snapshot`
  - structured config state, fingerprints, MCP count, hooks, instruction files
- `Session Summary`
  - token usage and raw query history collected by the CLI
- `Recommendation`
  - ranked proposal from the cloud research agent, optionally with a structured `harness_spec`
- `Harness Run`
  - repo-local harness execution result uploaded by the CLI
  - feeds dashboard-level harness health metrics such as pass rate and latest failing spec
- `Change Plan`
  - structured and reviewable local patch plan
- `Execution Result`
  - apply, failure, or rollback outcome reported by the CLI
- `Impact Report`
  - before/after metrics around rollout

## Directory Layout

### Bootstrap

- [main.go](/Users/doyechan/Desktop/codes/aiops/main.go)
- [wire.go](/Users/doyechan/Desktop/codes/aiops/wire.go)
- [wire_gen.go](/Users/doyechan/Desktop/codes/aiops/wire_gen.go)
- [app/app.go](/Users/doyechan/Desktop/codes/aiops/app/app.go)

### CLI

- [main.go](/Users/doyechan/Desktop/codes/aiops/cmd/agentopt/main.go)
- [main_test.go](/Users/doyechan/Desktop/codes/aiops/cmd/agentopt/main_test.go)

The CLI acts as `collector + sync client + execution agent + rollback helper`, and the harness runner can upload repo-local regression results when the working tree is connected to AgentOpt.

### Routes

- [routes.go](/Users/doyechan/Desktop/codes/aiops/routes/routes.go)
- [analytics.go](/Users/doyechan/Desktop/codes/aiops/routes/controller/analytics.go)
- [web.go](/Users/doyechan/Desktop/codes/aiops/routes/controller/web.go)
- [landing.html](/Users/doyechan/Desktop/codes/aiops/routes/controller/assets/landing.html)
- [dashboard.html](/Users/doyechan/Desktop/codes/aiops/routes/controller/assets/dashboard.html)

### Services

- [analytics.go](/Users/doyechan/Desktop/codes/aiops/service/analytics.go)
- [analytics_store.go](/Users/doyechan/Desktop/codes/aiops/service/analytics_store.go)
- [research_agent.go](/Users/doyechan/Desktop/codes/aiops/service/research_agent.go)

`AnalyticsService` owns the main product flow. `CloudResearchAgent` samples uploaded raw queries, asks the OpenAI Responses API for harness-first workflow recommendations, and localizes the returned change plans into repo-local agent files.

### DTOs

- [analytics.go](/Users/doyechan/Desktop/codes/aiops/dto/request/analytics.go)
- [analytics.go](/Users/doyechan/Desktop/codes/aiops/dto/response/analytics.go)

## Current Product Flow

1. `login`
   - authenticates a local CLI install with a dashboard-issued CLI token
2. Web login
   - signs in at `/`
   - opens `/dashboard`
   - issues a scoped CLI token from the dashboard
3. `connect`
   - connects the local repo as its own project inside the org
   - reconnecting the same repo reuses the existing project record instead of merging all repos together
4. `projects`
   - shows the connected project records for the current org
5. `snapshot` / `session`
   - uploads config snapshots plus token usage and raw query history
   - `session` can auto-collect the latest local Codex session JSONL under `~/.codex/sessions`
   - `session --recent N` uploads the most recent `N` local Codex sessions in chronological order
6. `recommendations`
   - lists research-agent output
7. `apply`
   - creates a change plan in `awaiting_review`
   - low-risk single-file config merges may be auto-approved by policy
   - when execution starts, the Go CLI now hands the approved local patch plan to a Codex SDK runner
   - if repo-local harness specs exist, they run before and after the apply; a red post-check triggers an automatic rollback attempt
8. `review`
   - approves or rejects the plan
9. `sync`
   - applies approved plans locally from the active project's queue
   - uses the same pre/post harness gate as `apply --yes`
10. `preflight`
   - validates a queued change plan against local guard rules before execution
11. `impact`
   - compares pre/post execution signals
12. `harness run`
   - discovers repo-local AgentOpt harness specs under `.agentopt/harness/`
   - executes allowlisted validation commands such as `go test`, reports pass/fail, and uploads the result when the repo is connected

## Persistence Model

State is stored in a single JSON file via [analytics_store.go](/Users/doyechan/Desktop/codes/aiops/service/analytics_store.go).

Persisted entities:

- organizations
- users
- access tokens
- devices
- projects
- config snapshots
- session summaries
- recommendations
- change plans / execution lifecycle records
- audits

## Notes

- API auth is still a shared token
- raw query history is uploaded for recommendation analysis, but no raw code is collected
- live web search and external research integration are intentionally deferred in this branch
- the local CLI executor only applies allowlisted config files such as `AGENTS.md`, `.codex/skills/agentopt-*/SKILL.md`, `.agentopt/harness/*.json`, `.mcp.json`, `.codex/config.json`, and `.claude/settings.local.json`
- the actual file-edit execution step is delegated to `tools/codex-runner/run.mjs`, which wraps the official Codex SDK
- Go still owns preflight, file allowlist enforcement, backup, rollback, and apply-result reporting
- approved change plans may contain multiple local patch steps, and rollback restores them in reverse order
