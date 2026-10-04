package edgeguard

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"
)

type RateLimit struct {
	RatePerSecond float64 `json:"rate_per_second"`
	Burst         int     `json:"burst"`
}

type RateRule struct {
	Name          string  `json:"name"`
	Host          string  `json:"host,omitempty"`
	PathPrefix    string  `json:"path_prefix"`
	RatePerSecond float64 `json:"rate_per_second"`
	Burst         int     `json:"burst"`
}

type AutoBanConfig struct {
	Enabled          bool `json:"enabled"`
	Threshold        int  `json:"threshold"`
	WindowSeconds    int  `json:"window_seconds"`
	BanSeconds       int  `json:"ban_seconds"`
	ScannerWeight    int  `json:"scanner_weight"`
	MethodWeight     int  `json:"method_weight"`
	RateLimitWeight  int  `json:"rate_limit_weight"`
	InvalidURIWeight int  `json:"invalid_uri_weight"`
}

type Config struct {
	Listen              string        `json:"listen"`
	AllowedHosts        []string      `json:"allowed_hosts"`
	BlacklistFile       string        `json:"blacklist_file"`
	RateExemptFile      string        `json:"rate_exempt_file"`
	StateFile           string        `json:"state_file"`
	ReloadSeconds       int           `json:"reload_seconds"`
	MaxURILength        int           `json:"max_uri_length"`
	MaxTrackedIPs       int           `json:"max_tracked_ips"`
	IPv6PrefixLength    int           `json:"ipv6_prefix_length"`
	GlobalLimit         RateLimit     `json:"global_limit"`
	PerIPLimit          RateLimit     `json:"per_ip_limit"`
	Rules               []RateRule    `json:"rules"`
	BlockedMethods      []string      `json:"blocked_methods"`
	ScannerPathPrefixes []string      `json:"scanner_path_prefixes"`
	AutoBan             AutoBanConfig `json:"auto_ban"`
}

type RuntimeConfig struct {
	Config
	allowedHosts   map[string]struct{}
	blockedMethods map[string]struct{}
	blacklist      []netip.Prefix
	rateExempt     []netip.Prefix
	fingerprint    string
}

func DefaultConfig() Config {
	return Config{
		Listen:        "127.0.0.1:9081",
		ReloadSeconds: 15,
		MaxURILength:  8192,
		MaxTrackedIPs: 100000,
		// One IPv6 end site usually receives at least a /64, so a single
		// client can rotate through 2^64 source addresses. Rate limits,
		// offense points and bans therefore apply to the whole /64.
		IPv6PrefixLength: 64,
		GlobalLimit: RateLimit{
			RatePerSecond: 2500,
			Burst:         5000,
		},
		PerIPLimit: RateLimit{
			RatePerSecond: 200,
			Burst:         500,
		},
		Rules: []RateRule{
			{Name: "access-login", PathPrefix: "/_sessionguard/auth/login", RatePerSecond: 5, Burst: 100},
			{Name: "access-callback", PathPrefix: "/_sessionguard/auth/oidc/callback", RatePerSecond: 10, Burst: 100},
			{Name: "admin-oidc", PathPrefix: "/oidc/", RatePerSecond: 5, Burst: 50},
			{Name: "admin-login", PathPrefix: "/login", RatePerSecond: 5, Burst: 50},
			{Name: "admin-access-login", PathPrefix: "/auth/login", RatePerSecond: 5, Burst: 50},
		},
		BlockedMethods: []string{"CONNECT", "TRACE", "TRACK"},
		ScannerPathPrefixes: []string{
			"/.env", "/.git", "/.svn", "/.hg", "/wp-admin", "/wp-login.php",
			"/phpmyadmin", "/pma", "/cgi-bin", "/server-status", "/actuator",
			"/vendor/phpunit", "/boaform", "/HNAP1", "/solr/", "/jenkins/",
		},
		AutoBan: AutoBanConfig{
			Enabled:          true,
			Threshold:        10,
			WindowSeconds:    120,
			BanSeconds:       900,
			ScannerWeight:    5,
			MethodWeight:     3,
			RateLimitWeight:  0,
			InvalidURIWeight: 3,
		},
	}
}

func LoadRuntimeConfig(path string) (*RuntimeConfig, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}

	rc := &RuntimeConfig{Config: cfg}
	rc.allowedHosts = make(map[string]struct{}, len(cfg.AllowedHosts))
	for _, h := range cfg.AllowedHosts {
		h = normalizeHost(h)
		if h != "" {
			rc.allowedHosts[h] = struct{}{}
		}
	}
	rc.blockedMethods = make(map[string]struct{}, len(cfg.BlockedMethods))
	for _, m := range cfg.BlockedMethods {
		m = strings.ToUpper(strings.TrimSpace(m))
		if m != "" {
			rc.blockedMethods[m] = struct{}{}
		}
	}

	rc.blacklist, err = loadPrefixFile(cfg.BlacklistFile)
	if err != nil {
		return nil, fmt.Errorf("load blacklist: %w", err)
	}
	rc.rateExempt, err = loadPrefixFile(cfg.RateExemptFile)
	if err != nil {
		return nil, fmt.Errorf("load rate exempt list: %w", err)
	}

	h := sha256.New()
	h.Write(b)
	for _, p := range rc.blacklist {
		h.Write([]byte("blacklist:" + p.String() + "\n"))
	}
	for _, p := range rc.rateExempt {
		h.Write([]byte("rate-exempt:" + p.String() + "\n"))
	}
	rc.fingerprint = hex.EncodeToString(h.Sum(nil))
	return rc, nil
}

