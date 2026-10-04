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

func TestTrackedIPCapacityEvictsLeastRecentlySeen(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.MaxTrackedIPs = 2
		c.GlobalLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.PerIPLimit = RateLimit{RatePerSecond: 0.001, Burst: 1}
		c.Rules = nil
		c.AutoBan.Enabled = false
	})
	g := newTestGuard(cfg)
	now := time.Unix(1000, 0)
	a := netip.MustParseAddr("192.0.2.1")
	b := netip.MustParseAddr("192.0.2.2")
	c := netip.MustParseAddr("192.0.2.3")
	for i, ip := range []netip.Addr{a, b} {
		if d := g.Check(ip, "ts.example.test", "GET", "/", now.Add(time.Duration(i)*time.Second)); !d.Allowed {
			t.Fatalf("first request from %s should be allowed: %+v", ip, d)
		}
	}
	// a is now the least recently seen entry; a new client must not be
	// rejected but must evict a.
	if d := g.Check(c, "ts.example.test", "GET", "/", now.Add(2*time.Second)); !d.Allowed {
		t.Fatalf("new client must be admitted by evicting the oldest entry: %+v", d)
	}
	if got := g.TrackedClients(); got != 2 {
		t.Fatalf("tracked clients = %d, want 2", got)
	}
	if got := g.counters.StateEvictions.Load(); got != 1 {
		t.Fatalf("evictions = %d, want 1", got)
	}
	// b was kept: its exhausted bucket still denies.
	if d := g.Check(b, "ts.example.test", "GET", "/", now.Add(3*time.Second)); d.StatusCode != 429 {
		t.Fatalf("expected b to remain tracked and rate limited, got %+v", d)
	}
	// a was evicted: it starts with a fresh bucket.
	if d := g.Check(a, "ts.example.test", "GET", "/", now.Add(4*time.Second)); !d.Allowed {
		t.Fatalf("expected evicted client a to get fresh state, got %+v", d)
	}
}

func TestEvictionKeepsActiveBans(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.MaxTrackedIPs = 2
		c.GlobalLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.PerIPLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.Rules = nil
		c.AutoBan.Threshold = 5
		c.AutoBan.ScannerWeight = 5
	})
	g := newTestGuard(cfg)
	now := time.Unix(1000, 0)
	attacker := netip.MustParseAddr("198.51.100.66")
	if d := g.Check(attacker, "ts.example.test", "GET", "/.env", now); d.StatusCode != 404 {
		t.Fatalf("expected scanner deny, got %+v", d)
	}
	// Flood the table with fresh sources so the attacker's client entry is
	// evicted.
	for i := 1; i <= 10; i++ {
		ip := netip.AddrFrom4([4]byte{192, 0, 2, byte(i)})
		if d := g.Check(ip, "ts.example.test", "GET", "/", now.Add(time.Second)); !d.Allowed {
			t.Fatalf("source %s should be admitted: %+v", ip, d)
		}
	}
	if got := g.TrackedClients(); got != 2 {
		t.Fatalf("tracked clients = %d, want 2", got)
	}
	d := g.Check(attacker, "ts.example.test", "GET", "/", now.Add(2*time.Second))
	if d.StatusCode != 403 || d.Reason != "temporarily banned" {
		t.Fatalf("ban must survive state eviction, got %+v", d)
	}
}

func TestIPv6ClientsAggregatedPer64(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.GlobalLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.PerIPLimit = RateLimit{RatePerSecond: 0.001, Burst: 2}
		c.Rules = nil
		c.AutoBan.Enabled = false
	})
	g := newTestGuard(cfg)
	now := time.Unix(1000, 0)
	for i, raw := range []string{"2001:db8:1:2::1", "2001:db8:1:2:ffff::2"} {
		if d := g.Check(netip.MustParseAddr(raw), "ts.example.test", "GET", "/", now); !d.Allowed {
			t.Fatalf("request %d should be allowed: %+v", i, d)
		}
	}
	// A third, different address in the same /64 shares the exhausted bucket.
	if d := g.Check(netip.MustParseAddr("2001:db8:1:2:abcd:ef01:2345:6789"), "ts.example.test", "GET", "/", now); d.StatusCode != 429 {
		t.Fatalf("expected /64 aggregation to rate limit rotated address, got %+v", d)
	}
	// A neighbouring /64 is a different client.
	if d := g.Check(netip.MustParseAddr("2001:db8:1:3::1"), "ts.example.test", "GET", "/", now); !d.Allowed {
		t.Fatalf("different /64 should have its own bucket: %+v", d)
	}
	if got := g.TrackedClients(); got != 2 {
		t.Fatalf("tracked clients = %d, want 2", got)
	}
}

