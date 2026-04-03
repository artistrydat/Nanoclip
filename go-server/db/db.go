package db

import (
        "log"
        "os"
        "path/filepath"
        "time"

        "gorm.io/driver/mysql"
        "gorm.io/driver/sqlite"
        "gorm.io/gorm"
        "gorm.io/gorm/logger"
        "paperclip-go/models"
)

var DB *gorm.DB

func Connect(dsn string) *gorm.DB {
        newLogger := logger.New(
                log.New(os.Stdout, "\r\n", log.LstdFlags),
                logger.Config{
                        SlowThreshold:             time.Second,
                        LogLevel:                  logger.Warn,
                        IgnoreRecordNotFoundError: true,
                        Colorful:                  true,
                },
        )
        cfg := &gorm.Config{Logger: newLogger}

        var db *gorm.DB
        var err error

        if dsn == "" || dsn == "sqlite" {
                dbPath := sqliteDBPath()
                log.Printf("[db] using SQLite at %s", dbPath)
                db, err = gorm.Open(sqlite.Open(dbPath), cfg)
        } else {
                log.Printf("[db] connecting to MariaDB...")
                db, err = gorm.Open(mysql.Open(dsn), cfg)
        }

        if err != nil {
                log.Fatalf("[db] connection failed: %v", err)
        }

        sqlDB, _ := db.DB()
        sqlDB.SetMaxIdleConns(5)
        sqlDB.SetMaxOpenConns(25)
        sqlDB.SetConnMaxLifetime(5 * time.Minute)

        DB = db
        return db
}

func sqliteDBPath() string {
        dir := os.Getenv("NANOCLIP_DATA_DIR")
        if dir == "" {
                dir = os.Getenv("PAPERCLIP_DATA_DIR")
        }
        home, _ := os.UserHomeDir()
        if dir == "" {
                dir = filepath.Join(home, ".nanoclip")
        }
        os.MkdirAll(dir, 0755)
        newPath := filepath.Join(dir, "nanoclip.db")
        // Migrate from old location if new DB doesn't exist yet
        if _, err := os.Stat(newPath); os.IsNotExist(err) {
                oldDir := filepath.Join(home, ".paperclip-go")
                oldPath := filepath.Join(oldDir, "paperclip.db")
                if data, err := os.ReadFile(oldPath); err == nil {
                        os.WriteFile(newPath, data, 0644)
                }
        }
        return newPath
}

func AutoMigrate(db *gorm.DB) {
        err := db.AutoMigrate(
                // Auth
                &models.User{},
                &models.Session{},
                &models.Account{},
                // Instance
                &models.InstanceUserRole{},
                &models.InstanceSetting{},
                // Company
                &models.Company{},
                &models.CompanyMembership{},
                // Agent
                &models.Agent{},
                &models.AgentAPIKey{},
                &models.BoardAPIKey{},
                &models.AgentWakeupRequest{},
                // Project / Goal
                &models.Project{},
                &models.ProjectWorkspace{},
                &models.Goal{},
                // Issue
                &models.Issue{},
                &models.IssueComment{},
                &models.IssueAttachment{},
                &models.Label{},
                &models.IssueLabel{},
                // Runs
                &models.HeartbeatRun{},
                &models.HeartbeatRunEvent{},
                // Cost
                &models.CostEvent{},
                // Approvals
                &models.Approval{},
                &models.ApprovalComment{},
                // Activity
                &models.ActivityLog{},
                // Routines
                &models.Routine{},
                &models.RoutineTrigger{},
                // Secrets / Skills
                &models.CompanySecret{},
                &models.CompanySkill{},
                // Workspaces
                &models.ExecutionWorkspace{},
                &models.WorkspaceOperation{},
                // Assets
                &models.Asset{},
                // Access
                &models.Invite{},
                // Inbox
                &models.InboxItem{},
                // Plugins
                &models.Plugin{},
                &models.PluginLog{},
                // Agent instruction files (DB-stored, replaces on-disk bundle)
                &models.AgentInstructionFile{},
                // MCP server configurations per agent
                &models.AgentMCPServer{},
        )
        if err != nil {
                log.Fatalf("[db] AutoMigrate failed: %v", err)
        }
        log.Println("[db] migrations applied")
}
