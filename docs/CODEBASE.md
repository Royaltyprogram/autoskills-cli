# Codebase Documentation

## Overview

This repository is a prototype for an `AI coding configuration operations platform`.

It has four runtime surfaces:

1. `Local CLI Agent`
   - registers a local device
   - uploads structured config snapshots and session summaries
   - pulls only approved change plans
   - applies or rolls back local configuration safely

2. `Cloud Research Agent`
   - lives in the server process for now
   - analyzes metrics and config snapshots
   - emits ranked recommendations with structured change plans
   - is currently an `OpenAI placeholder`, not a live API integration

3. `AIops Server`
   - exposes ingestion, review, execution, dashboard, and audit APIs
   - persists state to an on-disk JSON store
   - tracks rollout state and impact metrics

4. `Web Dashboard`
   - reviews recommendations
   - approves or rejects change plans
   - shows approved execution queue, impact, and audit history

## High-Level Architecture

```text
CLI agentopt
  -> /api/v1/devices/register
  -> /api/v1/projects/connect
  -> /api/v1/config-snapshots
  -> /api/v1/session-summaries
  -> /api/v1/recommendations/apply
  -> /api/v1/change-plans/review
  -> /api/v1/execution-queue
  -> /api/v1/executions/result

Server
  -> routes/controller/*
  -> service/analytics.go
  -> service/research_agent.go
  -> service/analytics_store.go
  -> data/agentopt-store.json

Dashboard
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
  - usage metrics and derived features, no raw transcript
- `Recommendation`
  - ranked proposal from the cloud research agent
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

The CLI acts as `collector + sync client + execution agent + rollback helper`.

### Routes

- [routes.go](/Users/doyechan/Desktop/codes/aiops/routes/routes.go)
- [analytics.go](/Users/doyechan/Desktop/codes/aiops/routes/controller/analytics.go)
- [web.go](/Users/doyechan/Desktop/codes/aiops/routes/controller/web.go)
- [dashboard.html](/Users/doyechan/Desktop/codes/aiops/routes/controller/assets/dashboard.html)

### Services

- [analytics.go](/Users/doyechan/Desktop/codes/aiops/service/analytics.go)
- [analytics_store.go](/Users/doyechan/Desktop/codes/aiops/service/analytics_store.go)
- [research_agent.go](/Users/doyechan/Desktop/codes/aiops/service/research_agent.go)

`AnalyticsService` owns the main product flow. `CloudResearchAgent` is a placeholder OpenAI-facing layer that currently runs deterministic rules.

### DTOs

- [analytics.go](/Users/doyechan/Desktop/codes/aiops/dto/request/analytics.go)
- [analytics.go](/Users/doyechan/Desktop/codes/aiops/dto/response/analytics.go)

## Current Product Flow

1. `login`
   - registers a device and consent scope
2. `connect`
   - connects a project
3. `snapshot` / `session`
   - uploads structured metrics only
4. `recommendations`
   - lists research-agent output
5. `apply`
   - creates a change plan in `awaiting_review`
6. `review`
   - approves or rejects the plan
7. `sync`
   - applies approved plans locally
8. `preflight`
   - validates a queued change plan against local guard rules before execution
9. `impact`
   - compares pre/post execution signals

## Persistence Model

State is stored in a single JSON file via [analytics_store.go](/Users/doyechan/Desktop/codes/aiops/service/analytics_store.go).

Persisted entities:

- organizations
- users
- devices
- projects
- config snapshots
- session summaries
- recommendations
- change plans / execution lifecycle records
- audits

## Notes

- API auth is still a shared token
- no raw code or transcript upload exists
- the OpenAI API integration is intentionally left as a placeholder in this branch
- the local CLI executor only applies allowlisted config files such as `AGENTS.md`, `.mcp.json`, `.codex/config.json`, and `.claude/settings.local.json`
- approved change plans may contain multiple local patch steps, and rollback restores them in reverse order
