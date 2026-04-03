package handlers

import (
        "fmt"
        "net/http"
        "regexp"
        "strings"
        "time"

        "github.com/gin-gonic/gin"
        "github.com/google/uuid"
        "gorm.io/gorm"
        "paperclip-go/models"
        mw "paperclip-go/middleware"
)

// GlobalAgentRoutes handles /api/agents/:agentId (no company scope, uses query param)
func GlobalAgentRoutes(rg *gin.RouterGroup, db *gorm.DB) {
        rg.GET("/:agentId", getGlobalAgent(db))
        rg.PATCH("/:agentId", updateGlobalAgent(db))
        rg.GET("/:agentId/runtime-state", getAgentRuntimeState(db))
        rg.POST("/:agentId/runtime-state/reset-session", resetAgentSession(db))
        rg.GET("/:agentId/skills", getAgentSkills(db))
        rg.POST("/:agentId/skills/sync", syncAgentSkills(db))
        rg.GET("/:agentId/keys", getAgentKeys(db))
        rg.POST("/:agentId/keys", CreateAgentKey(db))
        rg.DELETE("/:agentId/keys/:keyId", RevokeAgentKey(db))
        rg.GET("/:agentId/config-revisions", getAgentConfigRevisions(db))
        rg.POST("/:agentId/pause", pauseGlobalAgent(db))
        rg.POST("/:agentId/resume", resumeGlobalAgent(db))
        rg.POST("/:agentId/wakeup", wakeupGlobalAgent(db))
        rg.PATCH("/:agentId/permissions", updateAgentPermissions(db))
        // Instructions bundle
        rg.GET("/:agentId/instructions-bundle", GetInstructionsBundle(db))
        rg.PATCH("/:agentId/instructions-bundle", UpdateInstructionsBundle(db))
        rg.GET("/:agentId/instructions-bundle/file", GetInstructionsFile(db))
        rg.PUT("/:agentId/instructions-bundle/file", SaveInstructionsFile(db))
        rg.DELETE("/:agentId/instructions-bundle/file", DeleteInstructionsFile(db))
        // MCP server configurations
        rg.GET("/:agentId/mcp-servers", ListAgentMCPServers(db))
        rg.POST("/:agentId/mcp-servers", CreateAgentMCPServer(db))
        rg.PATCH("/:agentId/mcp-servers/:serverId", UpdateAgentMCPServer(db))
        rg.DELETE("/:agentId/mcp-servers/:serverId", DeleteAgentMCPServer(db))
}

var uuidRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var shortHexRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

func isUUIDLike(s string) bool {
        return uuidRe.MatchString(s)
}

func normalizeAgentKey(key string) string {
        re := regexp.MustCompile(`[^a-z0-9-]`)
        return re.ReplaceAllString(strings.ToLower(key), "-")
}

// resolveAgentByParam resolves an agent by full UUID or derived URL-key slug.
func resolveAgentByParam(db *gorm.DB, param, companyID string) (*models.Agent, int, error) {
        q := db
        if companyID != "" {
                q = q.Where("company_id = ?", companyID)
        }

        if isUUIDLike(param) {
                var agent models.Agent
                if err := q.Where("id = ?", param).First(&agent).Error; err != nil {
                        return nil, http.StatusNotFound, fmt.Errorf("agent not found")
                }
                return &agent, http.StatusOK, nil
        }

        // Slug format: {name-slug}-{shortId} where shortId = first 8 hex chars of UUID (no dashes).
        // The UUID string always starts with 8 hex chars so: id LIKE 'b8b8ffa6-%'
        parts := strings.Split(param, "-")
        shortID := parts[len(parts)-1]
        if !shortHexRe.MatchString(shortID) {
                return nil, http.StatusNotFound, fmt.Errorf("agent not found")
        }

        var agents []models.Agent
        if err := q.Where("id LIKE ?", shortID+"-%").Find(&agents).Error; err != nil || len(agents) == 0 {
                return nil, http.StatusNotFound, fmt.Errorf("agent not found")
        }

        normalizedParam := normalizeAgentKey(param)
        var matches []models.Agent
        for _, a := range agents {
                if normalizeAgentKey(computeAgentUrlKey(a.Name, a.ID)) == normalizedParam {
                        matches = append(matches, a)
                }
        }

        if len(matches) == 0 {
                return nil, http.StatusNotFound, fmt.Errorf("agent not found")
        }
        if len(matches) > 1 {
                return nil, http.StatusConflict, fmt.Errorf("ambiguous agent identifier")
        }
        return &matches[0], http.StatusOK, nil
}

