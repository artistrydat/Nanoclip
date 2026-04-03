package models

import (
        "database/sql/driver"
        "encoding/json"
        "errors"
        "time"
)

// JSON is a helper type for storing JSON in SQLite/MariaDB TEXT columns.
type JSON map[string]interface{}

// JSONAny stores any JSON value (object, array, primitive) as a raw byte blob.
type JSONAny []byte

func (j JSONAny) MarshalJSON() ([]byte, error) {
        if j == nil {
                return []byte("null"), nil
        }
        return j, nil
}

func (j *JSONAny) UnmarshalJSON(data []byte) error {
        *j = append((*j)[:0], data...)
        return nil
}

func (j JSONAny) Value() (driver.Value, error) {
        if j == nil {
                return nil, nil
        }
        return string(j), nil
}

func (j *JSONAny) Scan(value interface{}) error {
        if value == nil {
                *j = nil
                return nil
        }
        switch v := value.(type) {
        case string:
                *j = []byte(v)
        case []byte:
                *j = append((*j)[:0], v...)
        }
        return nil
}

func (j JSON) Value() (driver.Value, error) {
        if j == nil {
                return nil, nil
        }
        b, err := json.Marshal(j)
        return string(b), err
}

func (j *JSON) Scan(src interface{}) error {
        var bytes []byte
        switch v := src.(type) {
        case []byte:
                bytes = v
        case string:
                bytes = []byte(v)
        case nil:
                *j = nil
                return nil
        default:
                return errors.New("unsupported type for JSON scan")
        }
        return json.Unmarshal(bytes, j)
}

// JSONArr is a helper type for JSON arrays in SQLite/MariaDB TEXT columns.
type JSONArr []interface{}

func (j JSONArr) Value() (driver.Value, error) {
        if j == nil {
                return "[]", nil
        }
        b, err := json.Marshal(j)
        return string(b), err
}

func (j *JSONArr) Scan(src interface{}) error {
        var bytes []byte
        switch v := src.(type) {
        case []byte:
                bytes = v
        case string:
                bytes = []byte(v)
        case nil:
                *j = []interface{}{}
                return nil
        default:
                return errors.New("unsupported type for JSONArr scan")
        }
        return json.Unmarshal(bytes, j)
}

// ─── Auth ────────────────────────────────────────────────────────────────────

type User struct {
        ID            string    `gorm:"primaryKey;type:char(36)" json:"id"`
        Name          string    `gorm:"not null" json:"name"`
        Email         string    `gorm:"uniqueIndex;not null" json:"email"`
        EmailVerified bool      `gorm:"default:false" json:"emailVerified"`
        Image         *string   `json:"image,omitempty"`
        CreatedAt     time.Time `json:"createdAt"`
        UpdatedAt     time.Time `json:"updatedAt"`
}

type Session struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        Token     string    `gorm:"uniqueIndex;not null;type:varchar(512)" json:"token"`
        UserID    string    `gorm:"not null;type:char(36);index" json:"userId"`
        User      *User     `gorm:"foreignKey:UserID" json:"user,omitempty"`
        ExpiresAt time.Time `json:"expiresAt"`
        IPAddress *string   `json:"ipAddress,omitempty"`
        UserAgent *string   `json:"userAgent,omitempty"`
        CreatedAt time.Time `json:"createdAt"`
        UpdatedAt time.Time `json:"updatedAt"`
}

type Account struct {
        ID         string    `gorm:"primaryKey;type:char(36)" json:"id"`
        AccountID  string    `gorm:"not null" json:"accountId"`
        ProviderID string    `gorm:"not null" json:"providerId"`
        UserID     string    `gorm:"not null;type:char(36);index" json:"userId"`
        Password   *string   `gorm:"type:varchar(255)" json:"-"`
        CreatedAt  time.Time `json:"createdAt"`
        UpdatedAt  time.Time `json:"updatedAt"`
}

// ─── Instance ─────────────────────────────────────────────────────────────────

type InstanceUserRole struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        UserID    string    `gorm:"not null;type:char(36);index" json:"userId"`
        Role      string    `gorm:"not null;type:varchar(64)" json:"role"`
        CreatedAt time.Time `json:"createdAt"`
        UpdatedAt time.Time `json:"updatedAt"`
}

