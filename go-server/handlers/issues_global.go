package handlers

import (
        "fmt"
        "net/http"
        "time"

        "github.com/gin-gonic/gin"
        "github.com/google/uuid"
        "gorm.io/gorm"
        "paperclip-go/middleware"
        "paperclip-go/models"
        "paperclip-go/services"
        "paperclip-go/ws"
)

// GlobalIssueRoutes handles /api/issues/:issueId/* (cross-company, resolved by identifier or UUID)
func GlobalIssueRoutes(rg *gin.RouterGroup, db *gorm.DB, hub *ws.Hub, hb *services.HeartbeatService) {
        rg.GET("/:issueId", getIssueGlobal(db))
        rg.PATCH("/:issueId", updateIssueGlobal(db, hub))
        rg.DELETE("/:issueId", deleteIssueGlobal(db))
        rg.POST("/:issueId/cancel", cancelIssueGlobal(db, hub))
        rg.GET("/:issueId/comments", listCommentsGlobal(db))
        rg.POST("/:issueId/comments", addCommentGlobal(db, hb))
        rg.GET("/:issueId/approvals", listApprovalsGlobal(db))
        rg.POST("/:issueId/approvals", func(c *gin.Context) { c.JSON(http.StatusOK, []any{}) })
        rg.GET("/:issueId/attachments", listAttachmentsGlobal(db))
        rg.GET("/:issueId/activity", listIssueActivityGlobal(db))
        rg.GET("/:issueId/runs", listIssueRunsGlobal(db))
        rg.GET("/:issueId/work-products", func(c *gin.Context) { c.JSON(http.StatusOK, []any{}) })
        rg.GET("/:issueId/documents", func(c *gin.Context) { c.JSON(http.StatusOK, []any{}) })
        rg.POST("/:issueId/read", markIssueReadGlobal(db))
        rg.DELETE("/:issueId/read", markIssueUnreadGlobal(db))
        rg.POST("/:issueId/inbox-archive", archiveIssueInboxGlobal(db))
        rg.DELETE("/:issueId/inbox-archive", unarchiveIssueInboxGlobal(db))
        rg.GET("/:issueId/live-runs", getIssueLiveRuns(db))
        rg.GET("/:issueId/active-run", getIssueActiveRun(db))
        rg.POST("/:issueId/sub-issues", createSubIssueGlobal(db, hub))
        rg.GET("/:issueId/sub-issues", listSubIssuesGlobal(db))
}

// resolveIssueByParam looks up an issue by human-readable identifier (e.g. "MUHA-1") or UUID.
func resolveIssueByParam(db *gorm.DB, param string) (*models.Issue, error) {
        var issue models.Issue
        // Try identifier first (contains a dash, e.g. "MUHA-1")
        if err := db.Where("identifier = ?", param).First(&issue).Error; err == nil {
                return &issue, nil
        }
        // Fall back to UUID
        if err := db.First(&issue, "id = ?", param).Error; err != nil {
                return nil, err
        }
        return &issue, nil
}

func getIssueGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                c.JSON(http.StatusOK, issue)
        }
}

func updateIssueGlobal(db *gorm.DB, hub *ws.Hub) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                var req updateIssueRequest
                c.ShouldBindJSON(&req)

                updates := map[string]interface{}{"updated_at": time.Now()}
                if req.Title != nil {
                        updates["title"] = *req.Title
                }
                if req.Description != nil {
                        updates["description"] = req.Description
                }
                if req.Status != nil {
                        updates["status"] = *req.Status
                        now := time.Now()
                        switch *req.Status {
                        case "in_progress":
                                if issue.StartedAt == nil {
                                        updates["started_at"] = now
                                }
                        case "done":
                                updates["completed_at"] = now
                        case "cancelled":
                                updates["cancelled_at"] = now
                        }
                }
                if req.Priority != nil {
                        updates["priority"] = *req.Priority
                }
                if req.AssigneeAgentID != nil {
                        updates["assignee_agent_id"] = req.AssigneeAgentID
                }
                if req.AssigneeUserID != nil {
                        updates["assignee_user_id"] = req.AssigneeUserID
                }
                if req.ProjectID != nil {
                        updates["project_id"] = req.ProjectID
                }
                if req.GoalID != nil {
                        updates["goal_id"] = req.GoalID
                }

                db.Model(issue).Updates(updates)
                db.First(issue, "id = ?", issue.ID)

                hub.Publish(ws.LiveEvent{Type: "issue.updated", Payload: issue})
                actor := middleware.GetActor(c)
                logActivity(db, issue.CompanyID, actor, "updated", "issue", issue.ID, nil)
                c.JSON(http.StatusOK, issue)
        }
}

func deleteIssueGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                now := time.Now()
                db.Model(issue).Updates(map[string]interface{}{"hidden_at": now, "updated_at": now})
                c.JSON(http.StatusOK, gin.H{"success": true})
        }
}

func cancelIssueGlobal(db *gorm.DB, hub *ws.Hub) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                now := time.Now()
                db.Model(issue).Updates(map[string]interface{}{
                        "status":       "cancelled",
                        "cancelled_at": now,
                        "updated_at":   now,
                })
                db.First(issue, "id = ?", issue.ID)
                hub.Publish(ws.LiveEvent{Type: "issue.updated", Payload: issue})
                c.JSON(http.StatusOK, issue)
        }
}

func listCommentsGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusOK, []any{})
                        return
                }
                var comments []models.IssueComment
                db.Where("issue_id = ?", issue.ID).Order("created_at asc").Find(&comments)
                c.JSON(http.StatusOK, comments)
        }
}

func addCommentGlobal(db *gorm.DB, hb *services.HeartbeatService) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                var req struct {
                        Body string `json:"body" binding:"required"`
                }
                if err := c.ShouldBindJSON(&req); err != nil {
                        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
                        return
                }
                actor := middleware.GetActor(c)
                comment := models.IssueComment{
                        ID:        uuid.NewString(),
                        CompanyID: issue.CompanyID,
                        IssueID:   issue.ID,
                        Body:      req.Body,
                        CreatedAt: time.Now(),
                        UpdatedAt: time.Now(),
                }
                fromUser := false
                if actor != nil {
                        if actor.Type == "user" {
                                comment.AuthorUserID = &actor.UserID
                                fromUser = true
                        } else if actor.Type == "agent" {
                                comment.AuthorAgentID = &actor.AgentID
                        }
                }
                db.Create(&comment)
                c.JSON(http.StatusCreated, comment)

                // Trigger agent run when a user posts a comment and the issue has an assigned agent
                if fromUser && hb != nil && issue.AssigneeAgentID != nil && *issue.AssigneeAgentID != "" {
                        go hb.TriggerRun(*issue.AssigneeAgentID, issue.CompanyID, issue)
                }
        }
}

func listApprovalsGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                // Approvals model doesn't have a direct issue_id field in this implementation.
                // Return empty list; future iterations can store approvals with issue references.
                c.JSON(http.StatusOK, []any{})
        }
}

func listAttachmentsGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusOK, []any{})
                        return
                }
                var attachments []models.IssueAttachment
                db.Where("issue_id = ?", issue.ID).Order("created_at desc").Find(&attachments)
                c.JSON(http.StatusOK, attachments)
        }
}

func listIssueActivityGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusOK, []any{})
                        return
                }
                var logs []models.ActivityLog
                db.Where("entity_type = ? AND entity_id = ?", "issue", issue.ID).
                        Order("created_at desc").Limit(100).Find(&logs)
                c.JSON(http.StatusOK, logs)
        }
}

func listIssueRunsGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusOK, []any{})
                        return
                }
                var runs []models.HeartbeatRun
                db.Where("issue_id = ?", issue.ID).Order("created_at desc").Find(&runs)
                out := make([]gin.H, 0, len(runs))
                for _, r := range runs {
                        out = append(out, gin.H{
                                "id":               r.ID,
                                "runId":            r.ID,
                                "companyId":        r.CompanyID,
                                "agentId":          r.AgentID,
                                "issueId":          r.IssueID,
                                "invocationSource": r.InvocationSource,
                                "triggerDetail":    r.TriggerDetail,
                                "status":           r.Status,
                                "startedAt":        r.StartedAt,
                                "completedAt":      r.CompletedAt,
                                "finishedAt":       r.CompletedAt,
                                "exitCode":         r.ExitCode,
                                "stdoutExcerpt":    r.StdoutExcerpt,
                                "stderrExcerpt":    r.StderrExcerpt,
                                "usageJson":        r.UsageJSON,
                                "resultJson":       r.ResultJSON,
                                "createdAt":        r.CreatedAt,
                                "updatedAt":        r.UpdatedAt,
                        })
                }
                c.JSON(http.StatusOK, out)
        }
}

func markIssueReadGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                now := time.Now()
                db.Model(&models.InboxItem{}).Where("issue_id = ?", issue.ID).
                        Updates(map[string]interface{}{"status": "read", "read_at": now, "updated_at": now})
                c.JSON(http.StatusOK, gin.H{"id": issue.ID, "lastReadAt": now})
        }
}

func markIssueUnreadGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                db.Model(&models.InboxItem{}).Where("issue_id = ?", issue.ID).
                        Updates(map[string]interface{}{"status": "unread", "read_at": nil, "updated_at": time.Now()})
                c.JSON(http.StatusOK, gin.H{"id": issue.ID, "removed": true})
        }
}

func archiveIssueInboxGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                now := time.Now()
                db.Model(&models.InboxItem{}).Where("issue_id = ?", issue.ID).
                        Updates(map[string]interface{}{"status": "archived", "updated_at": now})
                c.JSON(http.StatusOK, gin.H{"id": issue.ID, "archivedAt": now})
        }
}

func unarchiveIssueInboxGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issue, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "issue not found"})
                        return
                }
                db.Model(&models.InboxItem{}).Where("issue_id = ?", issue.ID).
                        Updates(map[string]interface{}{"status": "unread", "updated_at": time.Now()})
                c.JSON(http.StatusOK, gin.H{"ok": true})
        }
}

func getIssueLiveRuns(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issueId := c.Param("issueId")
                // Resolve to UUID if identifier was passed
                if issue, err := resolveIssueByParam(db, issueId); err == nil {
                        issueId = issue.ID
                }
                var runs []models.HeartbeatRun
                db.Where("issue_id = ? AND status IN ('queued','running')", issueId).
                        Order("created_at desc").Find(&runs)
                c.JSON(http.StatusOK, runs)
        }
}

func getIssueActiveRun(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                issueId := c.Param("issueId")
                if issue, err := resolveIssueByParam(db, issueId); err == nil {
                        issueId = issue.ID
                }
                var run models.HeartbeatRun
                if err := db.Where("issue_id = ? AND status IN ('queued','running')", issueId).
                        Order("created_at desc").First(&run).Error; err != nil {
                        c.JSON(http.StatusOK, nil)
                        return
                }
                c.JSON(http.StatusOK, run)
        }
}

type createSubIssueRequest struct {
        Title           string  `json:"title" binding:"required"`
        Description     *string `json:"description"`
        Status          *string `json:"status"`
        Priority        *string `json:"priority"`
        AssigneeAgentID *string `json:"assigneeAgentId"`
        AssigneeUserID  *string `json:"assigneeUserId"`
}

func createSubIssueGlobal(db *gorm.DB, hub *ws.Hub) gin.HandlerFunc {
        return func(c *gin.Context) {
                parent, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusNotFound, gin.H{"error": "parent issue not found"})
                        return
                }

                var req createSubIssueRequest
                if err := c.ShouldBindJSON(&req); err != nil {
                        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
                        return
                }

                var company models.Company
                db.First(&company, "id = ?", parent.CompanyID)
                db.Model(&company).Update("issue_counter", gorm.Expr("issue_counter + 1"))
                db.First(&company, "id = ?", parent.CompanyID)
                issueNumber := company.IssueCounter
                identifier := fmt.Sprintf("%s-%d", company.IssuePrefix, issueNumber)

                status := "backlog"
                if req.Status != nil {
                        status = *req.Status
                }
                priority := "medium"
                if req.Priority != nil {
                        priority = *req.Priority
                }

                actor := middleware.GetActor(c)
                issue := models.Issue{
                        ID:              uuid.NewString(),
                        CompanyID:       parent.CompanyID,
                        ProjectID:       parent.ProjectID,
                        ParentID:        &parent.ID,
                        Title:           req.Title,
                        Description:     req.Description,
                        Status:          status,
                        Priority:        priority,
                        AssigneeAgentID: req.AssigneeAgentID,
                        AssigneeUserID:  req.AssigneeUserID,
                        IssueNumber:     &issueNumber,
                        Identifier:      &identifier,
                        OriginKind:      "sub_issue",
                        CreatedAt:       time.Now(),
                        UpdatedAt:       time.Now(),
                }
                if actor != nil {
                        if actor.Type == "user" {
                                issue.CreatedByUserID = &actor.UserID
                        } else if actor.Type == "agent" {
                                issue.CreatedByAgentID = &actor.AgentID
                        }
                }

                if err := db.Create(&issue).Error; err != nil {
                        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
                        return
                }
                if hub != nil {
                        hub.Publish(ws.LiveEvent{Type: "issue.created", Payload: issue})
                }
                c.JSON(http.StatusCreated, issue)
        }
}

func listSubIssuesGlobal(db *gorm.DB) gin.HandlerFunc {
        return func(c *gin.Context) {
                parent, err := resolveIssueByParam(db, c.Param("issueId"))
                if err != nil {
                        c.JSON(http.StatusOK, []any{})
                        return
                }
                var issues []models.Issue
                db.Where("parent_id = ?", parent.ID).Order("created_at asc").Find(&issues)
                c.JSON(http.StatusOK, issues)
        }
}