func TestIPv6BanCoversWhole64AndPersists(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.AutoBan.Threshold = 10
		c.AutoBan.ScannerWeight = 5
	})
	now := time.Now()
	g1 := newTestGuard(cfg)
	for i, raw := range []string{"2001:db8:aa:bb::1", "2001:db8:aa:bb::2"} {
		if d := g1.Check(netip.MustParseAddr(raw), "ts.example.test", "GET", "/.env", now); d.StatusCode != 404 {
			t.Fatalf("scanner request %d: %+v", i, d)
		}
	}
	other := netip.MustParseAddr("2001:db8:aa:bb:1234::99")
	d := g1.Check(other, "ts.example.test", "GET", "/", now.Add(time.Second))
	if d.StatusCode != 403 || d.Reason != "temporarily banned" {
		t.Fatalf("expected ban on whole /64, got %+v", d)
	}
	g1.FlushState()
	g2 := newTestGuard(cfg)
	d = g2.Check(other, "ts.example.test", "GET", "/", now.Add(2*time.Second))
	if d.StatusCode != 403 || d.Reason != "temporarily banned" {
		t.Fatalf("expected persisted /64 ban, got %+v", d)
	}
	if d := g2.Check(netip.MustParseAddr("2001:db8:aa:bc::1"), "ts.example.test", "GET", "/", now.Add(2*time.Second)); !d.Allowed {
		t.Fatalf("neighbouring /64 must not be banned: %+v", d)
	}
}

func TestClientKey(t *testing.T) {
	cases := []struct {
		ip   string
		bits int
		want string
	}{
		{"203.0.113.9", 64, "203.0.113.9/32"},
		{"::ffff:203.0.113.9", 64, "203.0.113.9/32"},
		{"2001:db8:1:2:3:4:5:6", 64, "2001:db8:1:2::/64"},
		{"2001:db8:1:2:3:4:5:6", 56, "2001:db8:1::/56"},
		{"2001:db8:1:2:3:4:5:6", 128, "2001:db8:1:2:3:4:5:6/128"},
	}
	for _, tc := range cases {
		if got := clientKey(netip.MustParseAddr(tc.ip), tc.bits).String(); got != tc.want {
			t.Errorf("clientKey(%s, %d) = %s, want %s", tc.ip, tc.bits, got, tc.want)
		}
	}
}

func TestIPv6PrefixLengthConfig(t *testing.T) {
	cfg := testConfig(t, func(c *Config) { c.IPv6PrefixLength = 0 })
	if cfg.IPv6PrefixLength != 64 {
		t.Fatalf("default ipv6_prefix_length = %d, want 64", cfg.IPv6PrefixLength)
	}
	c := DefaultConfig()
	c.IPv6PrefixLength = 129
	if err := validateConfig(&c); err == nil {
		t.Fatal("expected ipv6_prefix_length=129 to be rejected")
	}
}

func TestDefaultLoginEndpointRules(t *testing.T) {
	cfg := testConfig(t, func(c *Config) {
		c.GlobalLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.PerIPLimit = RateLimit{RatePerSecond: 1000, Burst: 1000}
		c.AutoBan.Enabled = false
	})
	for _, path := range []string{"/login", "/auth/login?return=%2F", "/_sessionguard/auth/login"} {
		g := newTestGuard(cfg)
		now := time.Unix(1000, 0)
		ip := netip.MustParseAddr("192.0.2.50")
		var d Decision
		for i := 0; i < 200; i++ {
			if d = g.Check(ip, "sessionguard.example.test", "GET", path, now); !d.Allowed {
				break
			}
		}
		if d.StatusCode != 429 || d.Reason != "endpoint rate limit" {
			t.Fatalf("%s: expected endpoint rate limit, got %+v", path, d)
		}
	}
}
