package services

import (
        "bytes"
        "context"
        "encoding/json"
        "fmt"
        "io"
        "log"
        "net/http"
        "os"
        "strings"
        "sync"
        "time"

        "github.com/google/uuid"
        "gorm.io/gorm"
        "paperclip-go/models"
        "paperclip-go/ws"
)

const (
        defaultIntervalMs      = 30000
        defaultMaxConcurrent   = 1
        maxExcerptBytes        = 8 * 1024
)

type HeartbeatService struct {
        db          *gorm.DB
        hub         *ws.Hub
        intervalMs  int
        mu          sync.Mutex
        runningLock map[string]bool
        ctx         context.Context
        cancel      context.CancelFunc
}

func NewHeartbeatService(db *gorm.DB, hub *ws.Hub) *HeartbeatService {
        ctx, cancel := context.WithCancel(context.Background())
        return &HeartbeatService{
                db:          db,
                hub:         hub,
                intervalMs:  defaultIntervalMs,
                runningLock: make(map[string]bool),
                ctx:         ctx,
                cancel:      cancel,
        }
}

func (s *HeartbeatService) Start() {
        log.Printf("[heartbeat] service starting (interval=%dms)", s.intervalMs)
        ticker := time.NewTicker(time.Duration(s.intervalMs) * time.Millisecond)
        defer ticker.Stop()

        // Run once on startup
        go s.tick()

        for {
                select {
                case <-s.ctx.Done():
                        log.Println("[heartbeat] service stopped")
                        return
                case <-ticker.C:
                        go s.tick()
                }
        }
}

func (s *HeartbeatService) Stop() {
        s.cancel()
}

func (s *HeartbeatService) tick() {
        var agents []models.Agent
        s.db.Where("status NOT IN ('paused') AND adapter_type != 'none'").Find(&agents)

        for _, agent := range agents {
                if s.isLocked(agent.ID) {
                        continue
                }

                // Check for pending issues assigned to this agent
                var issue models.Issue
                err := s.db.Where(
                        "assignee_agent_id = ? AND company_id = ? AND status IN ('backlog','todo') AND execution_locked_at IS NULL AND hidden_at IS NULL",
                        agent.ID, agent.CompanyID,
                ).Order("priority ASC, created_at ASC").First(&issue).Error

                if err != nil {
                        // No issue to work on — check for wakeup requests
                        var wakeup models.AgentWakeupRequest
                        if s.db.Where("agent_id = ?", agent.ID).Order("created_at asc").First(&wakeup).Error == nil {
                                s.db.Delete(&wakeup)
                                go s.runAgent(agent, nil, "wakeup")
                        }
                        continue
                }

                go s.checkoutAndRun(agent, &issue)
        }
}

func (s *HeartbeatService) isLocked(agentID string) bool {
        s.mu.Lock()
        defer s.mu.Unlock()
        return s.runningLock[agentID]
}

func (s *HeartbeatService) lock(agentID string) bool {
        s.mu.Lock()
        defer s.mu.Unlock()
        if s.runningLock[agentID] {
                return false
        }
        s.runningLock[agentID] = true
        return true
}

func (s *HeartbeatService) unlock(agentID string) {
        s.mu.Lock()
        defer s.mu.Unlock()
        delete(s.runningLock, agentID)
}

func (s *HeartbeatService) checkoutAndRun(agent models.Agent, issue *models.Issue, ) {
        if !s.lock(agent.ID) {
                return
        }
        defer s.unlock(agent.ID)

        now := time.Now()
        // Lock the issue
        s.db.Model(issue).Updates(map[string]interface{}{
                "execution_locked_at": now,
                "status":              "in_progress",
                "started_at":          now,
                "updated_at":          now,
        })
        s.hub.Publish(ws.LiveEvent{Type: "issue.updated", Payload: issue})

        s.runAgent(agent, issue, "scheduled")
}

func (s *HeartbeatService) runAgent(agent models.Agent, issue *models.Issue, source string) {
        runID := uuid.NewString()
        now := time.Now()

        run := models.HeartbeatRun{
                ID:               runID,
                CompanyID:        agent.CompanyID,
                AgentID:          agent.ID,
                InvocationSource: source,
                Status:           "queued",
                CreatedAt:        now,
                UpdatedAt:        now,
        }
        if issue != nil {
                run.IssueID = &issue.ID
                detail := fmt.Sprintf("issue:%s", issue.ID)
                run.TriggerDetail = &detail
        }
        s.db.Create(&run)
        s.dispatchRun(agent, issue, &run)
}

