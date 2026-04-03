package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"paperclip-go/models"
)

const mcpInitTimeout = 15 * time.Second
const mcpCallTimeout = 30 * time.Second

// ─── JSON-RPC types ───────────────────────────────────────────────────────────

type mcpRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ─── Tool schema ──────────────────────────────────────────────────────────────

// MCPTool is a single tool advertised by an MCP server.
type MCPTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// mcpToolRef pairs a live client with the original (unprefixed) tool name.
type mcpToolRef struct {
	client   MCPClient
	toolName string
}

// ─── Client interface ─────────────────────────────────────────────────────────

// MCPClient abstracts over stdio and HTTP MCP transport.
type MCPClient interface {
	Connect(ctx context.Context) error
	ListTools(ctx context.Context) ([]MCPTool, error)
	CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error)
	Close()
}

// ─── stdio client ─────────────────────────────────────────────────────────────

type stdioMCPClient struct {
	server  models.AgentMCPServer
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	mu      sync.Mutex
	idSeq   int
}

func newStdioMCPClient(server models.AgentMCPServer) *stdioMCPClient {
	return &stdioMCPClient{server: server}
}

func (c *stdioMCPClient) Connect(ctx context.Context) error {
	command := ""
	if c.server.Command != nil {
		command = strings.TrimSpace(*c.server.Command)
	}
	if command == "" {
		return fmt.Errorf("MCP server %q: no command configured", c.server.Name)
	}

	// Run via sh so the user can write "npx pkg arg1 arg2" naturally.
	c.cmd = exec.CommandContext(ctx, "sh", "-c", command)

	// Inject configured env vars on top of the current environment.
	c.cmd.Env = os.Environ()
	for k, v := range c.server.Env {
		if s, ok := v.(string); ok {
			c.cmd.Env = append(c.cmd.Env, fmt.Sprintf("%s=%s", k, s))
		}
	}

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	c.stdin = stdin

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	// 4 MB scanner buffer — generous but bounded for Termux.
	c.scanner = bufio.NewScanner(stdout)
	c.scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	// MCP handshake: initialize
	return c.initialize(ctx)
}

func (c *stdioMCPClient) nextID() int {
	c.idSeq++
	return c.idSeq
}

func (c *stdioMCPClient) sendRaw(req mcpRPCRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

func (c *stdioMCPClient) recvRaw() (*mcpRPCResponse, error) {
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("MCP server closed connection")
	}
	var resp mcpRPCResponse
	if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w (raw: %s)", err, c.scanner.Text())
	}
	return &resp, nil
}

func (c *stdioMCPClient) initialize(ctx context.Context) error {
	req := mcpRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "NanoClip", "version": "1.0"},
		},
	}
	if err := c.sendRaw(req); err != nil {
		return fmt.Errorf("initialize send: %w", err)
	}
	resp, err := c.recvRaw()
	if err != nil {
		return fmt.Errorf("initialize recv: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}
	// Send initialized notification (no response expected).
	notif := mcpRPCRequest{JSONRPC: "2.0", Method: "notifications/initialized", Params: map[string]interface{}{}}
	_ = c.sendRaw(notif)
	return nil
}

func (c *stdioMCPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	req := mcpRPCRequest{JSONRPC: "2.0", ID: c.nextID(), Method: "tools/list", Params: map[string]interface{}{}}
	if err := c.sendRaw(req); err != nil {
		return nil, err
	}
	resp, err := c.recvRaw()
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list: %s", resp.Error.Message)
	}
	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}
	return result.Tools, nil
}

func (c *stdioMCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if args == nil {
		args = map[string]interface{}{}
	}
	req := mcpRPCRequest{
		JSONRPC: "2.0",
		ID:      c.nextID(),
		Method:  "tools/call",
		Params:  map[string]interface{}{"name": name, "arguments": args},
	}
	if err := c.sendRaw(req); err != nil {
		return "", err
	}
	resp, err := c.recvRaw()
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return fmt.Sprintf(`{"error":"MCP error: %s"}`, resp.Error.Message), nil
	}
	return extractMCPContent(resp.Result), nil
}

func (c *stdioMCPClient) Close() {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}

// ─── HTTP client ──────────────────────────────────────────────────────────────

type httpMCPClient struct {
	server models.AgentMCPServer
	url    string
	mu     sync.Mutex
	idSeq  int
}

