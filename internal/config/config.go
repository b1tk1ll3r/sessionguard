package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/example/sessionguard/internal/model"
)

type Master struct {
	Listen              string           `json:"listen"`
	PublicURL           string           `json:"public_url"`
	DataFile            string           `json:"data_file"`
	EnrollmentToken     string           `json:"enrollment_token"`
	OIDC                model.OIDCConfig `json:"oidc"`
	OfflineAfterSeconds int              `json:"offline_after_seconds"`
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
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DataFile == "" {
		c.DataFile = "./data/master.json"
	}
	if c.OfflineAfterSeconds <= 0 {
		c.OfflineAfterSeconds = 30
	}
	return c, validateOIDC(c.OIDC)
}

func LoadAgent(path string) (Agent, error) {
	var c Agent
	if err := read(path, &c); err != nil {
		return c, err
	}
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
	if c.OIDC.Issuer != "" {
		if err := validateOIDC(c.OIDC); err != nil {
			return c, err
		}
	}
	return c, nil
}

func read(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return err
	}
	return nil
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