func validateConfig(cfg *Config) error {
	if strings.TrimSpace(cfg.Listen) == "" {
		return fmt.Errorf("listen must not be empty")
	}
	if cfg.ReloadSeconds <= 0 {
		cfg.ReloadSeconds = 15
	}
	if cfg.MaxURILength <= 0 {
		cfg.MaxURILength = 8192
	}
	if cfg.MaxTrackedIPs <= 0 {
		cfg.MaxTrackedIPs = 100000
	}
	if cfg.IPv6PrefixLength == 0 {
		cfg.IPv6PrefixLength = 64
	}
	if cfg.IPv6PrefixLength < 1 || cfg.IPv6PrefixLength > 128 {
		return fmt.Errorf("ipv6_prefix_length must be between 1 and 128")
	}
	if err := validateRate("global_limit", cfg.GlobalLimit); err != nil {
		return err
	}
	if err := validateRate("per_ip_limit", cfg.PerIPLimit); err != nil {
		return err
	}
	for i := range cfg.Rules {
		r := &cfg.Rules[i]
		r.Host = normalizeHost(r.Host)
		r.PathPrefix = strings.ToLower(strings.TrimSpace(r.PathPrefix))
		if r.PathPrefix == "" {
			return fmt.Errorf("rules[%d].path_prefix must not be empty", i)
		}
		if r.RatePerSecond <= 0 || r.Burst <= 0 {
			return fmt.Errorf("rules[%d] must have positive rate_per_second and burst", i)
		}
	}
	if cfg.AutoBan.Enabled {
		if cfg.AutoBan.Threshold <= 0 || cfg.AutoBan.WindowSeconds <= 0 || cfg.AutoBan.BanSeconds <= 0 {
			return fmt.Errorf("auto_ban threshold/window_seconds/ban_seconds must be positive")
		}
	}
	return nil
}

func validateRate(name string, r RateLimit) error {
	if r.RatePerSecond <= 0 || r.Burst <= 0 {
		return fmt.Errorf("%s must have positive rate_per_second and burst", name)
	}
	return nil
}

func loadPrefixFile(path string) ([]netip.Prefix, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []netip.Prefix
	s := bufio.NewScanner(f)
	lineNo := 0
	for s.Scan() {
		lineNo++
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		p, err := parsePrefix(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		out = append(out, p)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func parsePrefix(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid IP/CIDR %q", s)
	}
	bits := 128
	if a.Is4() {
		bits = 32
	}
	return netip.PrefixFrom(a, bits), nil
}

func normalizeHost(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if i := strings.IndexByte(s, ':'); i > 0 && strings.Count(s, ":") == 1 {
		s = s[:i]
	}
	return strings.TrimSuffix(s, ".")
}

func (r *RuntimeConfig) IsBlacklisted(ip netip.Addr) bool {
	return containsPrefix(r.blacklist, ip)
}

func (r *RuntimeConfig) IsRateExempt(ip netip.Addr) bool {
	return containsPrefix(r.rateExempt, ip)
}

func containsPrefix(prefixes []netip.Prefix, ip netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientKey returns the prefix that identifies one client for rate limits,
// offense points and temporary bans. IPv4 (including IPv4-mapped IPv6)
// clients are tracked per /32; IPv6 clients are aggregated to the configured
// prefix length (default /64) so rotating addresses inside one allocation
// cannot bypass per-client limits or exhaust the state table.
func (r *RuntimeConfig) ClientKey(ip netip.Addr) netip.Prefix {
	return clientKey(ip, r.IPv6PrefixLength)
}

func clientKey(ip netip.Addr, ipv6Bits int) netip.Prefix {
	ip = ip.Unmap().WithZone("")
	if ip.Is4() {
		return netip.PrefixFrom(ip, 32)
	}
	if ipv6Bits <= 0 || ipv6Bits > 128 {
		ipv6Bits = 64
	}
	return netip.PrefixFrom(ip, ipv6Bits).Masked()
}

func (r *RuntimeConfig) HostAllowed(host string) bool {
	if len(r.allowedHosts) == 0 {
		return true
	}
	_, ok := r.allowedHosts[normalizeHost(host)]
	return ok
}

func (r *RuntimeConfig) MethodBlocked(method string) bool {
	_, ok := r.blockedMethods[strings.ToUpper(strings.TrimSpace(method))]
	return ok
}

func (r *RuntimeConfig) MatchingRule(host, path string) *RateRule {
	host = normalizeHost(host)
	for i := range r.Rules {
		rule := &r.Rules[i]
		if rule.Host != "" && rule.Host != host {
			continue
		}
		if strings.HasPrefix(path, rule.PathPrefix) {
			return rule
		}
	}
	return nil
}

func (r *RuntimeConfig) ReloadInterval() time.Duration {
	return time.Duration(r.ReloadSeconds) * time.Second
}

func (r *RuntimeConfig) Fingerprint() string { return r.fingerprint }