// InstanceSetting is a key-value store for instance-wide settings, organized by section.
type InstanceSetting struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        Section   string    `gorm:"not null;type:varchar(64);index" json:"section"`
        Key       string    `gorm:"not null;type:varchar(128)" json:"key"`
        Value     string    `gorm:"type:longtext" json:"value"`
        CreatedAt time.Time `json:"createdAt"`
        UpdatedAt time.Time `json:"updatedAt"`
}

// ─── Company ─────────────────────────────────────────────────────────────────

type Company struct {
        ID                              string     `gorm:"primaryKey;type:char(36)" json:"id"`
        Name                            string     `gorm:"not null;type:varchar(255)" json:"name"`
        Description                     *string    `gorm:"type:text" json:"description,omitempty"`
        Status                          string     `gorm:"not null;default:'active';type:varchar(32)" json:"status"`
        PauseReason                     *string    `gorm:"type:text" json:"pauseReason,omitempty"`
        PausedAt                        *time.Time `json:"pausedAt,omitempty"`
        ArchivedAt                      *time.Time `json:"archivedAt,omitempty"`
        IssuePrefix                     string     `gorm:"not null;default:'PAP';uniqueIndex;type:varchar(16)" json:"issuePrefix"`
        IssueCounter                    int        `gorm:"not null;default:0" json:"issueCounter"`
        BudgetMonthlyCents              int        `gorm:"not null;default:0" json:"budgetMonthlyCents"`
        SpentMonthlyCents               int        `gorm:"not null;default:0" json:"spentMonthlyCents"`
        RequireBoardApprovalForNewAgents bool      `gorm:"not null;default:true" json:"requireBoardApprovalForNewAgents"`
        BrandColor                      *string    `gorm:"type:varchar(32)" json:"brandColor,omitempty"`
        LogoURL                         *string    `gorm:"type:varchar(512)" json:"logoUrl,omitempty"`
        CreatedAt                       time.Time  `json:"createdAt"`
        UpdatedAt                       time.Time  `json:"updatedAt"`
}

type CompanyMembership struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID string    `gorm:"not null;type:char(36);index" json:"companyId"`
        UserID    string    `gorm:"not null;type:char(36);index" json:"userId"`
        Role      string    `gorm:"not null;default:'member';type:varchar(32)" json:"role"`
        CreatedAt time.Time `json:"createdAt"`
        UpdatedAt time.Time `json:"updatedAt"`
}

// ─── Agent ───────────────────────────────────────────────────────────────────

type Agent struct {
        ID                 string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID          string     `gorm:"not null;type:char(36);index" json:"companyId"`
        Name               string     `gorm:"not null;type:varchar(255)" json:"name"`
        Role               string     `gorm:"not null;default:'general';type:varchar(64)" json:"role"`
        Title              *string    `gorm:"type:varchar(255)" json:"title,omitempty"`
        Icon               *string    `gorm:"type:varchar(128)" json:"icon,omitempty"`
        Status             string     `gorm:"not null;default:'idle';type:varchar(32)" json:"status"`
        ReportsTo          *string    `gorm:"type:char(36);index" json:"reportsTo,omitempty"`
        Capabilities       *string    `gorm:"type:text" json:"capabilities,omitempty"`
        AdapterType        string     `gorm:"not null;default:'process';type:varchar(64)" json:"adapterType"`
        AdapterConfig      JSON       `gorm:"type:longtext" json:"adapterConfig"`
        RuntimeConfig      JSON       `gorm:"type:longtext" json:"runtimeConfig"`
        BudgetMonthlyCents int        `gorm:"not null;default:0" json:"budgetMonthlyCents"`
        SpentMonthlyCents  int        `gorm:"not null;default:0" json:"spentMonthlyCents"`
        PauseReason        *string    `gorm:"type:text" json:"pauseReason,omitempty"`
        PausedAt           *time.Time `json:"pausedAt,omitempty"`
        Permissions        JSON       `gorm:"type:longtext" json:"permissions"`
        LastHeartbeatAt    *time.Time `json:"lastHeartbeatAt,omitempty"`
        Metadata           JSON       `gorm:"type:longtext" json:"metadata,omitempty"`
        CreatedAt          time.Time  `json:"createdAt"`
        UpdatedAt          time.Time  `json:"updatedAt"`
}

type AgentAPIKey struct {
        ID         string     `gorm:"primaryKey;type:char(36)" json:"id"`
        AgentID    string     `gorm:"not null;type:char(36);index" json:"agentId"`
        CompanyID  string     `gorm:"not null;type:char(36);index" json:"companyId"`
        KeyHash    string     `gorm:"not null;type:varchar(255)" json:"-"`
        Label      *string    `gorm:"type:varchar(255)" json:"label,omitempty"`
        LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
        RevokedAt  *time.Time `json:"revokedAt,omitempty"`
        CreatedAt  time.Time  `json:"createdAt"`
}