// dispatchRun transitions an already-created run to running state and executes the agent.
// Called by both runAgent (scheduled) and TriggerRun (comment-triggered) to avoid creating
// a second run record.
func (s *HeartbeatService) dispatchRun(agent models.Agent, issue *models.Issue, run *models.HeartbeatRun) {
        s.hub.Publish(ws.LiveEvent{Type: "heartbeat_run.created", Payload: run})

        // Update agent status
        s.db.Model(&models.Agent{}).Where("id = ?", agent.ID).
                Updates(map[string]interface{}{"status": "running", "updated_at": time.Now()})
        s.hub.Publish(ws.LiveEvent{Type: "agent.updated", Payload: gin_H{"id": agent.ID, "status": "running"}})

        startedAt := time.Now()
        s.db.Model(run).Updates(map[string]interface{}{
                "status":     "running",
                "started_at": startedAt,
                "updated_at": startedAt,
        })

        // Record run_started in issue activity
        if issue != nil {
                agentID := agent.ID
                s.db.Create(&models.ActivityLog{
                        ID:         uuid.NewString(),
                        CompanyID:  run.CompanyID,
                        ActorType:  "agent",
                        ActorID:    agentID,
                        Action:     "run_started",
                        EntityType: "issue",
                        EntityID:   issue.ID,
                        AgentID:    &agentID,
                        Details:    models.JSON{"runId": run.ID, "source": run.InvocationSource},
                        CreatedAt:  time.Now(),
                })
        }

        switch agent.AdapterType {
        case "ollama_local":
                s.runOllamaAgent(agent, issue, run)
        case "openrouter_local":
                s.runOpenRouterAgent(agent, issue, run)
        default:
                s.finishRun(run, nil,
                        fmt.Sprintf("unsupported adapter type: %s", agent.AdapterType),
                        "unsupported_adapter", issue)
        }
}

func (s *HeartbeatService) finishRun(run *models.HeartbeatRun, exitCode *int, errMsg, errCode string, issue *models.Issue) {
        now := time.Now()
        status := "completed"
        if errMsg != "" {
                status = "failed"
        }

        updates := map[string]interface{}{
                "status":       status,
                "completed_at": now,
                "updated_at":   now,
        }
        if errMsg != "" {
                updates["error"] = errMsg
        }
        if errCode != "" {
                updates["error_code"] = errCode
        }
        if exitCode != nil {
                updates["exit_code"] = *exitCode
        }
        s.db.Model(run).Updates(updates)
        s.hub.Publish(ws.LiveEvent{Type: "heartbeat_run.updated", Payload: run})

        // Release issue lock
        if issue != nil {
                issueStatus := "done"
                if status == "failed" {
                        issueStatus = "blocked"
                }
                s.db.Model(&models.Issue{}).Where("id = ?", issue.ID).Updates(map[string]interface{}{
                        "execution_locked_at": nil,
                        "execution_run_id":    run.ID,
                        "status":              issueStatus,
                        "updated_at":          now,
                })
                s.hub.Publish(ws.LiveEvent{Type: "issue.updated", Payload: gin_H{"id": issue.ID, "status": issueStatus}})
        }

        // Log run_completed in issue activity
        if issue != nil {
                agentID := run.AgentID
                s.db.Create(&models.ActivityLog{
                        ID:         uuid.NewString(),
                        CompanyID:  run.CompanyID,
                        ActorType:  "agent",
                        ActorID:    agentID,
                        Action:     "run_completed",
                        EntityType: "issue",
                        EntityID:   issue.ID,
                        AgentID:    &agentID,
                        Details:    models.JSON{"runId": run.ID, "status": status},
                        CreatedAt:  now,
                })
        }

        // Reset agent status to idle
        s.db.Model(&models.Agent{}).Where("id = ?", run.AgentID).
                Updates(map[string]interface{}{
                        "status":            "idle",
                        "last_heartbeat_at": now,
                        "updated_at":        now,
                })
        s.hub.Publish(ws.LiveEvent{Type: "agent.updated", Payload: gin_H{"id": run.AgentID, "status": "idle"}})
}

// chatMessage is a generic role/content pair used by all adapters.
type chatMessage struct {
        Role    string
        Content string
}