func getGlobalAgent(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agentID := c.Param("agentId")
                companyID := c.Query("companyId")

                agent, status, err := resolveAgentByParam(db, agentID, companyID)
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }
                c.JSON(http.StatusOK, wrapAgent(agent))
        }
}

func updateGlobalAgent(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agentID := c.Param("agentId")
                companyID := c.Query("companyId")

                agent, status, err := resolveAgentByParam(db, agentID, companyID)
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }

                var req struct {
                        Name          *string     `json:"name"`
                        Status        *string     `json:"status"`
                        Title         *string     `json:"title"`
                        Icon          *string     `json:"icon"`
                        AdapterType   *string     `json:"adapterType"`
                        AdapterConfig models.JSON `json:"adapterConfig"`
                        RuntimeConfig models.JSON `json:"runtimeConfig"`
                }
                c.ShouldBindJSON(&req)

                updates := map[string]interface{}{"updated_at": time.Now()}
                if req.Name != nil {
                        updates["name"] = *req.Name
                }
                if req.Status != nil {
                        updates["status"] = *req.Status
                }
                if req.Title != nil {
                        updates["title"] = req.Title
                }
                if req.Icon != nil {
                        updates["icon"] = req.Icon
                }
                if req.AdapterType != nil {
                        updates["adapter_type"] = *req.AdapterType
                }
                if req.AdapterConfig != nil {
                        // Merge incoming fields into existing config so a partial update
                        // (e.g. updating only apiKey) does not wipe model/baseUrl/etc.
                        merged := models.JSON{}
                        for k, v := range agent.AdapterConfig {
                                merged[k] = v
                        }
                        for k, v := range req.AdapterConfig {
                                merged[k] = v
                        }
                        updates["adapter_config"] = merged
                }
                if req.RuntimeConfig != nil {
                        updates["runtime_config"] = req.RuntimeConfig
                }
                db.Model(agent).Updates(updates)
                db.First(agent, "id = ?", agent.ID)

                actor := mw.GetActor(c)
                logActivity(db, agent.CompanyID, actor, "updated", "agent", agent.ID, nil)
                c.JSON(http.StatusOK, wrapAgent(agent))
        }
}

func getAgentRuntimeState(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agentID := c.Param("agentId")
                companyID := c.Query("companyId")

                agent, status, err := resolveAgentByParam(db, agentID, companyID)
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }

                // Fetch the last heartbeat run for timing info
                var lastRun models.HeartbeatRun
                lastRunErr := db.Where("agent_id = ?", agent.ID).
                        Order("created_at desc").First(&lastRun).Error

                var lastRunAt *string
                var currentRunID *string
                if lastRunErr == nil {
                        ts := lastRun.CreatedAt.Format(time.RFC3339)
                        lastRunAt = &ts
                        if lastRun.Status == "running" {
                                currentRunID = &lastRun.ID
                        }
                }

                c.JSON(http.StatusOK, gin.H{
                        "status":                  agent.Status,
                        "lastRunAt":               lastRunAt,
                        "currentRunId":            currentRunID,
                        "totalInputTokens":        0,
                        "totalOutputTokens":       0,
                        "totalCachedInputTokens":  0,
                        "totalCostCents":          0,
                })
        }
}