func newHTTPMCPClient(server models.AgentMCPServer) *httpMCPClient {
	url := ""
	if server.URL != nil {
		url = strings.TrimSpace(*server.URL)
	}
	return &httpMCPClient{server: server, url: url}
}

func (c *httpMCPClient) Connect(ctx context.Context) error {
	if c.url == "" {
		return fmt.Errorf("MCP server %q: no URL configured", c.server.Name)
	}
	return nil
}

func (c *httpMCPClient) nextID() int {
	c.idSeq++
	return c.idSeq
}

func (c *httpMCPClient) doRPC(ctx context.Context, method string, params interface{}) (*mcpRPCResponse, error) {
	c.mu.Lock()
	id := c.nextID()
	c.mu.Unlock()

	req := mcpRPCRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp mcpRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("parse HTTP MCP response: %w", err)
	}
	return &rpcResp, nil
}

func (c *httpMCPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	resp, err := c.doRPC(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list: %s", resp.Error.Message)
	}
	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}
	return result.Tools, nil
}

func (c *httpMCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	resp, err := c.doRPC(ctx, "tools/call", map[string]interface{}{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return fmt.Sprintf(`{"error":"MCP error: %s"}`, resp.Error.Message), nil
	}
	return extractMCPContent(resp.Result), nil
}

func (c *httpMCPClient) Close() {}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// extractMCPContent pulls text content out of a tools/call result.
func extractMCPContent(raw json.RawMessage) string {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return string(raw)
	}
	var parts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) == 0 {
		return string(raw)
	}
	return strings.Join(parts, "\n")
}

// newMCPClient creates the right client for the server's transport.
func newMCPClient(server models.AgentMCPServer) (MCPClient, error) {
	switch server.Transport {
	case "http", "sse":
		return newHTTPMCPClient(server), nil
	case "stdio", "":
		return newStdioMCPClient(server), nil
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", server.Transport)
	}
}

// mcpServerToolName builds the prefixed tool name exposed to the LLM.
// Format: mcp__{normalizedServerName}__{originalToolName}
func mcpServerToolName(serverName, toolName string) string {
	norm := strings.ToLower(serverName)
	norm = strings.NewReplacer(" ", "_", "-", "_", ".", "_").Replace(norm)
	return fmt.Sprintf("mcp__%s__%s", norm, toolName)
}

// loadAgentMCPTools connects to all enabled MCP servers for the given agent,
// discovers their tools, and returns tool definitions + a ref map for dispatch.
func loadAgentMCPTools(db *gorm.DB, agentID string) ([]ToolDefinition, map[string]mcpToolRef) {
	var servers []models.AgentMCPServer
	db.Where("agent_id = ? AND enabled = ?", agentID, true).Find(&servers)

	var tools []ToolDefinition
	refs := map[string]mcpToolRef{}

	for _, srv := range servers {
		client, err := newMCPClient(srv)
		if err != nil {
			log.Printf("[mcp] agent=%s server=%q: %v", agentID, srv.Name, err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), mcpInitTimeout)
		err = client.Connect(ctx)
		cancel()
		if err != nil {
			log.Printf("[mcp] agent=%s server=%q connect: %v", agentID, srv.Name, err)
			client.Close()
			continue
		}

		ctx2, cancel2 := context.WithTimeout(context.Background(), mcpInitTimeout)
		mcpTools, err := client.ListTools(ctx2)
		cancel2()
		if err != nil {
			log.Printf("[mcp] agent=%s server=%q tools/list: %v", agentID, srv.Name, err)
			client.Close()
			continue
		}

		log.Printf("[mcp] agent=%s server=%q: discovered %d tool(s)", agentID, srv.Name, len(mcpTools))

		for _, t := range mcpTools {
			prefixed := mcpServerToolName(srv.Name, t.Name)

			schema := t.InputSchema
			if schema == nil {
				schema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}

			tools = append(tools, ToolDefinition{
				Type: "function",
				Function: toolFunctionDef{
					Name:        prefixed,
					Description: fmt.Sprintf("[MCP:%s] %s", srv.Name, t.Description),
					Parameters:  schema,
				},
			})
			refs[prefixed] = mcpToolRef{client: client, toolName: t.Name}
		}
	}

	return tools, refs
}
