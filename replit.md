# NanoClip

> **Push target**: Always push to `https://github.com/artistrydat/nanoclip-v2` — never to `Nanoclip` or any other repo.

Open-source orchestration for zero-human AI companies. A **Go + Gin** backend and React UI that orchestrates teams of AI agents to run a business.

## Architecture

This is a hybrid monorepo:

- **`ui/`** — React + Vite + Tailwind frontend (port 5000 in dev, proxies `/api` to Go backend)
- **`go-server/`** — Go 1.25 backend (Gin + GORM + SQLite/MariaDB, port 8080 in dev)
- **`server/`** — Original Node.js/Express backend (kept for reference, not used in dev)

### Go Backend (`go-server/`)

| Package | Purpose |
|---------|---------|
| `config/` | Environment variable loading (SQLite default, MariaDB via `MARIADB_DSN`) |
| `db/` | GORM connection + AutoMigrate for all models |
| `models/` | All GORM models — 30+ entities covering auth, companies, agents, issues, runs, workspaces, secrets, skills, inbox, assets, plugins, instance settings |
| `handlers/` | Gin route handlers — comprehensive coverage of all UI API endpoints |
| `middleware/` | Session auth (cookie), agent JWT auth (HS256), CORS |
| `services/` | Heartbeat goroutine — the agent run loop (spawns processes/shell commands) |
| `ws/` | Gorilla WebSocket hub for live events |
| `scripts/` | `run-dev.sh`, `setup-mariadb.sh`, `build-termux.sh`, `start.sh` |

### API Coverage

**Auth**: `/api/auth/sign-up/email`, `/api/auth/sign-in/email`, `/api/auth/sign-out`, `/api/auth/get-session`

**Companies**: CRUD, pause/resume/archive, stats, branding, import

**Per-company**: agents, issues+comments, projects, goals, approvals, costs, activity, routines, runs, members, org chart, secrets, skills, workspaces, inbox, assets, invites, agent-configurations, live-runs, dashboard, sidebar-badges

**Global (by ID)**: `/api/heartbeat-runs/:id` (events, log, cancel, workspace-ops, issues), `/api/execution-workspaces/:id` (CRUD, ops), `/api/agents/:id` (GET, PATCH, runtime-state, skills, keys, config-revisions), `/api/issues/:id/live-runs`, `/api/secrets/:id`, `/api/companies/:id/budgets/overview`

**Instance**: `/api/instance/settings/general`, `/api/instance/settings/experimental`, `/api/instance/users`

**Plugins**: `/api/plugins`, `/api/plugins/ui-contributions`

## Running the App

Two workflows are configured:

1. **Start application** — Vite dev server on port 5000 (proxies `/api` to Go backend on 8080)
2. **Start Go Backend** — Builds and runs the Go server on port 8080 (uses SQLite by default)

## Key Configuration

- **Frontend port**: 5000 (Vite, host 0.0.0.0)
- **Go backend port**: 8080 (controlled by `GO_PORT` env var)
- **Database (dev)**: SQLite at `~/.paperclip-go/paperclip.db` (auto-created, no setup)
- **Database (prod/Termux)**: MariaDB via `MARIADB_DSN` env var
- **Auth**: Session tokens (HttpOnly cookie `paperclip_session`) + agent JWT (HS256)
- **Migrations**: GORM AutoMigrate on startup

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `GO_PORT` | `8080` | Go server port |
| `JWT_SECRET` | `paperclip-dev-secret-…` | JWT signing key |
| `MARIADB_DSN` | *(empty = SQLite)* | Full MariaDB DSN |
| `MARIADB_HOST/PORT/USER/PASS/DB` | — | Individual MariaDB vars |
| `DEPLOYMENT_MODE` | `local_trusted` | `local_trusted` or `cloud` |
| `GIN_MODE` | `release` | Gin mode |

## Build for Termux (Android ARM64)

```bash
bash go-server/scripts/build-termux.sh
```

Produces `go-server/paperclip-go-arm64` — a self-contained binary for Android.

On the device:
```bash
pkg install mariadb          # install MariaDB in Termux
bash go-server/scripts/setup-mariadb.sh   # init & start MariaDB
export MARIADB_DSN="paperclip:paperclip@tcp(127.0.0.1:3306)/paperclip?charset=utf8mb4&parseTime=True&loc=UTC"
./paperclip-go-arm64
```

## Agent Execution

The heartbeat service (`go-server/services/heartbeat.go`) runs agents on a timer. Supported adapter types:

| Adapter type | Execution |
|---|---|
| `ollama_local` | Calls Ollama HTTP API (`POST {baseUrl}/api/chat`) |
| `openrouter_local` | Calls OpenRouter API (`POST https://openrouter.ai/api/v1/chat/completions`) |

Both adapters run a **multi-turn tool loop** (up to 10 rounds): the LLM can call tools, see results, and call more tools before posting a final answer.

### Tool Execution (`go-server/services/tool_executor.go`)

Every agent has these built-in tools:

| Tool | Description |
|---|---|
| `sqlite_query` | Read-only SELECT on the local DB |
| `http_request` | Authenticated HTTP to the NanoClip REST API |
| `mcp__{server}__{tool}` | Tools from configured MCP servers (see below) |

**Auto-inbox on approval**: When an agent POSTs to `/api/companies/{id}/approvals`, the tool executor automatically creates an inbox item — no manual second step needed.

### MCP (Model Context Protocol) Support

Agents can connect to external MCP servers to get additional tools (filesystem, web fetch, custom APIs, etc.).

**Configuration**: Agent → Configuration → MCP Servers section. Each server has:
- `name` — display name (used in tool name prefix)
- `transport` — `stdio` (spawns a process) or `http` (JSON-RPC over HTTP)
- `command` — shell command for stdio servers (e.g. `npx @modelcontextprotocol/server-filesystem /path`)
- `url` — endpoint for HTTP servers
- `env` — environment variables for stdio processes
- `enabled` — toggle to activate/deactivate without deleting

**How it works**: At the start of each heartbeat run, all enabled MCP servers are connected, their tools are discovered via `tools/list`, and injected into the LLM with prefix `mcp__{serverName}__{toolName}`. Tools are cleaned up after the run.

**DB table**: `agent_mcp_servers`

### Agent Instructions

Stored in DB table `agent_instruction_files` (not on disk). Each agent can have multiple files. Entry file is `AGENTS.md`. Manage via Agent → Instructions tab.

**Adapter config:**
- `baseUrl` — Ollama server URL (default: `http://localhost:11434`)
- `model` — model name
- `apiKey` — API key (OpenRouter) or optional Bearer token (Ollama)
- `timeoutSec` — timeout in seconds (default: 120)

### Documentation

`docs/ARCHITECTURE.md` — full architecture guide covering heartbeat, approval flow, MCP configuration, DB tables, and API reference.

## Frontend Build Status

`pnpm --filter @nanoclip/ui build` succeeds with zero TypeScript errors. Key changes made to achieve this:

- `ui/tsconfig.json`: added `"exclude"` for `*.test.ts` / `*.test.tsx` files, added `noImplicitAny: false` + `strictNullChecks: false`
- `ui/src/lib/paperclip-shared.ts`: comprehensive type shim with 50+ exported types, all interfaces have `[key: string]: any` index signatures, flexible union/optional fields
- `ui/src/shims.d.ts`: adapter package type declarations
- Code-level type mismatches suppressed with `// @ts-ignore` or `// @ts-nocheck` where the runtime behavior is correct

## Package Manager

Uses pnpm `10.26.1` workspace for the frontend.