func resetAgentSession(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                // Stub — session management is handled by the adapter layer
                c.JSON(http.StatusOK, gin.H{"ok": true})
        }
}

// editableAdapterTypes lists adapter types that support NanoClip-managed skill selection.
var editableAdapterTypes = map[string]bool{
        "ollama_local":      true,
        "openrouter_local":  true,
        "http":              true,
}

// buildAgentSkillSnapshot assembles the skill snapshot response for an agent.
func buildAgentSkillSnapshot(agent *models.Agent) gin.H {
        mode := "unsupported"
        if editableAdapterTypes[agent.AdapterType] {
                mode = "editable"
        }

        desiredSkills := []string{}
        if raw, ok := agent.AdapterConfig["desiredSkills"]; ok {
                if sl, ok := raw.([]interface{}); ok {
                        for _, v := range sl {
                                if s, ok := v.(string); ok {
                                        desiredSkills = append(desiredSkills, s)
                                }
                        }
                }
        }

        return gin.H{
                "skills":        []gin.H{},
                "entries":       []map[string]interface{}{},
                "desiredSkills": desiredSkills,
                "warnings":      []string{},
                "mode":          mode,
        }
}

// getAgentSkills returns the skills snapshot for an agent.
func getAgentSkills(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agentID := c.Param("agentId")
                companyID := c.Query("companyId")

                agent, status, err := resolveAgentByParam(db, agentID, companyID)
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }

                c.JSON(http.StatusOK, buildAgentSkillSnapshot(agent))
        }
}

// syncAgentSkills saves the desired skill keys for an agent and returns the updated snapshot.
func syncAgentSkills(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agentID := c.Param("agentId")
                companyID := c.Query("companyId")

                agent, status, err := resolveAgentByParam(db, agentID, companyID)
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }

                var body struct {
                        DesiredSkills []string `json:"desiredSkills"`
                }
                if err := c.ShouldBindJSON(&body); err != nil {
                        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
                        return
                }

                cfg := agent.AdapterConfig
                if cfg == nil {
                        cfg = models.JSON{}
                }
                iface := make([]interface{}, len(body.DesiredSkills))
                for i, s := range body.DesiredSkills {
                        iface[i] = s
                }
                cfg["desiredSkills"] = iface
                if err := db.Model(agent).Update("adapter_config", cfg).Error; err != nil {
                        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save skills"})
                        return
                }
                agent.AdapterConfig = cfg

                c.JSON(http.StatusOK, buildAgentSkillSnapshot(agent))
        }
}

// getAgentKeys returns the API keys for an agent.
func getAgentKeys(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agentID := c.Param("agentId")
                companyID := c.Query("companyId")

                agent, status, err := resolveAgentByParam(db, agentID, companyID)
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }

                var keys []models.AgentAPIKey
                db.Where("agent_id = ?", agent.ID).Order("created_at desc").Find(&keys)

                // Never return raw key values — only metadata
                out := make([]gin.H, 0, len(keys))
                for _, k := range keys {
                        out = append(out, gin.H{
                                "id":          k.ID,
                                "agentId":     k.AgentID,
                                "label":      k.Label,
                                "companyId":  k.CompanyID,
                                "lastUsedAt":  k.LastUsedAt,
                                "revokedAt":  k.RevokedAt,
                                "createdAt":   k.CreatedAt,
                        })
                }
                c.JSON(http.StatusOK, out)
        }
}

// getAgentConfigRevisions returns a history of config revisions for an agent.
// Currently returns an empty list since revision tracking is not yet implemented.
func getAgentConfigRevisions(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agentID := c.Param("agentId")
                companyID := c.Query("companyId")

                _, status, err := resolveAgentByParam(db, agentID, companyID)
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }

                c.JSON(http.StatusOK, []gin.H{})
        }
}

