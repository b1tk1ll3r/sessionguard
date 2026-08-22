package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/example/sessionguard/internal/model"
)

func TestAgentDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.json")
	if err := os.WriteFile(p, []byte(`{"listen":"127.0.0.1:9091","policy":{"cleanup":{"enabled":true}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadAgent(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Policy.Cleanup.GraceSeconds != 600 {
		t.Fatalf("grace=%d", c.Policy.Cleanup.GraceSeconds)
	}
	if len(c.Policy.Cleanup.AllowedProfileRoots) == 0 {
		t.Fatal("missing allowed profile root")
	}
}

func TestValidateProfilePolicy(t *testing.T) {
	p := model.Policy{
		Cleanup:  model.CleanupPolicy{GraceSeconds: 600, PollSeconds: 10, RetrySeconds: 60, AllowedProfileRoots: []string{`C:\Users`}},
		Profiles: model.ProfilePolicy{Enabled: true, StoreRoot: `\\server\profiles`, RetrySeconds: 60, RestoreWindowSeconds: 120, Folders: []model.ProfileFolder{{Path: `AppData\Roaming\Example`}}},
		Sessions: model.SessionPolicy{DisconnectedTimeoutSeconds: 3600},
	}
	if err := ValidatePolicy(p); err != nil {
		t.Fatal(err)
	}
	p.Profiles.Folders[0].Path = `..\Windows`
	if err := ValidatePolicy(p); err == nil {
		t.Fatal("expected profile traversal validation error")
	}
}

func TestDisconnectedTimeoutMinimum(t *testing.T) {
	p := model.Policy{Cleanup: model.CleanupPolicy{GraceSeconds: 600, PollSeconds: 10, RetrySeconds: 60}, Sessions: model.SessionPolicy{DisconnectedLogoffEnabled: true, DisconnectedTimeoutSeconds: 30}}
	NormalizePolicy(&p)
	p.Sessions.DisconnectedTimeoutSeconds = 30
	if err := ValidatePolicy(p); err == nil {
		t.Fatal("expected disconnected timeout validation error")
	}
}