// buildSystemPrompt assembles the full system prompt for an agent:
//  1. instruction files from the database (entry file first, then the rest)
//  2. falls back to agent.Capabilities if no instruction files exist
//  3. all company skills appended as labelled sections
func (s *HeartbeatService) buildSystemPrompt(agent models.Agent) string {
        prompt := fmt.Sprintf("You are %s, an AI agent.", agent.Name)
        if agent.Capabilities != nil && *agent.Capabilities != "" {
                prompt = *agent.Capabilities
        }

        // Load instruction files from DB (entry file first, then sorted by order/path)
        var instrFiles []models.AgentInstructionFile
        s.db.Where("agent_id = ?", agent.ID).
                Order("is_entry_file desc, sort_order asc, path asc").
                Find(&instrFiles)
        if len(instrFiles) > 0 {
                var sb strings.Builder
                for i, f := range instrFiles {
                        if i > 0 {
                                sb.WriteString("\n\n")
                        }
                        sb.WriteString(strings.TrimSpace(f.Content))
                }
                prompt = sb.String()
        }

        // Append every company skill so the LLM knows all available tools/docs
        var skills []models.CompanySkill
        s.db.Where("company_id = ?", agent.CompanyID).Order("name asc").Find(&skills)
        if len(skills) > 0 {
                var sb strings.Builder
                sb.WriteString(prompt)
                sb.WriteString("\n\n---\n## Skills & Knowledge\n")
                for _, skill := range skills {
                        sb.WriteString(fmt.Sprintf("\n### %s", skill.Name))
                        if skill.Description != nil && *skill.Description != "" {
                                sb.WriteString(fmt.Sprintf("\n%s", *skill.Description))
                        }
                        if skill.Content != nil && *skill.Content != "" {
                                sb.WriteString(fmt.Sprintf("\n\n%s", *skill.Content))
                        }
                        sb.WriteString("\n")
                }
                prompt = sb.String()
        }

        return prompt
}

// buildIssueUserMessage returns the first user-turn message that describes the issue.
// It includes identifier, title, description, status, priority, and any attachments.
func (s *HeartbeatService) buildIssueUserMessage(issue *models.Issue) string {
        if issue == nil {
                return "You have been activated. Please perform your role."
        }

        var sb strings.Builder
        if issue.Identifier != nil && *issue.Identifier != "" {
                sb.WriteString(fmt.Sprintf("Issue: %s\n", *issue.Identifier))
        }
        sb.WriteString(fmt.Sprintf("Title: %s\n", issue.Title))
        sb.WriteString(fmt.Sprintf("Status: %s | Priority: %s\n", issue.Status, issue.Priority))
        if issue.Description != nil && *issue.Description != "" {
                sb.WriteString(fmt.Sprintf("\nDescription:\n%s", *issue.Description))
        }
        sb.WriteString(s.attachmentContext(issue.ID))
        return sb.String()
}

// buildConversationHistory returns prior comments as alternating user/assistant turns.
func (s *HeartbeatService) buildConversationHistory(issue *models.Issue) []chatMessage {
        if issue == nil {
                return nil
        }
        var priorComments []models.IssueComment
        s.db.Where("issue_id = ?", issue.ID).Order("created_at asc").Find(&priorComments)
        msgs := make([]chatMessage, 0, len(priorComments))
        for _, c := range priorComments {
                role := "user"
                if c.AuthorAgentID != nil && *c.AuthorAgentID != "" {
                        role = "assistant"
                }
                msgs = append(msgs, chatMessage{Role: role, Content: c.Body})
        }
        return msgs
}

