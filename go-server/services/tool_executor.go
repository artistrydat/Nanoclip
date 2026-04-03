package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"paperclip-go/middleware"
	"paperclip-go/models"
)

const maxToolRounds = 10

// ToolDefinition is the OpenAI-compatible tool schema sent to the LLM.
type ToolDefinition struct {
	Type     string          `json:"type"`
	Function toolFunctionDef `json:"function"`
}

type toolFunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// LLMToolCall represents a single tool call from an LLM response.
type LLMToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// agentToolExecutor executes tool calls on behalf of an agent.
type agentToolExecutor struct {
	db       *gorm.DB
	agent    models.Agent
	agentJWT string
	mcpRefs  map[string]mcpToolRef // prefixed tool name → client+originalName
	mcpTools []ToolDefinition      // tool defs from MCP servers
}

// newAgentToolExecutor creates an executor, loads MCP tools from all enabled
// servers for the agent, and pre-generates the agent JWT.
func newAgentToolExecutor(db *gorm.DB, agent models.Agent) *agentToolExecutor {
	jwt, _ := middleware.IssueAgentJWT(agent.ID, agent.CompanyID)
	mcpTools, mcpRefs := loadAgentMCPTools(db, agent.ID)
	return &agentToolExecutor{
		db:       db,
		agent:    agent,
		agentJWT: jwt,
		mcpRefs:  mcpRefs,
		mcpTools: mcpTools,
	}
}

// close shuts down all MCP server connections (kills stdio processes, etc.).
func (e *agentToolExecutor) close() {
	seen := map[MCPClient]bool{}
	for _, ref := range e.mcpRefs {
		if !seen[ref.client] {
			ref.client.Close()
			seen[ref.client] = true
		}
	}
}

// allTools returns the built-in tools plus any tools from MCP servers.
func (e *agentToolExecutor) allTools() []ToolDefinition {
	base := builtinToolDefinitions()
	return append(base, e.mcpTools...)
}

// builtinToolDefinitions returns the always-available tools in OpenAI format.
func builtinToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Type: "function",
			Function: toolFunctionDef{
				Name: "sqlite_query",
				Description: "Execute a read-only SQL SELECT query against the local NanoClip SQLite database. " +
					"Use this FIRST to look up company IDs, agent IDs, project IDs, existing agents (for reportsTo), " +
					"issue details, and any other data you need before making API calls. Never ask the user for IDs.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{
							"type":        "string",
							"description": "A valid SQL SELECT query (read-only). Use LIMIT to keep results manageable.",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: toolFunctionDef{
				Name: "http_request",
				Description: "Make an HTTP request to the local NanoClip REST API at http://127.0.0.1:8080. " +
					"Use GET to read data, POST to create resources (agents, approvals, issues, comments, inbox items), " +
					"PATCH to update. " +
					"NOTE: When you POST to /api/companies/{companyId}/approvals, an inbox notification is created " +
					"automatically — you do NOT need to separately POST to /api/companies/{companyId}/inbox. " +
					"Only post to inbox manually for other significant events requiring human attention.",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"method": map[string]interface{}{
							"type": "string",
							"enum": []string{"GET", "POST", "PATCH", "DELETE"},
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Full API path starting with /api, e.g. /api/companies/abc/agents/hire",
						},
						"body": map[string]interface{}{
							"type":                 "object",
							"description":          "JSON body for POST/PATCH requests",
							"additionalProperties": true,
						},
					},
					"required": []string{"method", "path"},
				},
			},
		},
	}
}

// toolDefinitions is kept as a package-level alias for backward compatibility.
func toolDefinitions() []ToolDefinition {
	return builtinToolDefinitions()
}

// approvalPathRe matches POST paths that create an approval and captures companyId.
var approvalPathRe = regexp.MustCompile(`^/api/companies/([^/]+)/approvals$`)

