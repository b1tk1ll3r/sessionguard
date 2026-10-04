package model

import "time"

const ProtocolVersion = 4

type OIDCConfig struct {
	Issuer            string   `json:"issuer"`
	ClientID          string   `json:"client_id"`
	ClientSecret      string   `json:"client_secret"`
	RedirectURL       string   `json:"redirect_url"`
	LogoutRedirectURL string   `json:"logout_redirect_url,omitempty"`
	AdminGroups       []string `json:"admin_groups,omitempty"`
	// AllowAllAuthenticatedUsers explicitly permits every IdP user when
	// admin_groups is empty. Without it an empty list denies all logins.
	AllowAllAuthenticatedUsers bool `json:"allow_all_authenticated_users,omitempty"`
	// SessionHours is the absolute admin session lifetime (default 8);
	// IdleTimeoutMinutes ends sessions without requests (default 60).
	SessionHours       int  `json:"session_hours,omitempty"`
	IdleTimeoutMinutes int  `json:"idle_timeout_minutes,omitempty"`
	SecureCookie       bool `json:"secure_cookie"`
	// Scopes requested at the IdP (default: openid profile email groups).
	// GroupsClaims lists claim names or dot paths holding groups/roles
	// (default: groups), e.g. ["groups", "realm_access.roles"] for Keycloak.
	Scopes       []string `json:"scopes,omitempty"`
	GroupsClaims []string `json:"groups_claims,omitempty"`
}

// AccessAuthConfig configures the SessionGuard Master as a Traefik ForwardAuth
// endpoint for browser-facing services such as Apache Guacamole. It deliberately
// has its own redirect/cookie scope because the SessionGuard admin UI and the
// protected application may live on different DNS domains. Blank issuer/client
// fields inherit their values from the primary OIDC configuration.
type AccessAuthConfig struct {
	Enabled           bool   `json:"enabled"`
	Issuer            string `json:"issuer,omitempty"`
	ClientID          string `json:"client_id,omitempty"`
	ClientSecret      string `json:"client_secret,omitempty"`
	RedirectURL       string `json:"redirect_url"`
	LogoutRedirectURL string `json:"logout_redirect_url,omitempty"`
	CookieName        string `json:"cookie_name,omitempty"`
	CookieDomain      string `json:"cookie_domain,omitempty"`
	SecureCookie      bool   `json:"secure_cookie"`
	SessionHours      int    `json:"session_hours,omitempty"`
	UsernameClaim     string `json:"username_claim,omitempty"`
	// Scopes requested at the IdP (default: openid profile email groups).
	// GroupsClaims lists claim names or dot paths holding groups/roles
	// (default: groups), e.g. ["groups", "realm_access.roles"] for Keycloak.
	Scopes        []string `json:"scopes,omitempty"`
	GroupsClaims  []string `json:"groups_claims,omitempty"`
	AllowedGroups []string `json:"allowed_groups,omitempty"`
	AllowedHosts  []string `json:"allowed_hosts,omitempty"`
	// ReservedUsernames can never be used as Guacamole identity (default:
	// guacadmin, administrator, root). UsernamePattern restricts the
	// accepted username characters (default ASCII letters, digits, ._@-).
	ReservedUsernames []string `json:"reserved_usernames,omitempty"`
	UsernamePattern   string   `json:"username_pattern,omitempty"`
}