// runOllamaAgent calls the Ollama HTTP API in a multi-turn agentic tool-calling loop.
func (s *HeartbeatService) runOllamaAgent(agent models.Agent, issue *models.Issue, run *models.HeartbeatRun) {
        cfg := agent.AdapterConfig

        baseURL := "http://localhost:11434"
        if v, ok := cfg["baseUrl"].(string); ok && v != "" {
                baseURL = strings.TrimRight(v, "/")
        }
        model := "llama3.2"
        if v, ok := cfg["model"].(string); ok && v != "" {
                model = v
        }
        apiKey, _ := cfg["apiKey"].(string)
        timeoutSec := 180
        if v, ok := cfg["timeoutSec"].(float64); ok && v > 0 {
                timeoutSec = int(v)
        }

        systemPrompt := s.buildSystemPrompt(agent)
        userMsg := s.buildIssueUserMessage(issue)
        history := s.buildConversationHistory(issue)

        executor := newAgentToolExecutor(s.db, agent)
        defer executor.close()
        tools := executor.allTools()

        // Build initial messages as generic maps for flexible tool message support
        messages := []map[string]interface{}{
                {"role": "system", "content": systemPrompt},
                {"role": "user", "content": userMsg},
        }
        for _, h := range history {
                messages = append(messages, map[string]interface{}{"role": h.Role, "content": h.Content})
        }

        s.saveRunEvent(run.ID, "llm_call", fmt.Sprintf("Calling Ollama model %s at %s (tool-enabled)", model, baseURL), nil)

        // Ollama tool-call response shape
        type ollamaToolCall struct {
                Function struct {
                        Name      string                 `json:"name"`
                        Arguments map[string]interface{} `json:"arguments"`
                } `json:"function"`
        }
        type ollamaMessage struct {
                Role      string           `json:"role"`
                Content   string           `json:"content"`
                ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
        }
        type ollamaResponse struct {
                Message    ollamaMessage `json:"message"`
                DoneReason string        `json:"done_reason"`
                Done       bool          `json:"done"`
        }

        var finalText string

        for round := 0; round < maxToolRounds; round++ {
                reqBody := map[string]interface{}{
                        "model":    model,
                        "messages": messages,
                        "stream":   false,
                        "tools":    tools,
                }
                bodyBytes, _ := json.Marshal(reqBody)
                ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)

                httpReq, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/chat", bytes.NewReader(bodyBytes))
                if err != nil {
                        cancel()
                        s.finishRun(run, nil, "failed to build Ollama request: "+err.Error(), "ollama_error", issue)
                        return
                }
                httpReq.Header.Set("Content-Type", "application/json")
                if apiKey != "" {
                        httpReq.Header.Set("Authorization", "Bearer "+apiKey)
                }

                resp, err := http.DefaultClient.Do(httpReq)
                cancel()
                if err != nil {
                        s.finishRun(run, nil, "Ollama request failed: "+err.Error(), "ollama_error", issue)
                        return
                }

                if resp.StatusCode < 200 || resp.StatusCode >= 300 {
                        body, _ := io.ReadAll(resp.Body)
                        resp.Body.Close()
                        s.finishRun(run, nil, fmt.Sprintf("Ollama returned HTTP %d: %s", resp.StatusCode, string(body)), "ollama_error", issue)
                        return
                }

                var ollamaResp ollamaResponse
                if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
                        resp.Body.Close()
                        s.finishRun(run, nil, "failed to parse Ollama response: "+err.Error(), "ollama_error", issue)
                        return
                }
                resp.Body.Close()

                msg := ollamaResp.Message

                // No tool calls — this is the final text response
                if len(msg.ToolCalls) == 0 {
                        finalText = msg.Content
                        break
                }

                // Append the assistant's tool-call message to history
                messages = append(messages, map[string]interface{}{
                        "role":       "assistant",
                        "content":    msg.Content,
                        "tool_calls": msg.ToolCalls,
                })

                // Execute each tool call and append results
                for _, tc := range msg.ToolCalls {
                        argsBytes, _ := json.Marshal(tc.Function.Arguments)
                        result := executor.execute(tc.Function.Name, string(argsBytes))
                        log.Printf("[ollama] tool=%s agent=%s round=%d result_len=%d", tc.Function.Name, agent.ID, round, len(result))
                        s.saveRunEvent(run.ID, "tool_call", fmt.Sprintf("Tool: %s", tc.Function.Name), map[string]interface{}{
                                "tool":   tc.Function.Name,
                                "args":   tc.Function.Arguments,
                                "result": truncate(result, 1024),
                        })
                        messages = append(messages, map[string]interface{}{
                                "role":    "tool",
                                "content": result,
                        })
                }
        }

        log.Printf("[ollama] agent=%s run=%s final_len=%d", agent.ID, run.ID, len(finalText))
        s.saveRunEvent(run.ID, "llm_response", truncate(finalText, 4096), map[string]interface{}{
                "model":   model,
                "baseUrl": baseURL,
        })
        s.postRunResult(agent, issue, run, finalText)
}

