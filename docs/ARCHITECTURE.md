# NanoClip Architecture Guide

This document covers the key architectural concepts of NanoClip: the heartbeat service, agent tool execution, the approval flow, MCP server configuration, and how they all connect.

---

## Overview

NanoClip is a **local-first AI agent orchestration platform**. Agents run on your device (PC, server, or Termux on Android), connect to local or remote LLMs, and autonomously work on issues. There is no cloud dependency — all data stays on device.

```
User / Telegram
     │
     ▼
 NanoClip UI (React)  ──►  Go HTTP API  ──►  SQLite / MariaDB
                                │
                          Heartbeat Service
                                │
                    ┌───────────┼───────────┐
                    ▼           ▼           ▼
               Ollama LLM  OpenRouter  (future LLMs)
                    │
               Tool Executor
                    │
           ┌────────┼────────┐
           ▼        ▼        ▼
      sqlite_query  http_request  mcp__{server}__{tool}
```

---

## Heartbeat Service

**File:** `go-server/services/heartbeat.go`

The heartbeat service is the autonomous agent loop. It runs as a background goroutine and drives every agent heartbeat.

### Flow

1. **Tick** — A goroutine ticks every 30 seconds (configurable). For each non-paused agent, it checks whether a wakeup request is pending or enough time has passed since the last run.

2. **Issue Selection** — The service picks one open issue (`backlog` or `in_progress`) assigned to the agent. If no issue is found, the agent goes idle.

3. **Prompt Building** — `buildSystemPrompt` loads instruction files from the DB (entry file first, then others by sort order), then appends company skills. `buildIssueUserMessage` formats the issue context.

4. **LLM Call** — Depending on the agent's `adapterType`, either `runOllamaAgent` or `runOpenRouterAgent` is called.

5. **Tool Loop** — The LLM may request tool calls (up to `maxToolRounds = 10` rounds). Each round:
   - Execute all requested tool calls via `agentToolExecutor`
   - Append tool results to the conversation
   - Send the updated conversation back to the LLM
   - Stop when the LLM produces a text response with no further tool calls

6. **Result** — The final text response is posted as a comment on the issue.

### Key files

| File | Purpose |
|---|---|
| `heartbeat.go` | Main loop, LLM dispatch, conversation management |
| `tool_executor.go` | Built-in tools + MCP routing |
| `mcp_client.go` | MCP stdio and HTTP clients |

---

## Tool Execution

**File:** `go-server/services/tool_executor.go`

### Built-in tools

Every agent has two built-in tools:

| Tool | Description |
|---|---|
| `sqlite_query` | Read-only SELECT queries against the local DB |
| `http_request` | Authenticated HTTP calls to the NanoClip REST API |

The `http_request` tool automatically includes the agent's JWT (`Authorization: Bearer ...`) on every call.

### MCP tools

Additional tools are loaded from configured MCP servers at the start of each heartbeat run. See the MCP section below for details.

### Tool naming

MCP tools are prefixed to avoid collisions with built-in tools:

```
mcp__{normalized-server-name}__{original-tool-name}
```

---

## Approval Flow

When an agent wants to do something that requires human approval (e.g., hire a new agent), it creates an approval request.

### Steps

1. **Agent creates approval** via `http_request POST /api/companies/{companyId}/approvals`
   - The `requestedByAgentId` is set automatically from the agent JWT
   - The approval is stored with `status = "pending"`

2. **Auto inbox notification** — The tool executor detects the approval POST and automatically creates an inbox item with `kind = "approval_request"`. The agent does NOT need to manually post to `/inbox`.

3. **Human reviews** — The approval appears in the Inbox UI and (if the Telegram plugin is enabled) as a Telegram message with ✅/❌ inline buttons.

4. **Decision** — The human (or Telegram button) PATCHes the approval to `approved` or `rejected`.

5. **Agent sees result** — On its next heartbeat, the agent can query `approvals` to see the decision.

### Database tables

| Table | Purpose |
|---|---|
| `approvals` | Approval records with type, payload, status |
| `approval_comments` | Notes added to approvals |
| `inbox_items` | Inbox notifications including approval requests |

---

## MCP (Model Context Protocol)

MCP is a protocol that lets LLMs call external tools over JSON-RPC. NanoClip supports connecting agents to MCP servers, which can provide any capability (filesystem access, web search, databases, APIs, etc.).

### Configuration

MCP servers are configured per agent in the database (`agent_mcp_servers` table). You can manage them in the UI under **Agent → Configuration → MCP Servers**.

| Field | Type | Description |
|---|---|---|
| `name` | string | Display name (used in tool prefix) |
| `transport` | `"stdio"` or `"http"` | How to connect |
| `command` | string | (stdio only) Shell command to start the server |
| `url` | string | (http only) JSON-RPC endpoint URL |
| `env` | JSON object | Environment variables for stdio processes |
| `enabled` | bool | Whether to load tools from this server |

### stdio transport

The server process is started fresh for each agent heartbeat run using `sh -c <command>`. The MCP JSON-RPC protocol is used over stdin/stdout. The process is killed when the run completes.

This is the most common transport and works with any MCP server distributed via npm (`npx`), pip (`uvx`), or a local binary.

