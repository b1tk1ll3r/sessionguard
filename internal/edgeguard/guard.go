package edgeguard

import (
	"container/list"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

type offenseState struct {
	points int
	start  time.Time
}

type clientState struct {
	key      netip.Prefix
	bucket   bucket
	rules    map[string]*bucket
	offense  offenseState
	lastSeen time.Time
	// elem is the entry in Guard.lru (front = most recently seen).
	elem *list.Element
}

type persistedState struct {
	Bans map[string]time.Time `json:"bans"`
}

type Counters struct {
	Requests        atomic.Uint64
	Allowed         atomic.Uint64
	DeniedBlacklist atomic.Uint64
	DeniedBan       atomic.Uint64
	DeniedRate      atomic.Uint64
	DeniedGlobal    atomic.Uint64
	DeniedScanner   atomic.Uint64
	DeniedMethod    atomic.Uint64
	DeniedHost      atomic.Uint64
	DeniedInvalid   atomic.Uint64
	StateEvictions  atomic.Uint64
	BansDropped     atomic.Uint64
	AutoBans        atomic.Uint64
}

type Decision struct {
	Allowed    bool
	StatusCode int
	Reason     string
	RetryAfter int
}

type Guard struct {
	mu     sync.Mutex
	cfg    atomic.Pointer[RuntimeConfig]
	global bucket
	// clients and bans are keyed by RuntimeConfig.ClientKey: /32 for IPv4,
	// the configured prefix (default /64) for IPv6.
	clients map[netip.Prefix]*clientState
	// lru orders clients by last activity so the bounded table can evict the
	// least-recently-seen entry in O(1) instead of rejecting new clients.
	// Temporary bans live in the separate bans map and are never evicted.
	lru        *list.List
	bans       map[netip.Prefix]time.Time
	stateDirty bool
	counters   Counters
	logger     *slog.Logger
}

func NewGuard(cfg *RuntimeConfig, logger *slog.Logger) *Guard {
	if logger == nil {
		logger = slog.Default()
	}
	g := &Guard{
		clients: make(map[netip.Prefix]*clientState),
		lru:     list.New(),
		bans:    make(map[netip.Prefix]time.Time),
		logger:  logger,
	}
	g.cfg.Store(cfg)
	g.loadState(cfg.StateFile, cfg.IPv6PrefixLength)
	return g
}

func (g *Guard) Config() *RuntimeConfig { return g.cfg.Load() }

func (g *Guard) ReplaceConfig(cfg *RuntimeConfig) {
	old := g.cfg.Swap(cfg)
	if old != nil && old.IPv6PrefixLength != cfg.IPv6PrefixLength {
		g.rekey(cfg.IPv6PrefixLength)
	}
	if old == nil || old.Fingerprint() != cfg.Fingerprint() {
		g.logger.Info("edgeguard configuration loaded", "fingerprint", cfg.Fingerprint()[:12])
	}
}

// rekey adapts existing state after ipv6_prefix_length changed: bans
// are re-keyed (keeping the latest expiry) and per-client buckets are reset.
func (g *Guard) rekey(ipv6Bits int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	bans := make(map[netip.Prefix]time.Time, len(g.bans))
	for key, until := range g.bans {
		nk := clientKey(key.Addr(), ipv6Bits)
		if prev, ok := bans[nk]; !ok || until.After(prev) {
			bans[nk] = until
		}
	}
	g.bans = bans
	g.clients = make(map[netip.Prefix]*clientState)
	g.lru.Init()
	g.stateDirty = true
}

func (g *Guard) Check(ip netip.Addr, host, method, rawURI string, now time.Time) Decision {
	g.counters.Requests.Add(1)
	cfg := g.cfg.Load()
	if cfg == nil {
		return Decision{StatusCode: 503, Reason: "configuration unavailable"}
	}
	if !ip.IsValid() {
		g.counters.DeniedInvalid.Add(1)
		return Decision{StatusCode: 400, Reason: "invalid client ip"}
	}
	ip = ip.Unmap()
	// Blacklist and rate-exempt lists match the exact address; all mutable
	// per-client state (buckets, offenses, bans) uses the aggregated key.
	key := cfg.ClientKey(ip)

	if len(rawURI) == 0 || len(rawURI) > cfg.MaxURILength {
		g.counters.DeniedInvalid.Add(1)
		g.addOffense(key, cfg.AutoBan.InvalidURIWeight, now, cfg)
		return Decision{StatusCode: 414, Reason: "invalid uri"}
	}

	path, ok := normalizeRequestPath(rawURI)
	if !ok {
		g.counters.DeniedInvalid.Add(1)
		g.addOffense(key, cfg.AutoBan.InvalidURIWeight, now, cfg)
		return Decision{StatusCode: 400, Reason: "invalid uri"}
	}

	if !cfg.HostAllowed(host) {
		g.counters.DeniedHost.Add(1)
		return Decision{StatusCode: 421, Reason: "host not allowed"}
	}
	if cfg.IsBlacklisted(ip) {
		g.counters.DeniedBlacklist.Add(1)
		return Decision{StatusCode: 403, Reason: "blacklisted"}
	}

	g.mu.Lock()
	if until, ok := g.bans[key]; ok {
		if now.Before(until) {
			g.mu.Unlock()
			g.counters.DeniedBan.Add(1)
			return Decision{StatusCode: 403, Reason: "temporarily banned", RetryAfter: max(1, int(until.Sub(now).Seconds()))}
		}
		delete(g.bans, key)
		g.stateDirty = true
	}
	g.mu.Unlock()

	if cfg.MethodBlocked(method) {
		g.counters.DeniedMethod.Add(1)
		g.addOffense(key, cfg.AutoBan.MethodWeight, now, cfg)
		return Decision{StatusCode: 405, Reason: "method blocked"}
	}
	if scannerPath(path, cfg.ScannerPathPrefixes) {
		g.counters.DeniedScanner.Add(1)
		g.addOffense(key, cfg.AutoBan.ScannerWeight, now, cfg)
		return Decision{StatusCode: 404, Reason: "scanner path"}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if !take(&g.global, cfg.GlobalLimit.RatePerSecond, cfg.GlobalLimit.Burst, now) {
		g.counters.DeniedGlobal.Add(1)
		return Decision{StatusCode: 429, Reason: "global rate limit", RetryAfter: 1}
	}

	cs := g.clientLocked(key, now, cfg)
	if !cfg.IsRateExempt(ip) {
		if !take(&cs.bucket, cfg.PerIPLimit.RatePerSecond, cfg.PerIPLimit.Burst, now) {
			g.counters.DeniedRate.Add(1)
			g.addOffenseLocked(key, cs, cfg.AutoBan.RateLimitWeight, now, cfg)
			return Decision{StatusCode: 429, Reason: "per-ip rate limit", RetryAfter: 1}
		}
		if rule := cfg.MatchingRule(host, path); rule != nil {
			rb := cs.rules[rule.Name]
			if rb == nil {
				rb = &bucket{}
				cs.rules[rule.Name] = rb
			}
			if !take(rb, rule.RatePerSecond, rule.Burst, now) {
				g.counters.DeniedRate.Add(1)
				g.addOffenseLocked(key, cs, cfg.AutoBan.RateLimitWeight, now, cfg)
				return Decision{StatusCode: 429, Reason: "endpoint rate limit", RetryAfter: 1}
			}
		}
	}

	g.counters.Allowed.Add(1)
	return Decision{Allowed: true, StatusCode: 204, Reason: "allow"}
}

func take(b *bucket, rate float64, burst int, now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = float64(burst)
		b.last = now
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * rate
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func normalizeRequestPath(rawURI string) (string, bool) {
	u, err := url.ParseRequestURI(rawURI)
	if err != nil {
		return "", false
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	decoded, err := url.PathUnescape(p)
	if err != nil {
		return "", false
	}
	decoded = strings.ReplaceAll(decoded, "\\", "/")
	decoded = pathpkg.Clean(decoded)
	if !strings.HasPrefix(decoded, "/") {
		decoded = "/" + decoded
	}
	return strings.ToLower(decoded), true
}

func scannerPath(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix != "" && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// clientLocked returns the state for key, creating it if necessary. When the
// bounded table is full, the least-recently-seen entries are evicted instead
// of rejecting the new client, so an attacker rotating source prefixes cannot
// lock legitimate clients out. Evicting an entry only resets its token buckets
// and offense points; active bans are stored separately and are kept.
// g.mu must be held.
func (g *Guard) clientLocked(key netip.Prefix, now time.Time, cfg *RuntimeConfig) *clientState {
	if cs := g.clients[key]; cs != nil {
		cs.lastSeen = now
		g.lru.MoveToFront(cs.elem)
		return cs
	}
	for len(g.clients) >= cfg.MaxTrackedIPs {
		oldest := g.lru.Back()
		if oldest == nil {
			break
		}
		g.removeClientLocked(oldest.Value.(*clientState))
		g.counters.StateEvictions.Add(1)
	}
	cs := &clientState{key: key, rules: make(map[string]*bucket), lastSeen: now}
	cs.elem = g.lru.PushFront(cs)
	g.clients[key] = cs
	return cs
}

func (g *Guard) removeClientLocked(cs *clientState) {
	g.lru.Remove(cs.elem)
	delete(g.clients, cs.key)
}

func (g *Guard) addOffense(key netip.Prefix, weight int, now time.Time, cfg *RuntimeConfig) {
	if weight <= 0 || !cfg.AutoBan.Enabled {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cs := g.clientLocked(key, now, cfg)
	g.addOffenseLocked(key, cs, weight, now, cfg)
}

func (g *Guard) addOffenseLocked(key netip.Prefix, cs *clientState, weight int, now time.Time, cfg *RuntimeConfig) {
	if weight <= 0 || !cfg.AutoBan.Enabled {
		return
	}
	window := time.Duration(cfg.AutoBan.WindowSeconds) * time.Second
	if cs.offense.start.IsZero() || now.Sub(cs.offense.start) > window {
		cs.offense = offenseState{start: now}
	}
	cs.offense.points += weight
	cs.lastSeen = now
	if cs.offense.points < cfg.AutoBan.Threshold {
		return
	}
	cs.offense = offenseState{}
	if _, exists := g.bans[key]; !exists && len(g.bans) >= cfg.MaxTrackedIPs {
		// Existing bans are never evicted to make room; the request that
		// triggered this offense has already been denied anyway. Expired
		// bans are purged by Cleanup.
		g.counters.BansDropped.Add(1)
		return
	}
	until := now.Add(time.Duration(cfg.AutoBan.BanSeconds) * time.Second)
	g.bans[key] = until
	g.counters.AutoBans.Add(1)
	g.logger.Warn("temporary IP ban", "ip", keyString(key), "until", until.UTC().Format(time.RFC3339))
	g.stateDirty = true
}

// keyString renders single-address keys as a plain address (compatible with
// the previous state-file format) and aggregated IPv6 keys as CIDR.
func keyString(key netip.Prefix) string {
	if key.IsSingleIP() {
		return key.Addr().String()
	}
	return key.String()
}

// parseKey accepts a plain address or CIDR from the state file and re-keys it
// with the current IPv6 aggregation length.
func parseKey(raw string, ipv6Bits int) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(raw); err == nil {
		return clientKey(p.Addr(), ipv6Bits), true
	}
	if a, err := netip.ParseAddr(raw); err == nil {
		return clientKey(a, ipv6Bits), true
	}
	return netip.Prefix{}, false
}

func (g *Guard) Cleanup(now time.Time) {
	cfg := g.cfg.Load()
	if cfg == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for key, until := range g.bans {
		if !now.Before(until) {
			delete(g.bans, key)
			g.stateDirty = true
		}
	}
	// The LRU list is ordered by last activity, so stale entries are at the
	// back and the walk stops at the first recently seen client.
	stale := now.Add(-30 * time.Minute)
	for e := g.lru.Back(); e != nil; {
		cs := e.Value.(*clientState)
		if !cs.lastSeen.Before(stale) {
			break
		}
		prev := e.Prev()
		g.removeClientLocked(cs)
		e = prev
	}
}

// TrackedClients returns the number of entries in the bounded client table.
func (g *Guard) TrackedClients() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.clients)
}

func (g *Guard) ActiveBans(now time.Time) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, until := range g.bans {
		if now.Before(until) {
			n++
		}
	}
	return n
}

func (g *Guard) loadState(path string, ipv6Bits int) {
	if strings.TrimSpace(path) == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			g.logger.Warn("read edgeguard state", "error", err)
		}
		return
	}
	var state persistedState
	if err := json.Unmarshal(b, &state); err != nil {
		g.logger.Warn("decode edgeguard state", "error", err)
		return
	}
	now := time.Now()
	for raw, until := range state.Bans {
		key, ok := parseKey(raw, ipv6Bits)
		if ok && now.Before(until) {
			if prev, exists := g.bans[key]; !exists || until.After(prev) {
				g.bans[key] = until
			}
		}
	}
}