// attachmentContext returns text from any readable attachments on the issue,
// ready to be appended to the LLM user message.
func (s *HeartbeatService) attachmentContext(issueID string) string {
        var attachments []models.IssueAttachment
        s.db.Where("issue_id = ?", issueID).Order("created_at asc").Find(&attachments)
        if len(attachments) == 0 {
                return ""
        }

        var sb strings.Builder
        sb.WriteString("\n\n--- Attached files ---")
        for _, a := range attachments {
                sb.WriteString(fmt.Sprintf("\n\nFile: %s (%s, %d bytes)", a.Filename, a.MimeType, a.SizeBytes))
                isText := strings.HasPrefix(a.MimeType, "text/") ||
                        a.MimeType == "application/json" ||
                        a.MimeType == "application/xml" ||
                        a.MimeType == "application/javascript" ||
                        a.MimeType == "application/x-yaml" ||
                        a.MimeType == "application/yaml"
                if isText {
                        data, err := os.ReadFile(a.StorePath)
                        if err == nil {
                                const maxBytes = 32 * 1024
                                content := string(data)
                                if len(content) > maxBytes {
                                        content = content[:maxBytes] + "\n[truncated]"
                                }
                                sb.WriteString("\n```\n")
                                sb.WriteString(content)
                                sb.WriteString("\n```")
                        }
                }
        }
        return sb.String()
}

func (s *HeartbeatService) saveRunEvent(runID, kind, summary string, detail map[string]interface{}) {
        var maxSeq struct{ Max int }
        s.db.Model(&models.HeartbeatRunEvent{}).
                Select("COALESCE(MAX(sequence_number),0) as max").
                Where("heartbeat_run_id = ?", runID).
                Scan(&maxSeq)

        var detailJSON models.JSON
        if detail != nil {
                detailJSON = models.JSON(detail)
        }

        event := models.HeartbeatRunEvent{
                ID:             uuid.NewString(),
                HeartbeatRunID: runID,
                Kind:           kind,
                Summary:        summary,
                Detail:         detailJSON,
                SequenceNumber: maxSeq.Max + 1,
                CreatedAt:      time.Now(),
        }
        s.db.Create(&event)
}