type BoardAPIKey struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID string    `gorm:"not null;type:char(36);index" json:"companyId"`
        KeyHash   string    `gorm:"not null;type:varchar(255)" json:"-"`
        Label     *string   `gorm:"type:varchar(255)" json:"label,omitempty"`
        CreatedAt time.Time `json:"createdAt"`
}

type AgentWakeupRequest struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        AgentID   string    `gorm:"not null;type:char(36);index" json:"agentId"`
        CompanyID string    `gorm:"not null;type:char(36);index" json:"companyId"`
        Reason    *string   `gorm:"type:text" json:"reason,omitempty"`
        CreatedAt time.Time `json:"createdAt"`
}

// ─── Project ──────────────────────────────────────────────────────────────────

type Project struct {
        ID          string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID   string     `gorm:"not null;type:char(36);index" json:"companyId"`
        GoalID      *string    `gorm:"type:char(36);index" json:"goalId,omitempty"`
        Name        string     `gorm:"not null;type:varchar(255)" json:"name"`
        Description *string    `gorm:"type:text" json:"description,omitempty"`
        Status      string     `gorm:"not null;default:'backlog';type:varchar(32)" json:"status"`
        LeadAgentID *string    `gorm:"type:char(36)" json:"leadAgentId,omitempty"`
        TargetDate  *string    `gorm:"type:date" json:"targetDate,omitempty"`
        Color       *string    `gorm:"type:varchar(32)" json:"color,omitempty"`
        ArchivedAt  *time.Time `json:"archivedAt,omitempty"`
        CreatedAt   time.Time  `json:"createdAt"`
        UpdatedAt   time.Time  `json:"updatedAt"`
}

// ProjectWorkspace represents a named workspace slot within a project.
type ProjectWorkspace struct {
        ID          string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID   string    `gorm:"not null;type:char(36);index" json:"companyId"`
        ProjectID   string    `gorm:"not null;type:char(36);index" json:"projectId"`
        Name        string    `gorm:"not null;type:varchar(255)" json:"name"`
        Description *string   `gorm:"type:text" json:"description,omitempty"`
        RepoURL     *string   `gorm:"type:varchar(512)" json:"repoUrl,omitempty"`
        Branch      *string   `gorm:"type:varchar(255)" json:"branch,omitempty"`
        CreatedAt   time.Time `json:"createdAt"`
        UpdatedAt   time.Time `json:"updatedAt"`
}

// ─── Goal ─────────────────────────────────────────────────────────────────────

type Goal struct {
        ID           string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID    string    `gorm:"not null;type:char(36);index" json:"companyId"`
        Title        string    `gorm:"not null;type:varchar(512)" json:"title"`
        Description  *string   `gorm:"type:text" json:"description,omitempty"`
        Level        string    `gorm:"not null;default:'task';type:varchar(32)" json:"level"`
        Status       string    `gorm:"not null;default:'planned';type:varchar(32)" json:"status"`
        ParentID     *string   `gorm:"type:char(36);index" json:"parentId,omitempty"`
        OwnerAgentID *string   `gorm:"type:char(36)" json:"ownerAgentId,omitempty"`
        CreatedAt    time.Time `json:"createdAt"`
        UpdatedAt    time.Time `json:"updatedAt"`
}

// ─── Issue ────────────────────────────────────────────────────────────────────