func (g *Guard) FlushState() {
	cfg := g.cfg.Load()
	if cfg == nil || strings.TrimSpace(cfg.StateFile) == "" {
		return
	}

	g.mu.Lock()
	if !g.stateDirty {
		g.mu.Unlock()
		return
	}
	state := persistedState{Bans: make(map[string]time.Time)}
	now := time.Now()
	for key, until := range g.bans {
		if now.Before(until) {
			state.Bans[keyString(key)] = until
		}
	}
	g.stateDirty = false
	g.mu.Unlock()

	if err := writeState(cfg.StateFile, state); err != nil {
		g.logger.Error("persist edgeguard state", "error", err)
		g.mu.Lock()
		g.stateDirty = true
		g.mu.Unlock()
	}
}

func writeState(path string, state persistedState) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func (g *Guard) Metrics() string {
	return fmt.Sprintf(`# HELP sessionguard_edgeguard_requests_total Requests evaluated by EdgeGuard.
# TYPE sessionguard_edgeguard_requests_total counter
sessionguard_edgeguard_requests_total %d
# HELP sessionguard_edgeguard_allowed_total Requests allowed by EdgeGuard.
# TYPE sessionguard_edgeguard_allowed_total counter
sessionguard_edgeguard_allowed_total %d
# HELP sessionguard_edgeguard_denied_blacklist_total Requests denied by static blacklist.
# TYPE sessionguard_edgeguard_denied_blacklist_total counter
sessionguard_edgeguard_denied_blacklist_total %d
# HELP sessionguard_edgeguard_denied_ban_total Requests denied by temporary auto-ban.
# TYPE sessionguard_edgeguard_denied_ban_total counter
sessionguard_edgeguard_denied_ban_total %d
# HELP sessionguard_edgeguard_denied_rate_total Requests denied by per-IP or endpoint rate limits.
# TYPE sessionguard_edgeguard_denied_rate_total counter
sessionguard_edgeguard_denied_rate_total %d
# HELP sessionguard_edgeguard_denied_global_total Requests denied by the global rate limit.
# TYPE sessionguard_edgeguard_denied_global_total counter
sessionguard_edgeguard_denied_global_total %d
# HELP sessionguard_edgeguard_denied_scanner_total Requests denied as scanner paths.
# TYPE sessionguard_edgeguard_denied_scanner_total counter
sessionguard_edgeguard_denied_scanner_total %d
# HELP sessionguard_edgeguard_denied_method_total Requests denied by HTTP method policy.
# TYPE sessionguard_edgeguard_denied_method_total counter
sessionguard_edgeguard_denied_method_total %d
# HELP sessionguard_edgeguard_denied_host_total Requests denied because the public host is not allowed.
# TYPE sessionguard_edgeguard_denied_host_total counter
sessionguard_edgeguard_denied_host_total %d
# HELP sessionguard_edgeguard_denied_invalid_total Requests denied because the client IP or URI was invalid.
# TYPE sessionguard_edgeguard_denied_invalid_total counter
sessionguard_edgeguard_denied_invalid_total %d
# HELP sessionguard_edgeguard_state_evictions_total Least-recently-seen client entries evicted because the bounded state table was full.
# TYPE sessionguard_edgeguard_state_evictions_total counter
sessionguard_edgeguard_state_evictions_total %d
# HELP sessionguard_edgeguard_bans_dropped_total Temporary bans not recorded because the ban table was full.
# TYPE sessionguard_edgeguard_bans_dropped_total counter
sessionguard_edgeguard_bans_dropped_total %d
# HELP sessionguard_edgeguard_autobans_total Temporary bans created.
# TYPE sessionguard_edgeguard_autobans_total counter
sessionguard_edgeguard_autobans_total %d
# HELP sessionguard_edgeguard_active_bans Current temporary bans.
# TYPE sessionguard_edgeguard_active_bans gauge
sessionguard_edgeguard_active_bans %d
# HELP sessionguard_edgeguard_tracked_clients Current entries in the bounded per-client state table.
# TYPE sessionguard_edgeguard_tracked_clients gauge
sessionguard_edgeguard_tracked_clients %d
`,
		g.counters.Requests.Load(),
		g.counters.Allowed.Load(),
		g.counters.DeniedBlacklist.Load(),
		g.counters.DeniedBan.Load(),
		g.counters.DeniedRate.Load(),
		g.counters.DeniedGlobal.Load(),
		g.counters.DeniedScanner.Load(),
		g.counters.DeniedMethod.Load(),
		g.counters.DeniedHost.Load(),
		g.counters.DeniedInvalid.Load(),
		g.counters.StateEvictions.Load(),
		g.counters.BansDropped.Load(),
		g.counters.AutoBans.Load(),
		g.ActiveBans(time.Now()),
		g.TrackedClients(),
	)
}
