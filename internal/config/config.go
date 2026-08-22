package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/example/sessionguard/internal/model"
)

type Master struct {
	Listen              string             `json:"listen"`
	PublicURL           string             `json:"public_url"`
	DataFile            string             `json:"data_file,omitempty"`
	DatabaseURL         string             `json:"database_url,omitempty"`
	EnrollmentToken     string             `json:"enrollment_token"`
	OIDC                model.OIDCConfig   `json:"oidc"`
	RBAC                model.RBACConfig   `json:"rbac"`
	Broker              model.BrokerConfig `json:"broker"`
	Alerts              model.AlertConfig  `json:"alerts"`
	OfflineAfterSeconds int                `json:"offline_after_seconds"`
	HistoryLimit        int                `json:"history_limit"`
}

type Agent struct {
	Listen           string           `json:"listen"`
	PublicURL        string           `json:"public_url"`
	DataDir          string           `json:"data_dir"`
	MasterURL        string           `json:"master_url"`
	EnrollmentToken  string           `json:"enrollment_token"`
	HeartbeatSeconds int              `json:"heartbeat_seconds"`
	OIDC             model.OIDCConfig `json:"oidc"`
	Policy           model.Policy     `json:"policy"`
}

func LoadMaster(path string) (Master, error) {
	var c Master
	if err := read(path, &c); err != nil {
		return c, err
	}
	applyMasterEnv(&c)
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DataFile == "" {
		c.DataFile = "./data/master.json"
	}
	if c.OfflineAfterSeconds <= 0 {
		c.OfflineAfterSeconds = 30
	}
	if c.HistoryLimit <= 0 {
		c.HistoryLimit = 50000
	}
	if c.Broker.LeaseSeconds <= 0 {
		c.Broker.LeaseSeconds = 900
	}
	if c.Broker.MinHealthScore <= 0 {
		c.Broker.MinHealthScore = 60
	}
	if c.Alerts.CPUPercent <= 0 {
		c.Alerts.CPUPercent = 90
	}
	if c.Alerts.MemoryPercent <= 0 {
		c.Alerts.MemoryPercent = 90
	}
	if c.Alerts.DiskFreeGB <= 0 {
		c.Alerts.DiskFreeGB = 10
	}
	if c.Alerts.HealthScore <= 0 {
		c.Alerts.HealthScore = 50
	}
	if c.Alerts.OfflineSeconds <= 0 {
		c.Alerts.OfflineSeconds = 120
	}
	if c.Alerts.ProfileFailures <= 0 {
		c.Alerts.ProfileFailures = 3
	}
	if c.Alerts.DisconnectedSessions <= 0 {
		c.Alerts.DisconnectedSessions = 20
	}
	if c.Alerts.LogonDurationSeconds <= 0 {
		c.Alerts.LogonDurationSeconds = 30
	}
	if c.Alerts.NotificationMinInterval <= 0 {
		c.Alerts.NotificationMinInterval = 900
	}
	if c.RBAC.DefaultRole == "" {
		c.RBAC.DefaultRole = "viewer"
	}
	if c.RBAC.Groups == nil {
		c.RBAC.Groups = map[string][]string{}
	}
	if err := validateOIDC(c.OIDC); err != nil {
		return c, err
	}
	if c.Broker.Enabled && strings.TrimSpace(c.Broker.APIKey) == "" {
		return c, errors.New("broker.api_key is required when broker is enabled")
	}
	return c, nil
}

func LoadAgent(path string) (Agent, error) {
	var c Agent
	if err := read(path, &c); err != nil {
		return c, err
	}
	applyAgentEnv(&c)
	if c.Listen == "" {
		c.Listen = ":9091"
	}
	if c.DataDir == "" {
		c.DataDir = `C:\ProgramData\SessionGuard`
	}
	if c.HeartbeatSeconds <= 0 {
		c.HeartbeatSeconds = 10
	}
	if c.Policy.Cleanup.GraceSeconds <= 0 {
		c.Policy.Cleanup.GraceSeconds = 600
	}
	if c.Policy.Cleanup.PollSeconds <= 0 {
		c.Policy.Cleanup.PollSeconds = 10
	}
	if c.Policy.Cleanup.RetrySeconds <= 0 {
		c.Policy.Cleanup.RetrySeconds = 60
	}
	if len(c.Policy.Cleanup.AllowedProfileRoots) == 0 {
		c.Policy.Cleanup.AllowedProfileRoots = []string{`C:\Users`}
	}
	if c.Policy.Cleanup.ExcludeUsers == nil {
		c.Policy.Cleanup.ExcludeUsers = []string{"Administrator", "DefaultAccount", "WDAGUtilityAccount"}
	}
	if c.Policy.Cleanup.ExcludeSIDs == nil {
		c.Policy.Cleanup.ExcludeSIDs = []string{"S-1-5-18", "S-1-5-19", "S-1-5-20"}
	}
	NormalizePolicy(&c.Policy)
	if err := ValidatePolicy(c.Policy); err != nil {
		return c, err
	}
	if c.OIDC.Issuer != "" {
		if err := validateOIDC(c.OIDC); err != nil {
			return c, err
		}
	}
	return c, nil
}