type Issue struct {
        ID              string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID       string     `gorm:"not null;type:char(36);index:issues_company_status" json:"companyId"`
        ProjectID       *string    `gorm:"type:char(36);index" json:"projectId,omitempty"`
        GoalID          *string    `gorm:"type:char(36);index" json:"goalId,omitempty"`
        ParentID        *string    `gorm:"type:char(36);index" json:"parentId,omitempty"`
        Title           string     `gorm:"not null;type:varchar(512)" json:"title"`
        Description     *string    `gorm:"type:longtext" json:"description,omitempty"`
        Status          string     `gorm:"not null;default:'backlog';type:varchar(32);index:issues_company_status" json:"status"`
        Priority        string     `gorm:"not null;default:'medium';type:varchar(32)" json:"priority"`
        AssigneeAgentID *string    `gorm:"type:char(36);index" json:"assigneeAgentId,omitempty"`
        AssigneeUserID  *string    `gorm:"type:varchar(255);index" json:"assigneeUserId,omitempty"`
        CreatedByAgentID *string   `gorm:"type:char(36)" json:"createdByAgentId,omitempty"`
        CreatedByUserID *string    `gorm:"type:varchar(255)" json:"createdByUserId,omitempty"`
        IssueNumber     *int       `json:"issueNumber,omitempty"`
        Identifier      *string    `gorm:"type:varchar(64);uniqueIndex" json:"identifier,omitempty"`
        OriginKind      string     `gorm:"not null;default:'manual';type:varchar(64)" json:"originKind"`
        BillingCode     *string    `gorm:"type:varchar(255)" json:"billingCode,omitempty"`
        RequestDepth    int        `gorm:"not null;default:0" json:"requestDepth"`
        StartedAt       *time.Time `json:"startedAt,omitempty"`
        CompletedAt     *time.Time `json:"completedAt,omitempty"`
        CancelledAt     *time.Time `json:"cancelledAt,omitempty"`
        HiddenAt           *time.Time `json:"hiddenAt,omitempty"`
        ExecutionLockedAt  *time.Time `gorm:"type:datetime;index" json:"executionLockedAt,omitempty"`
        ExecutionRunID     *string    `gorm:"type:char(36);index" json:"executionRunId,omitempty"`
        CreatedAt          time.Time  `json:"createdAt"`
        UpdatedAt          time.Time  `json:"updatedAt"`
}

type IssueComment struct {
        ID            string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID     string    `gorm:"not null;type:char(36);index" json:"companyId"`
        IssueID       string    `gorm:"not null;type:char(36);index" json:"issueId"`
        AuthorAgentID *string   `gorm:"type:char(36)" json:"authorAgentId,omitempty"`
        AuthorUserID  *string   `gorm:"type:varchar(255)" json:"authorUserId,omitempty"`
        Body          string    `gorm:"not null;type:longtext" json:"body"`
        CreatedAt     time.Time `json:"createdAt"`
        UpdatedAt     time.Time `json:"updatedAt"`
}

type IssueAttachment struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID string    `gorm:"not null;type:char(36);index" json:"companyId"`
        IssueID   string    `gorm:"not null;type:char(36);index" json:"issueId"`
        Filename  string    `gorm:"not null;type:varchar(255)" json:"filename"`
        MimeType  string    `gorm:"not null;type:varchar(128)" json:"mimeType"`
        SizeBytes int64     `gorm:"not null;default:0" json:"sizeBytes"`
        StorePath string    `gorm:"not null;type:varchar(512)" json:"storePath"`
        CreatedAt time.Time `json:"createdAt"`
}

type Label struct {
        ID          string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID   string    `gorm:"not null;type:char(36);index" json:"companyId"`
        Name        string    `gorm:"not null;type:varchar(128)" json:"name"`
        Color       string    `gorm:"not null;default:'#6b7280';type:varchar(32)" json:"color"`
        Description *string   `gorm:"type:text" json:"description,omitempty"`
        CreatedAt   time.Time `json:"createdAt"`
        UpdatedAt   time.Time `json:"updatedAt"`
}

type IssueLabel struct {
        IssueID   string    `gorm:"primaryKey;type:char(36)" json:"issueId"`
        LabelID   string    `gorm:"primaryKey;type:char(36)" json:"labelId"`
        CreatedAt time.Time `json:"createdAt"`
}

// ─── HeartbeatRun ─────────────────────────────────────────────────────────────

type HeartbeatRun struct {
        ID               string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID        string     `gorm:"not null;type:char(36);index" json:"companyId"`
        AgentID          string     `gorm:"not null;type:char(36);index" json:"agentId"`
        IssueID          *string    `gorm:"type:char(36);index" json:"issueId,omitempty"`
        InvocationSource string     `gorm:"not null;default:'on_demand';type:varchar(64)" json:"invocationSource"`
        TriggerDetail    *string    `gorm:"type:varchar(255)" json:"triggerDetail,omitempty"`
        Status           string     `gorm:"not null;default:'queued';type:varchar(32);index" json:"status"`
        StartedAt        *time.Time `json:"startedAt,omitempty"`
        CompletedAt      *time.Time `json:"completedAt,omitempty"`
        Error            *string    `gorm:"type:text" json:"error,omitempty"`
        ErrorCode        *string    `gorm:"type:varchar(64)" json:"errorCode,omitempty"`
        ExitCode         *int       `json:"exitCode,omitempty"`
        ProcessPID       *int       `json:"processPid,omitempty"`
        StdoutExcerpt    *string    `gorm:"type:text" json:"stdoutExcerpt,omitempty"`
        StderrExcerpt    *string    `gorm:"type:text" json:"stderrExcerpt,omitempty"`
        UsageJSON        JSON       `gorm:"type:longtext" json:"usageJson,omitempty"`
        ResultJSON       JSON       `gorm:"type:longtext" json:"resultJson,omitempty"`
        ExternalRunID    *string    `gorm:"type:varchar(255)" json:"externalRunId,omitempty"`
        RetryOfRunID     *string    `gorm:"type:char(36)" json:"retryOfRunId,omitempty"`
        CreatedAt        time.Time  `json:"createdAt"`
        UpdatedAt        time.Time  `json:"updatedAt"`
}

