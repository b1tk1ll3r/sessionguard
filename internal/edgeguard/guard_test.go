package edgeguard

import (
	"encoding/json"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testConfig(t *testing.T, mutate func(*Config)) *RuntimeConfig {
	t.Helper()
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.BlacklistFile = filepath.Join(dir, "blacklist.txt")
	cfg.RateExemptFile = filepath.Join(dir, "rate-exempt.txt")
	if err := os.WriteFile(cfg.BlacklistFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.RateExemptFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.StateFile = filepath.Join(dir, "state.json")
	cfg.AllowedHosts = []string{"ts.example.test", "sessionguard.example.test"}
	if mutate != nil {
		mutate(&cfg)
	}
	b, err := jsonMarshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "edgeguard.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	rc, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func jsonMarshal(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func newTestGuard(cfg *RuntimeConfig) *Guard {
	return NewGuard(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func TestAllowsNormalRequest(t *testing.T) {
	g := newTestGuard(testConfig(t, nil))
	d := g.Check(netip.MustParseAddr("203.0.113.10"), "ts.example.test", "GET", "/", time.Unix(1000, 0))
	if !d.Allowed || d.StatusCode != 204 {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestBlacklistCIDR(t *testing.T) {
	dir := t.TempDir()
	blacklist := filepath.Join(dir, "blacklist.txt")
	if err := os.WriteFile(blacklist, []byte("203.0.113.0/24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.AllowedHosts = []string{"ts.example.test"}
	cfg.BlacklistFile = blacklist
	cfg.RateExemptFile = ""
	cfg.StateFile = filepath.Join(dir, "state.json")
	b, _ := json.Marshal(cfg)
	configPath := filepath.Join(dir, "edgeguard.json")
	_ = os.WriteFile(configPath, b, 0o600)
	rc, err := LoadRuntimeConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	g := newTestGuard(rc)
	d := g.Check(netip.MustParseAddr("203.0.113.99"), "ts.example.test", "GET", "/", time.Unix(1000, 0))
	if d.StatusCode != 403 || d.Reason != "blacklisted" {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestScannerTriggersAutoBan(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.AutoBan.Threshold = 10
		c.AutoBan.ScannerWeight = 5
	})
	g := newTestGuard(cfg)
	ip := netip.MustParseAddr("198.51.100.7")
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		d := g.Check(ip, "ts.example.test", "GET", "/.env", now.Add(time.Duration(i)*time.Second))
		if d.StatusCode != 404 {
			t.Fatalf("scanner request %d: %+v", i, d)
		}
	}
	d := g.Check(ip, "ts.example.test", "GET", "/", now.Add(3*time.Second))
	if d.StatusCode != 403 || d.Reason != "temporarily banned" {
		t.Fatalf("expected temp ban, got %+v", d)
	}
}

func TestPerIPRateLimit(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.PerIPLimit = RateLimit{RatePerSecond: 1, Burst: 2}
		c.GlobalLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.AutoBan.Enabled = false
		c.Rules = nil
	})
	g := newTestGuard(cfg)
	ip := netip.MustParseAddr("198.51.100.8")
	now := time.Unix(1000, 0)
	for i := 0; i < 2; i++ {
		if d := g.Check(ip, "ts.example.test", "GET", "/", now); !d.Allowed {
			t.Fatalf("request %d should be allowed: %+v", i, d)
		}
	}
	d := g.Check(ip, "ts.example.test", "GET", "/", now)
	if d.StatusCode != 429 {
		t.Fatalf("expected 429, got %+v", d)
	}
}

func TestRateExemptCIDR(t *testing.T) {
	dir := t.TempDir()
	exempt := filepath.Join(dir, "exempt.txt")
	if err := os.WriteFile(exempt, []byte("198.51.100.0/24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.AllowedHosts = []string{"ts.example.test"}
	cfg.BlacklistFile = ""
	cfg.RateExemptFile = exempt
	cfg.PerIPLimit = RateLimit{RatePerSecond: 1, Burst: 1}
	cfg.GlobalLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
	cfg.Rules = nil
	cfg.StateFile = filepath.Join(dir, "state.json")
	b, _ := json.Marshal(cfg)
	configPath := filepath.Join(dir, "edgeguard.json")
	_ = os.WriteFile(configPath, b, 0o600)
	rc, err := LoadRuntimeConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	g := newTestGuard(rc)
	ip := netip.MustParseAddr("198.51.100.42")
	now := time.Unix(1000, 0)
	for i := 0; i < 20; i++ {
		if d := g.Check(ip, "ts.example.test", "GET", "/", now); !d.Allowed {
			t.Fatalf("request %d unexpectedly denied: %+v", i, d)
		}
	}
}

func TestBlockedMethodAndHost(t *testing.T) {
	g := newTestGuard(testConfig(t, nil))
	ip := netip.MustParseAddr("192.0.2.10")
	now := time.Unix(1000, 0)
	if d := g.Check(ip, "unknown.example.test", "GET", "/", now); d.StatusCode != 421 {
		t.Fatalf("expected 421, got %+v", d)
	}
	if d := g.Check(ip, "ts.example.test", "TRACE", "/", now); d.StatusCode != 405 {
		t.Fatalf("expected 405, got %+v", d)
	}
}

func TestEncodedScannerPath(t *testing.T) {
	g := newTestGuard(testConfig(t, nil))
	d := g.Check(netip.MustParseAddr("192.0.2.20"), "ts.example.test", "GET", "/.%65nv", time.Unix(1000, 0))
	if d.StatusCode != 404 {
		t.Fatalf("expected encoded /.env to be denied, got %+v", d)
	}
}

func TestTemporaryBanPersistsAcrossRestart(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.AutoBan.Threshold = 5
		c.AutoBan.ScannerWeight = 5
	})
	ip := netip.MustParseAddr("203.0.113.77")
	now := time.Now()
	g1 := newTestGuard(cfg)
	if d := g1.Check(ip, "ts.example.test", "GET", "/.env", now); d.StatusCode != 404 {
		t.Fatalf("expected scanner deny, got %+v", d)
	}
	g1.FlushState()
	g2 := newTestGuard(cfg)
	d := g2.Check(ip, "ts.example.test", "GET", "/", now.Add(time.Second))
	if d.StatusCode != 403 || d.Reason != "temporarily banned" {
		t.Fatalf("expected persisted temporary ban, got %+v", d)
	}
}

func TestTrackedIPCapacityIsBounded(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.MaxTrackedIPs = 2
		c.GlobalLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.PerIPLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.Rules = nil
		c.AutoBan.Enabled = false
	})
	g := newTestGuard(cfg)
	now := time.Unix(1000, 0)
	for _, raw := range []string{"192.0.2.1", "192.0.2.2"} {
		if d := g.Check(netip.MustParseAddr(raw), "ts.example.test", "GET", "/", now); !d.Allowed {
			t.Fatalf("first two IPs should be tracked: %s %+v", raw, d)
		}
	}
	d := g.Check(netip.MustParseAddr("192.0.2.3"), "ts.example.test", "GET", "/", now)
	if d.StatusCode != 429 || d.Reason != "edge state capacity" {
		t.Fatalf("expected bounded-state 429, got %+v", d)
	}
}