func applyMasterEnv(c *Master) {
	set := func(name string, dst *string) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			*dst = v
		}
	}
	set("SESSIONGUARD_DATABASE_URL", &c.DatabaseURL)
	set("SESSIONGUARD_ENROLLMENT_TOKEN", &c.EnrollmentToken)
	set("SESSIONGUARD_BROKER_API_KEY", &c.Broker.APIKey)
	set("SESSIONGUARD_OIDC_CLIENT_SECRET", &c.OIDC.ClientSecret)
	set("SESSIONGUARD_ALERT_WEBHOOK_URL", &c.Alerts.WebhookURL)
}

func applyAgentEnv(c *Agent) {
	set := func(name string, dst *string) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			*dst = v
		}
	}
	set("SESSIONGUARD_MASTER_URL", &c.MasterURL)
	set("SESSIONGUARD_ENROLLMENT_TOKEN", &c.EnrollmentToken)
	set("SESSIONGUARD_OIDC_CLIENT_SECRET", &c.OIDC.ClientSecret)
}

func read(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func SaveJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func validateOIDC(c model.OIDCConfig) error {
	if c.Issuer == "" || c.ClientID == "" || c.RedirectURL == "" {
		return errors.New("oidc issuer, client_id and redirect_url are required")
	}
	return nil
}

func NormalizePolicy(p *model.Policy) {
	if p.Cleanup.GraceSeconds <= 0 {
		p.Cleanup.GraceSeconds = 600
	}
	if p.Cleanup.PollSeconds <= 0 {
		p.Cleanup.PollSeconds = 10
	}
	if p.Cleanup.RetrySeconds <= 0 {
		p.Cleanup.RetrySeconds = 60
	}
	if len(p.Cleanup.AllowedProfileRoots) == 0 {
		p.Cleanup.AllowedProfileRoots = []string{`C:\Users`}
	}
	if p.Profiles.BackupDelaySeconds < 0 {
		p.Profiles.BackupDelaySeconds = 0
	}
	if p.Profiles.BackupDelaySeconds == 0 {
		p.Profiles.BackupDelaySeconds = 5
	}
	if p.Profiles.RetrySeconds <= 0 {
		p.Profiles.RetrySeconds = 60
	}
	if p.Profiles.RestoreWindowSeconds <= 0 {
		p.Profiles.RestoreWindowSeconds = 120
	}
	if p.Profiles.KeepVersions < 0 {
		p.Profiles.KeepVersions = 0
	}
	if p.Sessions.DisconnectedTimeoutSeconds <= 0 {
		p.Sessions.DisconnectedTimeoutSeconds = 3600
	}
}

func ValidatePolicy(p model.Policy) error {
	if p.Cleanup.GraceSeconds < 1 || p.Cleanup.PollSeconds < 2 || p.Cleanup.RetrySeconds < 1 {
		return errors.New("invalid cleanup timing")
	}
	if p.Profiles.Enabled {
		if strings.TrimSpace(p.Profiles.StoreRoot) == "" {
			return errors.New("profiles.store_root is required when profile sync is enabled")
		}
		if len(p.Profiles.Folders) == 0 {
			return errors.New("at least one profiles.folders entry is required when profile sync is enabled")
		}
		if p.Profiles.BackupDelaySeconds < 0 {
			return errors.New("profiles.backup_delay_seconds must be >= 0")
		}
		if p.Profiles.RetrySeconds < 1 {
			return errors.New("profiles.retry_seconds must be >= 1")
		}
		if p.Profiles.RestoreWindowSeconds < 10 {
			return errors.New("profiles.restore_window_seconds must be >= 10")
		}
		for _, f := range p.Profiles.Folders {
			v := strings.ReplaceAll(strings.TrimSpace(f.Path), `\`, "/")
			if v == "" || strings.HasPrefix(v, "/") || strings.Contains(v, ":") || v == ".." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../") {
				return errors.New("profile folder paths must be relative and may not escape the user profile")
			}
		}
	}
	if p.Sessions.DisconnectedLogoffEnabled && p.Sessions.DisconnectedTimeoutSeconds < 60 {
		return errors.New("sessions.disconnected_timeout_seconds must be >= 60")
	}
	return nil
}
