package store

import (
	"net/url"
	"strings"
	"time"
)

const DemoSourceMessage = "这是演示上游，未配置真实 Key，不能执行实际检查。请添加真实上游后再使用“立刻同步并查看”。"

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	NotifyEmail  string    `json:"notify_email"`
	EmailEnabled bool      `json:"email_enabled"`
	NotifyKinds  []string  `json:"notify_kinds"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	Onboarded    bool      `json:"onboarded"`
	CreatedAt    time.Time `json:"created_at"`
}
type Site struct {
	ID             int64      `json:"id"`
	UserID         int64      `json:"-"`
	Name           string     `json:"name"`
	BaseURL        string     `json:"base_url"`
	Platform       string     `json:"platform"`
	AdminSecret    string     `json:"-"`
	AdminUserID    string     `json:"admin_user_id"`
	AdminHeader    string     `json:"admin_header"`
	Status         string     `json:"status"`
	LastError      string     `json:"last_error,omitempty"`
	LastImportedAt *time.Time `json:"last_imported_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}
type Group struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"-"`
	SiteID     int64     `json:"site_id"`
	ExternalID string    `json:"external_id"`
	Name       string    `json:"name"`
	Rate       float64   `json:"rate"`
	Status     string    `json:"status"`
	Accounts   []Account `json:"accounts,omitempty"`
}
type Account struct {
	ID             int64      `json:"id"`
	UserID         int64      `json:"-"`
	SiteID         int64      `json:"site_id"`
	ExternalID     string     `json:"external_id"`
	Name           string     `json:"name"`
	Platform       string     `json:"platform"`
	BaseURL        string     `json:"base_url"`
	Secret         string     `json:"-"`
	SecretMask     string     `json:"secret_mask"`
	MonitorState   string     `json:"monitor_state"`
	Models         []string   `json:"models"`
	Rate           *float64   `json:"rate,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	Groups         []int64    `json:"group_ids,omitempty"`
	RelationGroups []string   `json:"-"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
}
type Source struct {
	ID            int64          `json:"id"`
	UserID        int64          `json:"-"`
	Name          string         `json:"name"`
	BaseURL       string         `json:"base_url"`
	Platform      string         `json:"platform"`
	Secret        string         `json:"-"`
	Fingerprint   string         `json:"-"`
	SecretMask    string         `json:"secret_mask"`
	MonitorState  string         `json:"monitor_state"`
	ProbeModel    string         `json:"probe_model"`
	Models        []string       `json:"models"`
	LastRate      *float64       `json:"last_rate,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	LastCheckedAt *time.Time     `json:"last_checked_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	HealthHistory []SourceHealth `json:"health_history,omitempty"`
}

func IsDemoSource(source Source) bool {
	if strings.EqualFold(strings.TrimSpace(source.Secret), "demo") {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(source.BaseURL))
	if err != nil {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	return hostname == "demo.local" || strings.HasSuffix(hostname, ".demo.local")
}

type SourceHealth struct {
	ID        int64     `json:"id"`
	SourceID  int64     `json:"source_id"`
	State     string    `json:"state"`
	Message   string    `json:"message,omitempty"`
	Rate      *float64  `json:"rate,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
type Task struct {
	ID               int64      `json:"id"`
	UserID           int64      `json:"-"`
	Name             string     `json:"name"`
	SourceID         int64      `json:"source_id"`
	SourceIDs        []int64    `json:"source_ids"`
	SiteID           int64      `json:"site_id"`
	GroupID          int64      `json:"group_id"`
	Adjustment       float64    `json:"adjustment"`
	MinUpstreamRate  float64    `json:"minimum_upstream_rate"`
	Enabled          bool       `json:"enabled"`
	ShadowMode       bool       `json:"shadow_mode"`
	LargeChangePct   float64    `json:"large_change_percent"`
	LastUpstreamRate *float64   `json:"last_upstream_rate,omitempty"`
	LastTargetRate   *float64   `json:"last_target_rate,omitempty"`
	LastStatus       string     `json:"last_status"`
	LastError        string     `json:"last_error,omitempty"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	Source           *Source    `json:"source,omitempty"`
	Site             *Site      `json:"site,omitempty"`
	Group            *Group     `json:"group,omitempty"`
}
type Event struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"-"`
	TaskID     *int64    `json:"task_id,omitempty"`
	Level      string    `json:"level"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title"`
	Detail     string    `json:"detail"`
	SiteID     *int64    `json:"site_id,omitempty"`
	SourceID   *int64    `json:"source_id,omitempty"`
	GroupID    *int64    `json:"group_id,omitempty"`
	SiteName   string    `json:"site_name,omitempty"`
	SourceName string    `json:"source_name,omitempty"`
	GroupName  string    `json:"group_name,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	BeforeRate *float64  `json:"before_rate,omitempty"`
	AfterRate  *float64  `json:"after_rate,omitempty"`
	Emailed    bool      `json:"emailed"`
	CreatedAt  time.Time `json:"created_at"`
}

type SystemSettings struct {
	SiteName          string `json:"site_name"`
	AdminPath         string `json:"admin_path"`
	RegistrationOpen  bool   `json:"registration_open"`
	PublicURL         string `json:"public_url"`
	SMTPHost          string `json:"smtp_host"`
	SMTPPort          int    `json:"smtp_port"`
	SMTPUser          string `json:"smtp_user"`
	SMTPPasswordSet   bool   `json:"smtp_password_set"`
	SMTPPassword      string `json:"-"`
	SMTPFrom          string `json:"smtp_from"`
	PollSeconds       int    `json:"poll_seconds"`
	ProbeSeconds      int    `json:"probe_seconds"`
	ModelCheckMinutes int    `json:"model_check_minutes"`
	EmailMinutes      int    `json:"email_minutes"`
}

type AdminUser struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Sites     int64     `json:"sites"`
	Sources   int64     `json:"sources"`
	Tasks     int64     `json:"tasks"`
	Alerts    int64     `json:"alerts"`
}

type AdminUserPage struct {
	Items    []AdminUser `json:"items"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}
type ImageObservation struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"-"`
	SourceID   int64     `json:"source_id"`
	Model      string    `json:"model"`
	Size       string    `json:"size"`
	Quality    string    `json:"quality"`
	Count      int       `json:"count"`
	GroupRate  *float64  `json:"group_rate,omitempty"`
	UnitPrice  *float64  `json:"unit_price,omitempty"`
	ActualCost float64   `json:"actual_cost"`
	RequestID  string    `json:"request_id"`
	ObservedAt time.Time `json:"observed_at"`
}