func pauseGlobalAgent(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agent, status, err := resolveAgentByParam(db, c.Param("agentId"), c.Query("companyId"))
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }
                var req struct{ Reason *string `json:"reason"` }
                c.ShouldBindJSON(&req)
                now := time.Now()
                updates := map[string]interface{}{
                        "status":     "paused",
                        "paused_at":  now,
                        "updated_at": now,
                }
                if req.Reason != nil {
                        updates["pause_reason"] = *req.Reason
                }
                db.Model(&models.Agent{}).Where("id = ?", agent.ID).Updates(updates)
                db.First(agent, "id = ?", agent.ID)
                c.JSON(http.StatusOK, agent)
        }
}

func resumeGlobalAgent(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agent, status, err := resolveAgentByParam(db, c.Param("agentId"), c.Query("companyId"))
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }
                db.Model(&models.Agent{}).Where("id = ?", agent.ID).Updates(map[string]interface{}{
                        "status":       "idle",
                        "paused_at":    nil,
                        "pause_reason": nil,
                        "updated_at":   time.Now(),
                })
                db.First(agent, "id = ?", agent.ID)
                c.JSON(http.StatusOK, agent)
        }
}

// wakeupGlobalAgent handles POST /api/agents/:agentId/wakeup
// Creates a wakeup request that the heartbeat service will pick up on its next tick.
func wakeupGlobalAgent(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agent, status, err := resolveAgentByParam(db, c.Param("agentId"), c.Query("companyId"))
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }
                var req struct {
                        Reason *string `json:"reason"`
                }
                c.ShouldBindJSON(&req)
                wakeup := models.AgentWakeupRequest{
                        ID:        uuid.NewString(),
                        AgentID:   agent.ID,
                        CompanyID: agent.CompanyID,
                        Reason:    req.Reason,
                        CreatedAt: time.Now(),
                }
                db.Create(&wakeup)
                c.JSON(http.StatusOK, wakeup)
        }
}

func updateAgentPermissions(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                agent, status, err := resolveAgentByParam(db, c.Param("agentId"), c.Query("companyId"))
                if err != nil {
                        c.JSON(status, gin.H{"error": err.Error()})
                        return
                }

                var req struct {
                        CanCreateAgents *bool `json:"canCreateAgents"`
                        CanAssignTasks  *bool `json:"canAssignTasks"`
                }
                if err := c.ShouldBindJSON(&req); err != nil {
                        c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
                        return
                }

                perms := models.JSON{}
                if agent.Permissions != nil {
                        for k, v := range agent.Permissions {
                                perms[k] = v
                        }
                }
                if req.CanCreateAgents != nil {
                        perms["canCreateAgents"] = *req.CanCreateAgents
                }
                if req.CanAssignTasks != nil {
                        perms["canAssignTasks"] = *req.CanAssignTasks
                }

                // Auto-load MariaDB and NanoClip Integration skills when elevated permissions are granted.
                canCreate, _ := perms["canCreateAgents"].(bool)
                canAssign, _ := perms["canAssignTasks"].(bool)
                agentUpdates := map[string]interface{}{
                        "permissions": perms,
                        "updated_at":  time.Now(),
                }
                if canCreate || canAssign {
                        skillKeys := upsertPermissionSkills(db, agent.CompanyID)
                        cfg := models.JSON{}
                        for k, v := range agent.AdapterConfig {
                                cfg[k] = v
                        }
                        existing := []interface{}{}
                        if raw, ok := cfg["desiredSkills"]; ok {
                                if sl, ok := raw.([]interface{}); ok {
                                        existing = sl
                                }
                        }
                        cfg["desiredSkills"] = mergeDesiredSkills(existing, skillKeys)
                        agentUpdates["adapter_config"] = cfg
                }

                db.Model(&models.Agent{}).Where("id = ?", agent.ID).Updates(agentUpdates)
                db.First(agent, "id = ?", agent.ID)

                actor := mw.GetActor(c)
                logActivity(db, agent.CompanyID, actor, "updated", "agent", agent.ID, nil)
                c.JSON(http.StatusOK, wrapAgent(agent))
        }
}