// runOpenRouterAgent calls the OpenRouter API in a multi-turn agentic tool-calling loop.
func (s *HeartbeatService) runOpenRouterAgent(agent models.Agent, issue *models.Issue, run *models.HeartbeatRun) {
        cfg := agent.AdapterConfig

        model := "openai/gpt-4o-mini"
        if v, ok := cfg["model"].(string); ok && v != "" {
                model = v
        }

        apiKey, _ := cfg["apiKey"].(string)
        if apiKey == "" {
                apiKey = os.Getenv("OPENROUTER_API_KEY")
        }
        if apiKey == "" {
                keys := make([]string, 0, len(cfg))
                for k := range cfg {
                        keys = append(keys, k)
                }
                log.Printf("[openrouter] agent=%s: apiKey missing. adapterConfig keys present: %v", agent.ID, keys)
                s.finishRun(run, nil, "OpenRouter API key not set — open the agent settings and add your API key in the 'API key' field, then retry", "missing_api_key", issue)
                return
        }

        timeoutSec := 180
        if v, ok := cfg["timeoutSec"].(float64); ok && v > 0 {
                timeoutSec = int(v)
        }

        systemPrompt := s.buildSystemPrompt(agent)
        userMsg := s.buildIssueUserMessage(issue)
        history := s.buildConversationHistory(issue)

        executor := newAgentToolExecutor(s.db, agent)
        defer executor.close()
        tools := executor.allTools()

        // Build initial messages as generic maps for flexible tool message support
        messages := []map[string]interface{}{
                {"role": "system", "content": systemPrompt},
                {"role": "user", "content": userMsg},
        }
        for _, h := range history {
                messages = append(messages, map[string]interface{}{"role": h.Role, "content": h.Content})
        }

        s.saveRunEvent(run.ID, "llm_call", fmt.Sprintf("Calling OpenRouter model %s (tool-enabled)", model), nil)

        // OpenAI-compatible tool call response shapes
        type orToolCallFn struct {
                Name      string `json:"name"`
                Arguments string `json:"arguments"`
        }
        type orToolCall struct {
                ID       string       `json:"id"`
                Type     string       `json:"type"`
                Function orToolCallFn `json:"function"`
        }
        type orMessage struct {
                Role      string       `json:"role"`
                Content   *string      `json:"content"`
                ToolCalls []orToolCall `json:"tool_calls,omitempty"`
        }
        type orChoice struct {
                Message      orMessage `json:"message"`
                FinishReason string    `json:"finish_reason"`
        }
        type orResponse struct {
                Choices []orChoice `json:"choices"`
        }

        var finalText string

        for round := 0; round < maxToolRounds; round++ {
                reqBody := map[string]interface{}{
                        "model":       model,
                        "messages":    messages,
                        "tools":       tools,
                        "tool_choice": "auto",
                }
                bodyBytes, _ := json.Marshal(reqBody)
                ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)

                httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(bodyBytes))
                if err != nil {
                        cancel()
                        s.finishRun(run, nil, "failed to build OpenRouter request: "+err.Error(), "openrouter_error", issue)
                        return
                }
                httpReq.Header.Set("Content-Type", "application/json")
                httpReq.Header.Set("Authorization", "Bearer "+apiKey)
                httpReq.Header.Set("HTTP-Referer", "https://nanoclip.dev")
                httpReq.Header.Set("X-Title", "NanoClip")

                resp, err := http.DefaultClient.Do(httpReq)
                cancel()
                if err != nil {
                        s.finishRun(run, nil, "OpenRouter request failed: "+err.Error(), "openrouter_error", issue)
                        return
                }

                if resp.StatusCode < 200 || resp.StatusCode >= 300 {
                        body, _ := io.ReadAll(resp.Body)
                        resp.Body.Close()
                        s.finishRun(run, nil, fmt.Sprintf("OpenRouter returned HTTP %d: %s", resp.StatusCode, string(body)), "openrouter_error", issue)
                        return
                }

                var orResp orResponse
                if err := json.NewDecoder(resp.Body).Decode(&orResp); err != nil {
                        resp.Body.Close()
                        s.finishRun(run, nil, "failed to parse OpenRouter response: "+err.Error(), "openrouter_error", issue)
                        return
                }
                resp.Body.Close()

                if len(orResp.Choices) == 0 {
                        s.finishRun(run, nil, "OpenRouter returned empty choices", "openrouter_error", issue)
                        return
                }

                choice := orResp.Choices[0]
                msg := choice.Message

                // No tool calls — final text response
                if len(msg.ToolCalls) == 0 || choice.FinishReason == "stop" {
                        if msg.Content != nil {
                                finalText = *msg.Content
                        }
                        break
                }

                // Append the assistant's tool-call message to history
                assistantMsg := map[string]interface{}{
                        "role":       "assistant",
                        "content":    nil,
                        "tool_calls": msg.ToolCalls,
                }
                messages = append(messages, assistantMsg)

                // Execute each tool call and append the results
                for _, tc := range msg.ToolCalls {
                        result := executor.execute(tc.Function.Name, tc.Function.Arguments)
                        log.Printf("[openrouter] tool=%s agent=%s round=%d result_len=%d", tc.Function.Name, agent.ID, round, len(result))
                        s.saveRunEvent(run.ID, "tool_call", fmt.Sprintf("Tool: %s", tc.Function.Name), map[string]interface{}{
                                "tool":   tc.Function.Name,
                                "args":   tc.Function.Arguments,
                                "result": truncate(result, 1024),
                        })
                        messages = append(messages, map[string]interface{}{
                                "role":         "tool",
                                "tool_call_id": tc.ID,
                                "content":      result,
                        })
                }
        }

        log.Printf("[openrouter] agent=%s run=%s final_len=%d", agent.ID, run.ID, len(finalText))
        s.saveRunEvent(run.ID, "llm_response", truncate(finalText, 4096), map[string]interface{}{
                "model": model,
        })
        s.postRunResult(agent, issue, run, finalText)
}

// postRunResult posts the agent's final text as an issue comment and finishes the run.
func (s *HeartbeatService) postRunResult(agent models.Agent, issue *models.Issue, run *models.HeartbeatRun, text string) {
        if issue != nil && text != "" {
                comment := models.IssueComment{
                        ID:            uuid.NewString(),
                        CompanyID:     agent.CompanyID,
                        IssueID:       issue.ID,
                        AuthorAgentID: &agent.ID,
                        Body:          text,
                        CreatedAt:     time.Now(),
                        UpdatedAt:     time.Now(),
                }
                s.db.Create(&comment)
        }

        excerpt := truncate(text, maxExcerptBytes)
        s.db.Model(run).Update("stdout_excerpt", excerpt)

        exitCode := 0
        s.finishRun(run, &exitCode, "", "", issue)
}

