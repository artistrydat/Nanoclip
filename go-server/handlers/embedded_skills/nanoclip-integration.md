# Skill: NanoClip Integration — REST API

This skill teaches NanoClip agents how to interact with the NanoClip REST API and resolve all required context (company ID, agent IDs, project IDs) autonomously from the local database before making any API call.

---

## Your Available Tools

You have built-in tools plus any MCP tools configured for your agent:

| Tool | Purpose |
|---|---|
| `sqlite_query` | Run a SQL SELECT query against the local NanoClip database to look up any IDs or data |
| `http_request` | Make an HTTP request to the NanoClip API (GET/POST/PATCH/DELETE) to create or update resources |
| `mcp__{server}__{tool}` | Tools discovered from MCP servers configured for your agent (see MCP section below) |

**Always use `sqlite_query` first** to resolve any IDs you need (company ID, agent IDs, project IDs). Then use `http_request` to take action. Never ask the user for IDs you can look up yourself.

---

## IMPORTANT: Always Resolve Context First

**Never ask the user for IDs.** All UUIDs you need (company ID, agent IDs, project IDs) are already in the local database. Query them first, then act.

Your current issue already contains the `company_id` — use it directly. If you need other IDs (agents, projects), query the database as shown below.

---

## Base URL

All API requests go to the NanoClip backend running on the same device:

```
http://127.0.0.1:8080/api
```

Scoped company routes:

```
http://127.0.0.1:8080/api/companies/{companyId}/...
```

---

## Authentication

Include the agent JWT in the `Authorization` header:

```
Authorization: Bearer <agent-jwt>
```

Agents retrieve their JWT from:

```
GET http://127.0.0.1:8080/api/companies/{companyId}/agents/{agentId}/jwt
```

---

## Self-Discovery — Resolve All Context Before Acting

Before calling any API, query the local SQLite database to resolve all required values.

### Find the company ID and prefix

```sql
SELECT id, name, issue_prefix FROM companies LIMIT 5;
```

### List all agents in the company (for `reportsTo`, assignees, etc.)

```sql
SELECT id, name, title, status, adapter_type, reports_to
FROM agents
WHERE company_id = '<company-id>'
ORDER BY name;
```

### Find a specific agent by name

```sql
SELECT id, name, title
FROM agents
WHERE company_id = '<company-id>'
  AND LOWER(name) LIKE '%manager%';
```

### List all projects

```sql
SELECT id, name, slug
FROM projects
WHERE company_id = '<company-id>';
```

### Find your own agent record (to get your company_id, agent_id)

The issue you are working on includes `company_id` — extract it from there. Your own agent ID is the one set as `assignee_agent_id` on the current issue:

```sql
SELECT id, name, company_id
FROM agents
WHERE id = (
  SELECT assignee_agent_id FROM issues WHERE id = '<current-issue-id>'
);
```

### Check existing skills for the company

```sql
SELECT id, name, kind FROM company_skills WHERE company_id = '<company-id>';
```

---

## SQLite Access (Local / Replit Environments)

When NanoClip is using SQLite (default for local/Replit), query the database with:

```bash
sqlite3 ~/.nanoclip/nanoclip.db "SELECT id, name FROM companies;"
```

Or for interactive use:

```bash
sqlite3 ~/.nanoclip/nanoclip.db
```

---

## Issues

### List open issues

```
GET http://127.0.0.1:8080/api/companies/{companyId}/issues?status=backlog,in_progress
```

### Create an issue

```
POST http://127.0.0.1:8080/api/companies/{companyId}/issues
Content-Type: application/json

{
  "title": "Task title",
  "description": "Details about the task",
  "priority": "medium",
  "projectId": "<project-uuid>",
  "assigneeAgentId": "<agent-uuid>"
}
```

### Update an issue

```
PATCH http://127.0.0.1:8080/api/companies/{companyId}/issues/{issueId}
Content-Type: application/json

{
  "status": "in_progress",
  "priority": "high"
}
```

### Add a comment to an issue

```
POST http://127.0.0.1:8080/api/companies/{companyId}/issues/{issueId}/comments
Content-Type: application/json

{ "body": "Comment text here" }
```

---

## Agents

### List all agents

```
GET http://127.0.0.1:8080/api/companies/{companyId}/agents
```

### Hire (create) a new agent