**Example — filesystem access:**
```
Command: npx @modelcontextprotocol/server-filesystem /sdcard/docs
```

**Low memory note (Termux):** Each stdio MCP server spawns one child process per heartbeat run. If you are running on a low-memory device, keep the number of enabled stdio MCP servers small (1–2) or prefer HTTP transport when possible.

### HTTP transport

The server runs independently and exposes a JSON-RPC HTTP endpoint. NanoClip POSTs JSON-RPC calls to the URL. No process is spawned.

**Example:**
```
URL: http://localhost:3001/mcp
```

### Tool discovery and injection

At the start of each heartbeat run:

1. All enabled MCP servers for the agent are queried from DB
2. For each server: connect → `initialize` → `tools/list`
3. Discovered tools are prefixed and added to the LLM's tool list
4. If a server fails to connect, its tools are skipped (logged)

Tools are available for the entire run and disconnected/cleaned up when the run finishes.

### Tool naming

```
mcp__{normalized-server-name}__{original-tool-name}
```

Server name normalization: lowercase, spaces/hyphens/dots → underscores.

Example: server `"Filesystem"` + tool `"read_file"` → `mcp__filesystem__read_file`

---

## Database Tables

NanoClip supports both SQLite (default, zero-config) and MariaDB (for multi-device or higher concurrency).

### Key tables

| Table | Description |
|---|---|
| `companies` | Multi-tenant company records |
| `agents` | Agent definitions (name, adapter, capabilities, permissions) |
| `agent_instruction_files` | Instruction files per agent (replaces on-disk files) |
| `agent_mcp_servers` | MCP server configurations per agent |
| `issues` | Work items / tasks |
| `issue_comments` | Comments on issues (including LLM run results) |
| `heartbeat_runs` | Run metadata (start, end, status, model used) |
| `heartbeat_run_events` | Step-by-step events within a run |
| `approvals` | Human-approval requests from agents |
| `inbox_items` | Unified inbox (approval_request, escalation, etc.) |
| `company_skills` | Embedded skill documents available to agents |
| `plugins` | Plugin configurations (Telegram, etc.) |
| `cost_events` | Token usage and cost tracking |

---

## Agent Instructions

Instructions are stored in the `agent_instruction_files` table. Each agent can have multiple files (e.g., `AGENTS.md`, `TOOLS.md`, `PROTOCOLS.md`). One file is marked as the entry file (`is_entry_file = true`).

When building the system prompt:
1. Entry file content is placed first
2. Other files are appended in sort order
3. Company skills the agent has requested are appended last

The entry file defaults to `AGENTS.md` and is auto-created with placeholder content the first time an agent's Instructions tab is opened.

---

## Telegram Plugin

**File:** `go-server/services/telegram.go`

An optional plugin that bridges NanoClip to Telegram. Uses long-polling (no public URL required).

Features:
- Sends notifications for new issues, approvals, and escalations
- Interactive approval buttons (✅/❌) via inline keyboards
- Commands: `/status`, `/issues`, `/agents`, `/approve <id>`
- Inbound message routing back to issues as comments

Configure it at **Company → Plugins → Telegram Bot**.

---

## Running on Termux (Android)

NanoClip is designed to be resource-efficient for Termux on older Android phones:

- SQLite by default — no separate DB server needed
- Go binary compiles to a single small executable
- Heartbeat ticks lazily (only runs when issues exist)
- MCP stdio servers are started on-demand and killed after each run
- Use HTTP MCP transport for persistent servers to avoid spawning processes

**Build for Termux:**
```bash
GOOS=linux GOARCH=arm64 go build -o nanoclip-server ./go-server
```

Or use the provided `go-server/scripts/run-dev.sh` directly on device.

---

## API Reference

All API endpoints are served at `http://127.0.0.1:8080/api`.

### Agent endpoints (global, no company scope)

```
GET    /api/agents/:agentId
PATCH  /api/agents/:agentId
GET    /api/agents/:agentId/mcp-servers
POST   /api/agents/:agentId/mcp-servers
PATCH  /api/agents/:agentId/mcp-servers/:serverId
DELETE /api/agents/:agentId/mcp-servers/:serverId
GET    /api/agents/:agentId/instructions-bundle
PUT    /api/agents/:agentId/instructions-bundle/file
DELETE /api/agents/:agentId/instructions-bundle/file
POST   /api/agents/:agentId/wakeup
POST   /api/agents/:agentId/pause
POST   /api/agents/:agentId/resume
```

Agents can be referenced by full UUID or slug (`name-shortid`).

### Company-scoped endpoints

```
GET    /api/companies/:companyId/agents
POST   /api/companies/:companyId/agents/hire
GET    /api/companies/:companyId/issues
POST   /api/companies/:companyId/issues
PATCH  /api/companies/:companyId/issues/:issueId
POST   /api/companies/:companyId/issues/:issueId/comments
GET    /api/companies/:companyId/approvals
POST   /api/companies/:companyId/approvals
PATCH  /api/companies/:companyId/approvals/:approvalId
GET    /api/companies/:companyId/inbox
POST   /api/companies/:companyId/inbox
```

---

*Last updated: 2025 — see git history for changes.*