// createRunResultSubIssue creates a child issue under the parent issue to record the agent's run result.
func (s *HeartbeatService) createRunResultSubIssue(agent models.Agent, parent *models.Issue, run *models.HeartbeatRun, result string) {
        var company models.Company
        if err := s.db.First(&company, "id = ?", parent.CompanyID).Error; err != nil {
                log.Printf("[heartbeat] createRunResultSubIssue: company not found: %v", err)
                return
        }
        s.db.Model(&company).Update("issue_counter", gorm.Expr("issue_counter + 1"))
        s.db.First(&company, "id = ?", parent.CompanyID)
        issueNumber := company.IssueCounter
        identifier := fmt.Sprintf("%s-%d", company.IssuePrefix, issueNumber)

        // Build a short title from the first non-empty line of the response
        title := result
        if idx := strings.IndexAny(title, "\n\r"); idx > 0 {
                title = title[:idx]
        }
        title = strings.TrimSpace(title)
        if len(title) > 120 {
                title = title[:120] + "…"
        }
        if title == "" {
                title = fmt.Sprintf("Run result (%s)", run.ID[:8])
        }

        now := time.Now()
        agentID := agent.ID
        subIssue := models.Issue{
                ID:               uuid.NewString(),
                CompanyID:        parent.CompanyID,
                ProjectID:        parent.ProjectID,
                ParentID:         &parent.ID,
                Title:            title,
                Description:      &result,
                Status:           "done",
                Priority:         "medium",
                AssigneeAgentID:  &agentID,
                CreatedByAgentID: &agentID,
                IssueNumber:      &issueNumber,
                Identifier:       &identifier,
                OriginKind:       "run_result",
                CreatedAt:        now,
                UpdatedAt:        now,
        }
        if err := s.db.Create(&subIssue).Error; err != nil {
                log.Printf("[heartbeat] createRunResultSubIssue: failed to create sub-issue: %v", err)
                return
        }
        s.hub.Publish(ws.LiveEvent{Type: "issue.created", CompanyID: parent.CompanyID, Payload: subIssue})
        parentID := parent.ID
        if parent.Identifier != nil {
                parentID = *parent.Identifier
        }
        log.Printf("[heartbeat] created run result sub-issue %s for parent %s", identifier, parentID)
}

// TriggerRun manually starts a run for an agent (called from the checkout API)
func (s *HeartbeatService) TriggerRun(agentID, companyID string, issue *models.Issue) (*models.HeartbeatRun, error) {
        var agent models.Agent
        if err := s.db.First(&agent, "id = ? AND company_id = ?", agentID, companyID).Error; err != nil {
                return nil, fmt.Errorf("agent not found")
        }
        if !s.lock(agentID) {
                return nil, fmt.Errorf("agent is already running")
        }

        runID := uuid.NewString()
        now := time.Now()
        source := "manual"
        if issue != nil {
                source = "comment"
        }
        run := models.HeartbeatRun{
                ID:               runID,
                CompanyID:        companyID,
                AgentID:          agentID,
                InvocationSource: source,
                Status:           "queued",
                CreatedAt:        now,
                UpdatedAt:        now,
        }
        if issue != nil {
                run.IssueID = &issue.ID
        }
        s.db.Create(&run)

        if issue != nil {
                actorID := agentID
                s.db.Create(&models.ActivityLog{
                        ID:         uuid.NewString(),
                        CompanyID:  companyID,
                        ActorType:  "agent",
                        ActorID:    actorID,
                        Action:     "run_triggered",
                        EntityType: "issue",
                        EntityID:   issue.ID,
                        AgentID:    &agentID,
                        CreatedAt:  now,
                })
        }

        go func() {
                defer s.unlock(agentID)
                s.dispatchRun(agent, issue, &run)
        }()

        return &run, nil
}

func truncate(s string, maxBytes int) string {
        if len(s) <= maxBytes {
                return s
        }
        return s[:maxBytes]
}

// gin_H is a shorthand alias (avoid import cycle with gin)
type gin_H = map[string]interface{}