// execute dispatches a tool call by name and returns the result as a string.
func (e *agentToolExecutor) execute(name, argsJSON string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf(`{"error":"failed to parse tool arguments: %v"}`, err)
	}

	switch {
	case name == "sqlite_query":
		query, _ := args["query"].(string)
		return e.runSQLiteQuery(query)

	case name == "http_request":
		method, _ := args["method"].(string)
		path, _ := args["path"].(string)
		return e.runHTTPRequest(method, path, args["body"])

	case strings.HasPrefix(name, "mcp__"):
		ref, ok := e.mcpRefs[name]
		if !ok {
			return fmt.Sprintf(`{"error":"unknown MCP tool %q"}`, name)
		}
		ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
		defer cancel()
		result, err := ref.client.CallTool(ctx, ref.toolName, args)
		if err != nil {
			return fmt.Sprintf(`{"error":"MCP call failed: %v"}`, err)
		}
		return result

	default:
		return fmt.Sprintf(`{"error":"unknown tool %q"}`, name)
	}
}

func (e *agentToolExecutor) runSQLiteQuery(query string) string {
	trimmed := strings.TrimSpace(strings.ToUpper(query))
	if !strings.HasPrefix(trimmed, "SELECT") && !strings.HasPrefix(trimmed, "WITH") {
		return `{"error":"only SELECT (or WITH ... SELECT) queries are allowed"}`
	}

	var results []map[string]interface{}
	tx := e.db.Raw(query).Scan(&results)
	if tx.Error != nil {
		return fmt.Sprintf(`{"error":"%v"}`, tx.Error)
	}
	if len(results) == 0 {
		return "[]"
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	return string(data)
}

func (e *agentToolExecutor) runHTTPRequest(method, path string, body interface{}) string {
	url := "http://127.0.0.1:8080" + path

	var reqBody io.Reader
	if body != nil && (method == "POST" || method == "PATCH") {
		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return fmt.Sprintf(`{"error":"failed to marshal body: %v"}`, err)
		}
		reqBody = bytes.NewReader(bodyBytes)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Sprintf(`{"error":"failed to build request: %v"}`, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.agentJWT != "" {
		req.Header.Set("Authorization", "Bearer "+e.agentJWT)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Sprintf(`{"error":"request failed: %v"}`, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf(`{"httpStatus":%d,"body":%s}`, resp.StatusCode, string(respBody))
	}

	// Auto-create inbox notification when the agent successfully creates an approval.
	if method == "POST" && (resp.StatusCode == 200 || resp.StatusCode == 201) {
		if m := approvalPathRe.FindStringSubmatch(path); len(m) == 2 {
			companyID := m[1]
			e.autoApprovalInbox(companyID, respBody)
		}
	}

	return string(respBody)
}

// autoApprovalInbox fires a best-effort inbox notification after an approval is created.
func (e *agentToolExecutor) autoApprovalInbox(companyID string, approvalBody []byte) {
	var approval struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Payload interface{} `json:"payload"`
	}
	_ = json.Unmarshal(approvalBody, &approval)

	approvalID := approval.ID
	if approvalID == "" {
		approvalID = "unknown"
	}
	summary := "Agent requested approval"
	if approval.Type != "" {
		summary = fmt.Sprintf("Agent requested approval: %s", approval.Type)
	}

	inboxPayload := map[string]interface{}{
		"id":        uuid.NewString(),
		"kind":      "approval_request",
		"summary":   summary,
		"agentId":   e.agent.ID,
		"payload":   map[string]interface{}{"approvalId": approvalID},
		"createdAt": time.Now().Format(time.RFC3339),
	}
	bodyBytes, err := json.Marshal(inboxPayload)
	if err != nil {
		log.Printf("[tool_executor] auto-inbox marshal error: %v", err)
		return
	}

	inboxURL := fmt.Sprintf("http://127.0.0.1:8080/api/companies/%s/inbox", companyID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inboxURL, bytes.NewReader(bodyBytes))
	if err != nil {
		log.Printf("[tool_executor] auto-inbox build request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if e.agentJWT != "" {
		req.Header.Set("Authorization", "Bearer "+e.agentJWT)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[tool_executor] auto-inbox request error: %v", err)
		return
	}
	defer res.Body.Close()
	log.Printf("[tool_executor] auto-inbox for approval %s → HTTP %d", approvalID, res.StatusCode)
}
