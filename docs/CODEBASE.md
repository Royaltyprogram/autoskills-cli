# Codebase Documentation

## Overview

This repository is a prototype for an AI workflow feedback platform for coding agents.

It has four runtime surfaces:

1. `Local CLI`
   - registers a local device
   - connects a repo into the org's shared workspace
   - uploads config snapshots and session summaries
   - can keep collecting in watch mode by watching Codex session files and advancing a saved upload cursor

2. `Cloud Research Agent`
   - runs inside the server process
   - analyzes uploaded prompts, assistant responses, tool traces, and usage metrics
   - generates ranked feedback reports for the user

3. `AIops Server`
   - exposes auth, ingestion, dashboard, report, workspace, and audit APIs
   - persists runtime state to the analytics store
   - tracks background report-research status per workspace

4. `Web Dashboard`
   - shows report-oriented feedback instead of approval queues
   - helps the user inspect workflow friction, usage trends, and report history

## High-Level Architecture

```text
CLI crux
  -> /api/v1/auth/cli/login
  -> /api/v1/agents/register
  -> /api/v1/projects/register
  -> /api/v1/config-snapshots
  -> /api/v1/session-summaries
  -> /api/v1/reports
  -> /api/v1/dashboard/*
  -> /api/v1/audits

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
  - one authenticated CLI installation
- `Project`
  - the org-level shared workspace currently connected from a device
- `Config Snapshot`
  - structured config state, fingerprints, MCP count, hooks, and instruction files
- `Session Summary`
  - token usage, raw queries, assistant responses, reasoning summaries, tool usage, and timing
- `Report`
  - a user-facing workflow feedback report synthesized by the research agent, including user intent and how the model appeared to interpret the request
- `Report Research Status`
  - background state for the current analysis pass
- `Audit Event`
  - notable auth, ingestion, and workspace lifecycle events

## Directory Layout

### Bootstrap

- [main.go](/Users/doyechan/Desktop/codes/aiops/main.go)
- [wire.go](/Users/doyechan/Desktop/codes/aiops/wire.go)
- [wire_gen.go](/Users/doyechan/Desktop/codes/aiops/wire_gen.go)
- [app/app.go](/Users/doyechan/Desktop/codes/aiops/app/app.go)

### CLI

- [main.go](/Users/doyechan/Desktop/codes/aiops/cmd/crux/main.go)
- [collect.go](/Users/doyechan/Desktop/codes/aiops/cmd/crux/collect.go)
- [codex_collect.go](/Users/doyechan/Desktop/codes/aiops/cmd/crux/codex_collect.go)

The CLI is now a collector and workspace client only. It does not apply config changes locally.

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

`AnalyticsService` owns auth, ingestion, dashboard aggregation, and report refresh scheduling. `CloudResearchAgent` calls the OpenAI Responses API and returns structured report items.

### DTOs

- [analytics.go](/Users/doyechan/Desktop/codes/aiops/dto/request/analytics.go)
- [analytics.go](/Users/doyechan/Desktop/codes/aiops/dto/response/analytics.go)

## Current Product Flow

1. Dashboard login
   - signs in at `/`
   - opens `/dashboard`
   - issues a scoped CLI token
2. `crux setup`
   - authenticates a local CLI install with the issued token
   - connects the local repo to the org's shared workspace
   - uploads an initial snapshot plus local Codex session history on first setup, then resumes from the saved session cursor
3. `crux snapshot` / `crux session` / `crux collect`
   - uploads config snapshots plus usage sessions
   - `session` and `collect` can auto-read recent Codex session JSONL files
4. Report refresh
   - starts after enough sessions and raw-query evidence exist
   - runs asynchronously on the server
6. `crux reports` / dashboard overview
   - shows report-style feedback, strengths, frictions, and next steps
7. Ongoing usage uploads
   - `collect --watch` reacts to session file changes, uses the interval as a fallback scan, and uploads every new logical session after the saved cursor

## Persistence Model

Runtime state is stored in the database-backed analytics store managed by [analytics_store.go](/Users/doyechan/Desktop/codes/aiops/service/analytics_store.go).

Persisted entities:

- organizations
- users
- access tokens
- devices
- projects
- config snapshots
- session summaries
- reports
- report research status
- audits

## Notes

- No Codex SDK runner or local patch-application queue remains in the current product shape.
- Reports are observational feedback reports, not patch plans.
- Raw query history is uploaded for report generation, but raw source code is not collected.
- Live web search and external repo indexing are still out of scope in this branch.
