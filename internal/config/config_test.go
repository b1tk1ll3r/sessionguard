package config

import (
	"os"
	"path/filepath"
	"testing"
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