// HeartbeatRunEvent stores structured events for a run.
type HeartbeatRunEvent struct {
        ID             string      `gorm:"primaryKey;type:char(36)" json:"id"`
        HeartbeatRunID string      `gorm:"not null;type:char(36);index" json:"heartbeatRunId"`
        Kind           string      `gorm:"not null;type:varchar(64)" json:"kind"`
        Summary        string      `gorm:"not null;type:longtext" json:"summary"`
        Detail         JSON        `gorm:"type:longtext" json:"detail,omitempty"`
        IssueID        *string     `gorm:"type:char(36)" json:"issueId,omitempty"`
        SequenceNumber int         `gorm:"not null;default:0" json:"sequenceNumber"`
        CreatedAt      time.Time   `gorm:"index" json:"createdAt"`
}

// ─── CostEvent ────────────────────────────────────────────────────────────────

type CostEvent struct {
        ID                string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID         string    `gorm:"not null;type:char(36);index" json:"companyId"`
        AgentID           string    `gorm:"not null;type:char(36);index" json:"agentId"`
        IssueID           *string   `gorm:"type:char(36);index" json:"issueId,omitempty"`
        ProjectID         *string   `gorm:"type:char(36)" json:"projectId,omitempty"`
        HeartbeatRunID    *string   `gorm:"type:char(36);index" json:"heartbeatRunId,omitempty"`
        BillingCode       *string   `gorm:"type:varchar(255)" json:"billingCode,omitempty"`
        Provider          string    `gorm:"not null;type:varchar(64)" json:"provider"`
        Model             string    `gorm:"not null;type:varchar(128)" json:"model"`
        InputTokens       int       `gorm:"not null;default:0" json:"inputTokens"`
        OutputTokens      int       `gorm:"not null;default:0" json:"outputTokens"`
        CostCents         int       `gorm:"not null" json:"costCents"`
        OccurredAt        time.Time `gorm:"index" json:"occurredAt"`
        CreatedAt         time.Time `json:"createdAt"`
}

// ─── Approval ─────────────────────────────────────────────────────────────────

type Approval struct {
        ID                 string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID          string     `gorm:"not null;type:char(36);index" json:"companyId"`
        Type               string     `gorm:"not null;type:varchar(64)" json:"type"`
        RequestedByAgentID *string    `gorm:"type:char(36)" json:"requestedByAgentId,omitempty"`
        RequestedByUserID  *string    `gorm:"type:varchar(255)" json:"requestedByUserId,omitempty"`
        Status             string     `gorm:"not null;default:'pending';type:varchar(32);index" json:"status"`
        Payload            JSON       `gorm:"not null;type:longtext" json:"payload"`
        DecisionNote       *string    `gorm:"type:text" json:"decisionNote,omitempty"`
        DecidedByUserID    *string    `gorm:"type:varchar(255)" json:"decidedByUserId,omitempty"`
        DecidedAt          *time.Time `json:"decidedAt,omitempty"`
        CreatedAt          time.Time  `json:"createdAt"`
        UpdatedAt          time.Time  `json:"updatedAt"`
}

type ApprovalComment struct {
        ID            string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID     string    `gorm:"not null;type:char(36);index" json:"companyId"`
        ApprovalID    string    `gorm:"not null;type:char(36);index" json:"approvalId"`
        AuthorUserID  *string   `gorm:"type:varchar(255)" json:"authorUserId,omitempty"`
        AuthorAgentID *string   `gorm:"type:char(36)" json:"authorAgentId,omitempty"`
        Body          string    `gorm:"not null;type:longtext" json:"body"`
        CreatedAt     time.Time `json:"createdAt"`
        UpdatedAt     time.Time `json:"updatedAt"`
}

