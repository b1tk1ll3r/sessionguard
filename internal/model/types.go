package model

import "time"

const ProtocolVersion = 1

type OIDCConfig struct {
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURL  string   `json:"redirect_url"`
	AdminGroups  []string `json:"admin_groups,omitempty"`
	SecureCookie bool     `json:"secure_cookie"`
}

type CleanupPolicy struct {
	Enabled             bool     `json:"enabled"`
	GraceSeconds        int      `json:"grace_seconds"`
	PollSeconds         int      `json:"poll_seconds"`
	RetrySeconds        int      `json:"retry_seconds"`
	DryRun              bool     `json:"dry_run"`
	ExcludeUsers        []string `json:"exclude_users,omitempty"`
	ExcludeSIDs         []string `json:"exclude_sids,omitempty"`
	AllowedProfileRoots []string `json:"allowed_profile_roots,omitempty"`
}

type ShortcutSpec struct {
	Target           string `json:"target"`
	Arguments        string `json:"arguments,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	IconLocation     string `json:"icon_location,omitempty"`
	Description      string `json:"description,omitempty"`
}

type TemplateItem struct {
	ID            string        `json:"id"`
	Kind          string        `json:"kind"` // file, directory, url, shortcut
	Target        string        `json:"target"`
	Source        string        `json:"source,omitempty"`
	Content       string        `json:"content,omitempty"`
	ContentBase64 string        `json:"content_base64,omitempty"`
	URL           string        `json:"url,omitempty"`
	Shortcut      *ShortcutSpec `json:"shortcut,omitempty"`
	Overwrite     bool          `json:"overwrite"`
}

type Policy struct {
	Revision  string         `json:"revision"`
	UpdatedAt time.Time      `json:"updated_at"`
	Cleanup   CleanupPolicy  `json:"cleanup"`
	Templates []TemplateItem `json:"templates,omitempty"`
}

type Session struct {
	ID          uint32 `json:"id"`
	State       string `json:"state"`
	User        string `json:"user,omitempty"`
	Domain      string `json:"domain,omitempty"`
	SID         string `json:"sid,omitempty"`
	ClientName  string `json:"client_name,omitempty"`
	StationName string `json:"station_name,omitempty"`
}

type ServerInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Version         string `json:"version,omitempty"`
	Build           string `json:"build,omitempty"`
	UptimeSeconds   uint64 `json:"uptime_seconds"`
	MemoryTotal     uint64 `json:"memory_total"`
	MemoryAvailable uint64 `json:"memory_available"`
}

type CleanupJob struct {
	SID         string    `json:"sid"`
	User        string    `json:"user"`
	ProfilePath string    `json:"profile_path"`
	DueAt       time.Time `json:"due_at"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error,omitempty"`
}

type AgentSnapshot struct {
	ProtocolVersion int          `json:"protocol_version"`
	AgentID         string       `json:"agent_id"`
	Server          ServerInfo   `json:"server"`
	Sessions        []Session    `json:"sessions"`
	PendingCleanup  []CleanupJob `json:"pending_cleanup,omitempty"`
	PolicyRevision  string       `json:"policy_revision"`
	AgentVersion    string       `json:"agent_version"`
	Time            time.Time    `json:"time"`
}

type AgentRecord struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	MachineID     string        `json:"machine_id"`
	TokenHash     string        `json:"token_hash"`
	EnrolledAt    time.Time     `json:"enrolled_at"`
	LastSeen      time.Time     `json:"last_seen"`
	Snapshot      AgentSnapshot `json:"snapshot"`
	DesiredPolicy *Policy       `json:"desired_policy,omitempty"`
}

type EnrollRequest struct {
	EnrollmentToken string `json:"enrollment_token"`
	Name            string `json:"name"`
	MachineID       string `json:"machine_id"`
}

type EnrollResponse struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

type HeartbeatResponse struct {
	DesiredPolicy *Policy   `json:"desired_policy,omitempty"`
	ServerTime    time.Time `json:"server_time"`
}