// IdentityBinding pins a Guacamole username to the immutable OIDC subject
// that used it first. It prevents account takeover by renaming an IdP
// account to an existing username.
type IdentityBinding struct {
	Username  string    `json:"username"`
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// AuthSession is an opaque, server-side browser session. SessionGuard stores
// only the SHA-256 hash of the random browser token as the map key.
type AuthSession struct {
	ID        string    `json:"id"`
	TokenHash string    `json:"token_hash"`
	Subject   string    `json:"subject"`
	SID       string    `json:"sid,omitempty"`
	Username  string    `json:"username"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	Groups    []string  `json:"groups,omitempty"`
	IDToken   string    `json:"id_token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type RBACConfig struct {
	DefaultRole string              `json:"default_role,omitempty"`
	Groups      map[string][]string `json:"groups,omitempty"`
}

type BrokerConfig struct {
	Enabled           bool   `json:"enabled"`
	APIKey            string `json:"api_key,omitempty"`
	LeaseSeconds      int    `json:"lease_seconds"`
	MinHealthScore    int    `json:"min_health_score"`
	ReconnectExisting bool   `json:"reconnect_existing"`
	SingleSession     bool   `json:"single_session_per_user"`
	DefaultFarmID     string `json:"default_farm_id,omitempty"`
}

type AlertConfig struct {
	Enabled                 bool   `json:"enabled"`
	WebhookURL              string `json:"webhook_url,omitempty"`
	CPUPercent              int    `json:"cpu_percent"`
	MemoryPercent           int    `json:"memory_percent"`
	DiskFreeGB              int    `json:"disk_free_gb"`
	HealthScore             int    `json:"health_score"`
	OfflineSeconds          int    `json:"offline_seconds"`
	ProfileFailures         int    `json:"profile_failures"`
	DisconnectedSessions    int    `json:"disconnected_sessions"`
	LogonDurationSeconds    int    `json:"logon_duration_seconds"`
	NotificationMinInterval int    `json:"notification_min_interval_seconds"`
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

type ProfileFolder struct {
	Path         string   `json:"path"`
	ExcludeGlobs []string `json:"exclude_globs,omitempty"`
}

type ProfilePolicy struct {
	Enabled              bool            `json:"enabled"`
	StoreRoot            string          `json:"store_root,omitempty"`
	BackupOnLogoff       bool            `json:"backup_on_logoff"`
	RestoreOnLogon       bool            `json:"restore_on_logon"`
	BackupDelaySeconds   int             `json:"backup_delay_seconds"`
	RetrySeconds         int             `json:"retry_seconds"`
	RestoreWindowSeconds int             `json:"restore_window_seconds"`
	KeepVersions         int             `json:"keep_versions"`
	ExcludeUsers         []string        `json:"exclude_users,omitempty"`
	ExcludeSIDs          []string        `json:"exclude_sids,omitempty"`
	Folders              []ProfileFolder `json:"folders,omitempty"`
}

type SessionPolicy struct {
	ControlEnabled             bool     `json:"control_enabled"`
	DisconnectedLogoffEnabled  bool     `json:"disconnected_logoff_enabled"`
	DisconnectedTimeoutSeconds int      `json:"disconnected_timeout_seconds"`
	ExcludeUsers               []string `json:"exclude_users,omitempty"`
	ExcludeSIDs                []string `json:"exclude_sids,omitempty"`
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
	Kind          string        `json:"kind"`
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
	Profiles  ProfilePolicy  `json:"profiles"`
	Sessions  SessionPolicy  `json:"sessions"`
	Templates []TemplateItem `json:"templates,omitempty"`
}

// RemoteAppSpec describes the desired RemoteApp registration on an RD Session Host.
// ResourceID binds the local Windows publication back to the SessionGuard resource.
type RemoteAppSpec struct {
	ResourceID          string `json:"resource_id"`
	Alias               string `json:"alias"`
	DisplayName         string `json:"display_name"`
	Path                string `json:"path"`
	IconPath            string `json:"icon_path,omitempty"`
	IconIndex           int32  `json:"icon_index,omitempty"`
	CommandLineSetting  uint32 `json:"command_line_setting"` // 0=deny, 1=allow, 2=require
	RequiredCommandLine string `json:"required_command_line,omitempty"`
	ShowInPortal        bool   `json:"show_in_portal,omitempty"`
}

// RemoteAppStatus is the agent-observed state of a RemoteApp publication.
type RemoteAppStatus struct {
	ResourceID          string    `json:"resource_id,omitempty"`
	Alias               string    `json:"alias"`
	DisplayName         string    `json:"display_name,omitempty"`
	Path                string    `json:"path,omitempty"`
	VPath               string    `json:"vpath,omitempty"`
	PathExists          bool      `json:"path_exists"`
	Published           bool      `json:"published"`
	Managed             bool      `json:"managed"`
	Owned               bool      `json:"owned,omitempty"` // registration was created by SessionGuard
	InSync              bool      `json:"in_sync"`
	CommandLineSetting  uint32    `json:"command_line_setting,omitempty"`
	RequiredCommandLine string    `json:"required_command_line,omitempty"`
	Error               string    `json:"error,omitempty"`
	ObservedAt          time.Time `json:"observed_at"`
}

type Session struct {
	ID                uint32     `json:"id"`
	State             string     `json:"state"`
	User              string     `json:"user,omitempty"`
	Domain            string     `json:"domain,omitempty"`
	SID               string     `json:"sid,omitempty"`
	ClientName        string     `json:"client_name,omitempty"`
	ClientAddress     string     `json:"client_address,omitempty"`
	StationName       string     `json:"station_name,omitempty"`
	LogonAt           *time.Time `json:"logon_at,omitempty"`
	ConnectAt         *time.Time `json:"connect_at,omitempty"`
	LastInputAt       *time.Time `json:"last_input_at,omitempty"`
	DisconnectedSince *time.Time `json:"disconnected_since,omitempty"`
	IdleSeconds       int64      `json:"idle_seconds,omitempty"`
}

type ProcessInfo struct {
	PID         uint32 `json:"pid"`
	SessionID   uint32 `json:"session_id"`
	Name        string `json:"name"`
	UserSID     string `json:"user_sid,omitempty"`
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
}

type ServerInfo struct {
	Hostname        string  `json:"hostname"`
	OS              string  `json:"os"`
	Version         string  `json:"version,omitempty"`
	Build           string  `json:"build,omitempty"`
	UptimeSeconds   uint64  `json:"uptime_seconds"`
	MemoryTotal     uint64  `json:"memory_total"`
	MemoryAvailable uint64  `json:"memory_available"`
	CPUPercent      float64 `json:"cpu_percent,omitempty"`
	DiskTotal       uint64  `json:"disk_total,omitempty"`
	DiskFree        uint64  `json:"disk_free,omitempty"`
}

type HealthCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type HealthStatus struct {
	Score          int           `json:"score"`
	ProfileStoreOK bool          `json:"profile_store_ok"`
	RDPListenerOK  bool          `json:"rdp_listener_ok"`
	Checks         []HealthCheck `json:"checks,omitempty"`
	CalculatedAt   time.Time     `json:"calculated_at"`
}

type SessionTelemetry struct {
	SessionID         uint32    `json:"session_id"`
	SID               string    `json:"sid,omitempty"`
	User              string    `json:"user,omitempty"`
	FirstSeenAt       time.Time `json:"first_seen_at"`
	LogonAt           time.Time `json:"logon_at,omitempty"`
	RestoreStartedAt  time.Time `json:"restore_started_at,omitempty"`
	RestoreFinishedAt time.Time `json:"restore_finished_at,omitempty"`
	ReadyAt           time.Time `json:"ready_at,omitempty"`
	RestoreDurationMS int64     `json:"restore_duration_ms,omitempty"`
	ObservedLogonMS   int64     `json:"observed_logon_ms,omitempty"`
}

type CleanupJob struct {
	SID         string    `json:"sid"`
	User        string    `json:"user"`
	ProfilePath string    `json:"profile_path"`
	DueAt       time.Time `json:"due_at"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error,omitempty"`
}

type ProfileJob struct {
	ID          string    `json:"id"`
	Operation   string    `json:"operation"`
	SID         string    `json:"sid"`
	User        string    `json:"user"`
	SessionID   uint32    `json:"session_id,omitempty"`
	ProfilePath string    `json:"profile_path"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	DueAt       time.Time `json:"due_at"`
	Attempts    int       `json:"attempts"`
	LastError   string    `json:"last_error,omitempty"`
}

type ProfileStatus struct {
	SID              string    `json:"sid"`
	User             string    `json:"user,omitempty"`
	LastBackupAt     time.Time `json:"last_backup_at,omitempty"`
	LastRestoreAt    time.Time `json:"last_restore_at,omitempty"`
	LastBackupError  string    `json:"last_backup_error,omitempty"`
	LastRestoreError string    `json:"last_restore_error,omitempty"`
}

type AgentEvent struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Type    string    `json:"type"`
	User    string    `json:"user,omitempty"`
	Message string    `json:"message"`
}

type SessionCommand struct {
	ID          string    `json:"id"`
	Action      string    `json:"action"`
	SessionID   uint32    `json:"session_id,omitempty"`
	PID         uint32    `json:"pid,omitempty"`
	Title       string    `json:"title,omitempty"`
	Message     string    `json:"message,omitempty"`
	RequestedBy string    `json:"requested_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type CommandResult struct {
	ID          string    `json:"id"`
	Action      string    `json:"action"`
	SessionID   uint32    `json:"session_id,omitempty"`
	PID         uint32    `json:"pid,omitempty"`
	CompletedAt time.Time `json:"completed_at"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
}

type SessionActionRequest struct {
	Action  string `json:"action"`
	PID     uint32 `json:"pid,omitempty"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message,omitempty"`
}

type AuditEntry struct {
	Time    time.Time `json:"time"`
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Target  string    `json:"target,omitempty"`
	Result  string    `json:"result"`
	Details string    `json:"details,omitempty"`
}

type AgentSnapshot struct {
	ProtocolVersion int                         `json:"protocol_version"`
	AgentID         string                      `json:"agent_id"`
	Server          ServerInfo                  `json:"server"`
	Health          HealthStatus                `json:"health"`
	Sessions        []Session                   `json:"sessions"`
	Processes       []ProcessInfo               `json:"processes,omitempty"`
	Telemetry       map[uint32]SessionTelemetry `json:"telemetry,omitempty"`
	PendingCleanup  []CleanupJob                `json:"pending_cleanup,omitempty"`
	ProfileJobs     []ProfileJob                `json:"profile_jobs,omitempty"`
	ProfileStatus   map[string]ProfileStatus    `json:"profile_status,omitempty"`
	Events          []AgentEvent                `json:"events,omitempty"`
	CommandResults  []CommandResult             `json:"command_results,omitempty"`
	RemoteApps      []RemoteAppStatus           `json:"remote_apps,omitempty"`
	Policy          Policy                      `json:"policy"`
	PolicyRevision  string                      `json:"policy_revision"`
	AgentVersion    string                      `json:"agent_version"`
	Time            time.Time                   `json:"time"`
}

type AgentRecord struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	MachineID          string            `json:"machine_id"`
	TokenHash          string            `json:"token_hash"`
	EnrolledAt         time.Time         `json:"enrolled_at"`
	LastSeen           time.Time         `json:"last_seen"`
	Snapshot           AgentSnapshot     `json:"snapshot"`
	DesiredPolicy      *Policy           `json:"desired_policy,omitempty"`
	PendingCommands    []SessionCommand  `json:"pending_commands,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
	FarmIDs            []string          `json:"farm_ids,omitempty"`
	MaintenanceMode    string            `json:"maintenance_mode,omitempty"` // online, drain, maintenance
	RestartWhenDrained bool              `json:"restart_when_drained,omitempty"`
}

type Farm struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	AgentIDs     []string          `json:"agent_ids,omitempty"`
	RequiredTags map[string]string `json:"required_tags,omitempty"`
	Policy       *Policy           `json:"policy,omitempty"`
	Enabled      bool              `json:"enabled"`
}

type Resource struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Kind                    string `json:"kind"` // desktop, remoteapp
	FarmID                  string `json:"farm_id"`
	GuacamoleConnectionID   string `json:"guacamole_connection_id,omitempty"`
	GuacamoleConnectionName string `json:"guacamole_connection_name,omitempty"`
	RemoteApp               string `json:"remote_app,omitempty"`
	RemoteAppDir            string `json:"remote_app_dir,omitempty"`
	RemoteAppArgs           string `json:"remote_app_args,omitempty"`
	ManageRemoteApp         bool   `json:"manage_remote_app,omitempty"`
	RemoteAppPath           string `json:"remote_app_path,omitempty"`
	RemoteAppIconPath       string `json:"remote_app_icon_path,omitempty"`
	RemoteAppIconIndex      int32  `json:"remote_app_icon_index,omitempty"`
	RemoteAppCommandLine    uint32 `json:"remote_app_command_line_setting,omitempty"` // 0=deny, 1=allow, 2=require
	RemoteAppRequiredArgs   string `json:"remote_app_required_command_line,omitempty"`
	RemoteAppShowInPortal   bool   `json:"remote_app_show_in_portal,omitempty"`
	// Multi-monitor "span" mode: the Guacamole browser helper may stretch one
	// window across several local screens; Guacamole's display-update channel
	// then resizes the RDP desktop accordingly. This is a UX policy and is
	// additionally bounded by guacd (8192 px) and the RDS host GPOs.
	MultiMonitor       bool     `json:"multi_monitor,omitempty"`
	MaxMonitors        int      `json:"max_monitors,omitempty"`         // 0 = default (2)
	MultiMonitorGroups []string `json:"multi_monitor_groups,omitempty"` // empty = all users
	Enabled            bool     `json:"enabled"`
}

// DisplayPolicy is returned to the Guacamole browser helper for the current
// access session and connection.
type DisplayPolicy struct {
	MultiMonitor bool   `json:"multi_monitor"`
	MaxMonitors  int    `json:"max_monitors"`
	MaxWidth     int    `json:"max_width"`
	MaxHeight    int    `json:"max_height"`
	ResourceID   string `json:"resource_id,omitempty"`
}

type UserLease struct {
	UserKey    string    `json:"user_key"`
	AgentID    string    `json:"agent_id"`
	FarmID     string    `json:"farm_id,omitempty"`
	ResourceID string    `json:"resource_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Reason     string    `json:"reason,omitempty"`
}

type BrokerRequest struct {
	Username       string `json:"username"`
	ConnectionID   string `json:"connection_id,omitempty"`
	ConnectionName string `json:"connection_name,omitempty"`
	ResourceID     string `json:"resource_id,omitempty"`
	FarmID         string `json:"farm_id,omitempty"`
}

type BrokerResponse struct {
	AgentID      string            `json:"agent_id"`
	Hostname     string            `json:"hostname"`
	FarmID       string            `json:"farm_id,omitempty"`
	ResourceID   string            `json:"resource_id,omitempty"`
	Reconnect    bool              `json:"reconnect"`
	Reason       string            `json:"reason"`
	HealthScore  int               `json:"health_score"`
	Tokens       map[string]string `json:"tokens"`
	LeaseExpires time.Time         `json:"lease_expires"`
}

type SessionHistoryEvent struct {
	Time       time.Time `json:"time"`
	AgentID    string    `json:"agent_id"`
	Hostname   string    `json:"hostname"`
	SessionID  uint32    `json:"session_id"`
	User       string    `json:"user,omitempty"`
	SID        string    `json:"sid,omitempty"`
	Event      string    `json:"event"`
	State      string    `json:"state,omitempty"`
	ClientName string    `json:"client_name,omitempty"`
	Details    string    `json:"details,omitempty"`
}

type PolicyVersion struct {
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	Revision  string    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	Actor     string    `json:"actor"`
	Policy    Policy    `json:"policy"`
}

type Alert struct {
	ID             string    `json:"id"`
	Key            string    `json:"key"`
	Severity       string    `json:"severity"`
	AgentID        string    `json:"agent_id,omitempty"`
	Hostname       string    `json:"hostname,omitempty"`
	Type           string    `json:"type"`
	Message        string    `json:"message"`
	Active         bool      `json:"active"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	ResolvedAt     time.Time `json:"resolved_at,omitempty"`
	LastNotifiedAt time.Time `json:"last_notified_at,omitempty"`
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
	DesiredPolicy     *Policy          `json:"desired_policy,omitempty"`
	DesiredRemoteApps []RemoteAppSpec  `json:"desired_remote_apps"`
	Commands          []SessionCommand `json:"commands,omitempty"`
	ServerTime        time.Time        `json:"server_time"`
}