Before calling this, always resolve:
- `companyId` — from your current issue or via DB query above
- `reportsTo` — query agents table to find the manager's UUID; use `null` if top-level

```
POST http://127.0.0.1:8080/api/companies/{companyId}/agents/hire
Authorization: Bearer <agent-jwt>
Content-Type: application/json

{
  "name": "Web Surfer",
  "title": "Internet Research Specialist",
  "role": "research",
  "reportsTo": "<manager-agent-uuid-or-null>",
  "adapterType": "openrouter_local"
}
```

Valid `adapterType` values: `ollama_local`, `openrouter_local`, `none`

### Update agent identity

```
PATCH http://127.0.0.1:8080/api/companies/{companyId}/agents/{agentId}
Content-Type: application/json

{
  "name": "Updated Name",
  "title": "Updated Title",
  "capabilities": "What this agent can do"
}
```

### Get org chart

```
GET http://127.0.0.1:8080/api/companies/{companyId}/org
```

---

## Approvals

### List pending approvals

```
GET http://127.0.0.1:8080/api/companies/{companyId}/approvals?status=pending
```

### Create an approval request

Create the approval — an inbox notification is sent to humans automatically:

```
http_request POST /api/companies/{companyId}/approvals
{
  "type": "agent_creation",
  "payload": {
    "name": "Web Surfer",
    "role": "Browses the internet for research tasks",
    "reason": "The team needs an agent for autonomous web research"
  }
}
```

The system automatically creates an inbox item with `kind="approval_request"` when you POST to `/approvals`. You do NOT need to manually post to `/inbox` for approvals — it is handled for you.

### Decide on an approval

```
http_request PATCH /api/companies/{companyId}/approvals/{approvalId}
{ "status": "approved", "decisionNote": "Looks good" }
```

---

## Heartbeat Runs

### List recent runs for an agent

```
GET http://127.0.0.1:8080/api/companies/{companyId}/agents/{agentId}/runs
```

---

## Status Reference

| Resource | Valid Values |
|---|---|
| Issue status | `backlog`, `todo`, `in_progress`, `in_review`, `blocked`, `done`, `cancelled` |
| Issue priority | `urgent`, `high`, `medium`, `low` |
| Agent status | `idle`, `running`, `paused`, `terminated` |
| Approval status | `pending`, `approved`, `rejected` |
| Adapter type | `ollama_local`, `openrouter_local`, `none` |

---

## Response Codes

| Code | Meaning |
|---|---|
| 200 | Success |
| 201 | Created |
| 400 | Bad request — check the request body |
| 401 | Unauthorized — check your JWT |
| 403 | Forbidden — insufficient permissions |
| 404 | Not found |
| 500 | Server error |

---

## MCP Tools (Model Context Protocol)

MCP servers extend your capabilities by adding external tools. If any MCP servers are configured and enabled for your agent, their tools are automatically available in every heartbeat run — you can call them just like built-in tools.

### Tool naming convention

MCP tools are prefixed to avoid name collisions:

```
mcp__{serverName}__{toolName}
```

For example, if a server called "Filesystem" provides a `read_file` tool, it appears as:

```
mcp__filesystem__read_file
```

### Calling an MCP tool

Call it exactly like any other tool in the conversation:

```
Tool: mcp__filesystem__read_file
Arguments: { "path": "/sdcard/docs/report.txt" }
```

### When tools are loaded

MCP tools are discovered at the start of each heartbeat run. If a server is offline or fails to connect, its tools are silently skipped for that run (logged server-side). Enabling/disabling servers takes effect on the next heartbeat.

### Common MCP servers

| Server | Transport | Example command |
|---|---|---|
| `@modelcontextprotocol/server-filesystem` | stdio | `npx @modelcontextprotocol/server-filesystem /data` |
| `@modelcontextprotocol/server-fetch` | stdio | `npx @modelcontextprotocol/server-fetch` |
| Custom HTTP server | http | URL: `http://localhost:3001/mcp` |

---

## Best Practices

- **Always resolve IDs from the database first.** Do not ask the user for company IDs, agent UUIDs, or project IDs — look them up.
- Your issue's `company_id` is already provided in your task context — use it directly.
- Use `PATCH` (not `PUT`) for partial updates — only send fields you want to change.
- When hiring an agent, query existing agents first to find the right `reportsTo` UUID. If no manager is needed, pass `null`.
- Create an approval request before performing sensitive actions like creating agents or modifying permissions.