// ─── ActivityLog ──────────────────────────────────────────────────────────────

type ActivityLog struct {
        ID         string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID  string    `gorm:"not null;type:char(36);index" json:"companyId"`
        ActorType  string    `gorm:"not null;default:'system';type:varchar(32)" json:"actorType"`
        ActorID    string    `gorm:"not null;type:varchar(255)" json:"actorId"`
        Action     string    `gorm:"not null;type:varchar(128);index" json:"action"`
        EntityType string    `gorm:"not null;type:varchar(64);index" json:"entityType"`
        EntityID   string    `gorm:"not null;type:varchar(255)" json:"entityId"`
        AgentID    *string   `gorm:"type:char(36)" json:"agentId,omitempty"`
        Details    JSON      `gorm:"type:longtext" json:"details,omitempty"`
        CreatedAt  time.Time `gorm:"index" json:"createdAt"`
}

// ─── Routine ──────────────────────────────────────────────────────────────────

type Routine struct {
        ID              string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID       string     `gorm:"not null;type:char(36);index" json:"companyId"`
        ProjectID       string     `gorm:"not null;type:char(36);index" json:"projectId"`
        GoalID          *string    `gorm:"type:char(36)" json:"goalId,omitempty"`
        Title           string     `gorm:"not null;type:varchar(512)" json:"title"`
        Description     *string    `gorm:"type:text" json:"description,omitempty"`
        AssigneeAgentID string     `gorm:"not null;type:char(36);index" json:"assigneeAgentId"`
        Priority        string     `gorm:"not null;default:'medium';type:varchar(32)" json:"priority"`
        Status          string     `gorm:"not null;default:'active';type:varchar(32);index" json:"status"`
        LastTriggeredAt *time.Time `json:"lastTriggeredAt,omitempty"`
        CreatedAt       time.Time  `json:"createdAt"`
        UpdatedAt       time.Time  `json:"updatedAt"`
}

type RoutineTrigger struct {
        ID             string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID      string     `gorm:"not null;type:char(36);index" json:"companyId"`
        RoutineID      string     `gorm:"not null;type:char(36);index" json:"routineId"`
        Kind           string     `gorm:"not null;type:varchar(32)" json:"kind"`
        Label          *string    `gorm:"type:varchar(255)" json:"label,omitempty"`
        Enabled        bool       `gorm:"not null;default:true" json:"enabled"`
        CronExpression *string    `gorm:"type:varchar(128)" json:"cronExpression,omitempty"`
        Timezone       *string    `gorm:"type:varchar(64)" json:"timezone,omitempty"`
        NextRunAt      *time.Time `json:"nextRunAt,omitempty"`
        CreatedAt      time.Time  `json:"createdAt"`
        UpdatedAt      time.Time  `json:"updatedAt"`
}

// ─── Secret ───────────────────────────────────────────────────────────────────

type CompanySecret struct {
        ID          string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID   string     `gorm:"not null;type:char(36);index" json:"companyId"`
        Key         string     `gorm:"not null;type:varchar(255)" json:"key"`
        Value       string     `gorm:"not null;type:longtext" json:"value"`
        Description *string    `gorm:"type:text" json:"description,omitempty"`
        Kind        string     `gorm:"not null;default:'env';type:varchar(64)" json:"kind"`
        CreatedAt   time.Time  `json:"createdAt"`
        UpdatedAt   time.Time  `json:"updatedAt"`
}

// ─── CompanySkill ─────────────────────────────────────────────────────────────

