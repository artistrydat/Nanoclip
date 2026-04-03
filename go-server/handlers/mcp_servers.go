package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"paperclip-go/models"
)

// ListAgentMCPServers returns all MCP server configs for an agent.
// GET /api/agents/:agentId/mcp-servers?companyId=...
func ListAgentMCPServers(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		agent, status, err := resolveAgentByParam(db, c.Param("agentId"), c.Query("companyId"))
		if err != nil {
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		var servers []models.AgentMCPServer
		db.Where("agent_id = ?", agent.ID).Order("created_at asc").Find(&servers)
		c.JSON(http.StatusOK, servers)
	}
}

// CreateAgentMCPServer adds a new MCP server configuration.
// POST /api/agents/:agentId/mcp-servers?companyId=...
func CreateAgentMCPServer(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		agent, status, err := resolveAgentByParam(db, c.Param("agentId"), c.Query("companyId"))
		if err != nil {
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		var req struct {
			Name      string      `json:"name" binding:"required"`
			Transport string      `json:"transport"`
			Command   *string     `json:"command"`
			URL       *string     `json:"url"`
			Env       models.JSON `json:"env"`
			Enabled   *bool       `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
			return
		}

		transport := req.Transport
		if transport == "" {
			transport = "stdio"
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		srv := models.AgentMCPServer{
			ID:        uuid.NewString(),
			AgentID:   agent.ID,
			CompanyID: agent.CompanyID,
			Name:      req.Name,
			Transport: transport,
			Command:   req.Command,
			URL:       req.URL,
			Env:       req.Env,
			Enabled:   enabled,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := db.Create(&srv).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save MCP server"})
			return
		}
		c.JSON(http.StatusCreated, srv)
	}
}

// UpdateAgentMCPServer patches an MCP server config (enabled toggle, command, etc.).
// PATCH /api/agents/:agentId/mcp-servers/:serverId?companyId=...
func UpdateAgentMCPServer(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		agent, status, err := resolveAgentByParam(db, c.Param("agentId"), c.Query("companyId"))
		if err != nil {
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		serverID := c.Param("serverId")
		var srv models.AgentMCPServer
		if err := db.Where("id = ? AND agent_id = ?", serverID, agent.ID).First(&srv).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "MCP server not found"})
			return
		}

		var req struct {
			Name      *string     `json:"name"`
			Transport *string     `json:"transport"`
			Command   *string     `json:"command"`
			URL       *string     `json:"url"`
			Env       models.JSON `json:"env"`
			Enabled   *bool       `json:"enabled"`
		}
		c.ShouldBindJSON(&req)

		updates := map[string]interface{}{"updated_at": time.Now()}
		if req.Name != nil {
			updates["name"] = *req.Name
		}
		if req.Transport != nil {
			updates["transport"] = *req.Transport
		}
		if req.Command != nil {
			updates["command"] = *req.Command
		}
		if req.URL != nil {
			updates["url"] = *req.URL
		}
		if req.Env != nil {
			updates["env"] = req.Env
		}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
		}

		db.Model(&srv).Updates(updates)
		db.First(&srv, "id = ?", srv.ID)
		c.JSON(http.StatusOK, srv)
	}
}

// DeleteAgentMCPServer removes an MCP server configuration.
// DELETE /api/agents/:agentId/mcp-servers/:serverId?companyId=...
func DeleteAgentMCPServer(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		agent, status, err := resolveAgentByParam(db, c.Param("agentId"), c.Query("companyId"))
		if err != nil {
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}

		serverID := c.Param("serverId")
		result := db.Where("id = ? AND agent_id = ?", serverID, agent.ID).Delete(&models.AgentMCPServer{})
		if result.RowsAffected == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "MCP server not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