type CompanySkill struct {
        ID          string      `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID   string      `gorm:"not null;type:char(36);index" json:"companyId"`
        Name        string      `gorm:"not null;type:varchar(255)" json:"name"`
        Description *string     `gorm:"type:text" json:"description,omitempty"`
        Kind        string      `gorm:"not null;default:'document';type:varchar(32)" json:"kind"`
        Content     *string     `gorm:"type:longtext" json:"content,omitempty"`
        Config      JSON        `gorm:"type:longtext" json:"config,omitempty"`
        CreatedAt   time.Time   `json:"createdAt"`
        UpdatedAt   time.Time   `json:"updatedAt"`
}

// ─── ExecutionWorkspace ───────────────────────────────────────────────────────

type ExecutionWorkspace struct {
        ID             string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID      string     `gorm:"not null;type:char(36);index" json:"companyId"`
        AgentID        string     `gorm:"not null;type:char(36);index" json:"agentId"`
        HeartbeatRunID *string    `gorm:"type:char(36);index" json:"heartbeatRunId,omitempty"`
        IssueID        *string    `gorm:"type:char(36);index" json:"issueId,omitempty"`
        Kind           string     `gorm:"not null;default:'execution';type:varchar(32)" json:"kind"`
        Status         string     `gorm:"not null;default:'open';type:varchar(32)" json:"status"`
        WorkspacePath  *string    `gorm:"type:varchar(512)" json:"workspacePath,omitempty"`
        RepoURL        *string    `gorm:"type:varchar(512)" json:"repoUrl,omitempty"`
        Branch         *string    `gorm:"type:varchar(255)" json:"branch,omitempty"`
        Config         JSON       `gorm:"type:longtext" json:"config,omitempty"`
        Metadata       JSON       `gorm:"type:longtext" json:"metadata,omitempty"`
        ClosedAt       *time.Time `json:"closedAt,omitempty"`
        CreatedAt      time.Time  `json:"createdAt"`
        UpdatedAt      time.Time  `json:"updatedAt"`
}

// ─── WorkspaceOperation ───────────────────────────────────────────────────────

type WorkspaceOperation struct {
        ID                   string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID            string     `gorm:"not null;type:char(36);index" json:"companyId"`
        AgentID              string     `gorm:"not null;type:char(36);index" json:"agentId"`
        ExecutionWorkspaceID string     `gorm:"not null;type:char(36);index" json:"executionWorkspaceId"`
        HeartbeatRunID       *string    `gorm:"type:char(36);index" json:"heartbeatRunId,omitempty"`
        Kind                 string     `gorm:"not null;type:varchar(64)" json:"kind"`
        Status               string     `gorm:"not null;default:'pending';type:varchar(32)" json:"status"`
        Payload              JSON       `gorm:"type:longtext" json:"payload,omitempty"`
        Result               JSON       `gorm:"type:longtext" json:"result,omitempty"`
        StartedAt            *time.Time `json:"startedAt,omitempty"`
        FinishedAt           *time.Time `json:"finishedAt,omitempty"`
        CreatedAt            time.Time  `json:"createdAt"`
        UpdatedAt            time.Time  `json:"updatedAt"`
}

// ─── Asset ────────────────────────────────────────────────────────────────────

type Asset struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID string    `gorm:"not null;type:char(36);index" json:"companyId"`
        Name      string    `gorm:"not null;type:varchar(255)" json:"name"`
        Kind      string    `gorm:"not null;default:'file';type:varchar(64)" json:"kind"`
        URL       *string   `gorm:"type:varchar(512)" json:"url,omitempty"`
        AgentID   *string   `gorm:"type:char(36);index" json:"agentId,omitempty"`
        IssueID   *string   `gorm:"type:char(36);index" json:"issueId,omitempty"`
        RunID     *string   `gorm:"type:char(36);index" json:"runId,omitempty"`
        Size      *int64    `json:"size,omitempty"`
        MimeType  *string   `gorm:"type:varchar(128)" json:"mimeType,omitempty"`
        CreatedAt time.Time `json:"createdAt"`
        UpdatedAt time.Time `json:"updatedAt"`
}

// ─── Invite / Access ──────────────────────────────────────────────────────────

type Invite struct {
        ID        string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID string     `gorm:"not null;type:char(36);index" json:"companyId"`
        Email     string     `gorm:"not null;type:varchar(255)" json:"email"`
        Role      string     `gorm:"not null;default:'member';type:varchar(32)" json:"role"`
        Token     string     `gorm:"uniqueIndex;not null;type:varchar(255)" json:"token"`
        ExpiresAt time.Time  `json:"expiresAt"`
        UsedAt    *time.Time `json:"usedAt,omitempty"`
        CreatedAt time.Time  `json:"createdAt"`
        UpdatedAt time.Time  `json:"updatedAt"`
}

// ─── InboxItem ────────────────────────────────────────────────────────────────

type InboxItem struct {
        ID        string     `gorm:"primaryKey;type:char(36)" json:"id"`
        CompanyID string     `gorm:"not null;type:char(36);index" json:"companyId"`
        Kind      string     `gorm:"not null;type:varchar(64)" json:"kind"`
        Summary   string     `gorm:"not null;type:varchar(512)" json:"summary"`
        AgentID   *string    `gorm:"type:char(36);index" json:"agentId,omitempty"`
        IssueID   *string    `gorm:"type:char(36);index" json:"issueId,omitempty"`
        RunID     *string    `gorm:"type:char(36);index" json:"runId,omitempty"`
        Payload   JSON       `gorm:"type:longtext" json:"payload,omitempty"`
        Status    string     `gorm:"not null;default:'unread';type:varchar(32);index" json:"status"`
        ReadAt    *time.Time `json:"readAt,omitempty"`
        CreatedAt time.Time  `gorm:"index" json:"createdAt"`
        UpdatedAt time.Time  `json:"updatedAt"`
}

// ─── Plugin ───────────────────────────────────────────────────────────────────

type Plugin struct {
        ID              string      `gorm:"primaryKey;type:char(36)" json:"id"`
        PackageName     string      `gorm:"type:varchar(255);uniqueIndex" json:"packageName"`
        PluginKey       string      `gorm:"type:varchar(255);uniqueIndex" json:"pluginKey"`
        Name            string      `gorm:"not null;type:varchar(255)" json:"name"`
        Version         *string     `gorm:"type:varchar(64)" json:"version,omitempty"`
        Status          string      `gorm:"type:varchar(32);default:'disabled'" json:"status"`
        LastError       *string     `gorm:"type:text" json:"lastError,omitempty"`
        Enabled         bool        `gorm:"not null;default:true" json:"enabled"`
        ManifestJSON    JSON        `gorm:"type:longtext" json:"manifestJson,omitempty"`
        Categories      JSONAny     `gorm:"type:longtext" json:"categories,omitempty"`
        Config          JSON        `gorm:"type:longtext" json:"config,omitempty"`
        UIContributions JSON        `gorm:"type:longtext" json:"uiContributions,omitempty"`
        CreatedAt       time.Time   `json:"createdAt"`
        UpdatedAt       time.Time   `json:"updatedAt"`
}

// PluginLog stores log entries emitted by a plugin service.
type PluginLog struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        PluginID  string    `gorm:"not null;type:char(36);index" json:"pluginId"`
        Level     string    `gorm:"not null;type:varchar(16);default:'info'" json:"level"`
        Message   string    `gorm:"not null;type:text" json:"message"`
        Meta      JSON      `gorm:"type:longtext" json:"meta,omitempty"`
        CreatedAt time.Time `gorm:"index" json:"createdAt"`
}

// AgentMCPServer stores an MCP (Model Context Protocol) server configuration
// for an agent. Transport is "stdio" (spawns a child process) or "http"
// (JSON-RPC over HTTP). Tools discovered from enabled servers are automatically
// injected into the agent's LLM tool list on every heartbeat run.
type AgentMCPServer struct {
        ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
        AgentID   string    `gorm:"not null;type:char(36);index" json:"agentId"`
        CompanyID string    `gorm:"not null;type:char(36);index" json:"companyId"`
        Name      string    `gorm:"not null;type:varchar(255)" json:"name"`
        Transport string    `gorm:"not null;type:varchar(32);default:'stdio'" json:"transport"` // "stdio" or "http"
        Command   *string   `gorm:"type:text" json:"command,omitempty"`     // stdio: full shell command with args
        URL       *string   `gorm:"type:varchar(512)" json:"url,omitempty"` // http: JSON-RPC endpoint URL
        Env       JSON      `gorm:"type:longtext" json:"env,omitempty"`     // {KEY: VALUE} env vars for stdio
        Enabled   bool      `gorm:"not null;default:true" json:"enabled"`
        CreatedAt time.Time `json:"createdAt"`
        UpdatedAt time.Time `json:"updatedAt"`
}

// Replaces the previous on-disk ~/.nanoclip/agents/{id}/instructions/ directory.
type AgentInstructionFile struct {
        ID          string    `gorm:"primaryKey;type:char(36)" json:"id"`
        AgentID     string    `gorm:"not null;type:char(36);uniqueIndex:idx_agent_instr_path;index" json:"agentId"`
        CompanyID   string    `gorm:"not null;type:char(36);index" json:"companyId"`
        Path        string    `gorm:"not null;type:varchar(512);uniqueIndex:idx_agent_instr_path" json:"path"`
        Content     string    `gorm:"not null;type:longtext" json:"content"`
        IsEntryFile bool      `gorm:"not null;default:false" json:"isEntryFile"`
        SortOrder   int       `gorm:"not null;default:0" json:"sortOrder"`
        CreatedAt   time.Time `json:"createdAt"`
        UpdatedAt   time.Time `json:"updatedAt"`
}
