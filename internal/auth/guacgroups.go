package auth

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/example/sessionguard/internal/model"
)

const (
	// GuacamoleGroupsHeader carries the mapped group names to Guacamole.
	GuacamoleGroupsHeader = "X-Guacamole-Groups"
	// guacamole_entity.name is varchar(128) in the Guacamole schema.
	maxGuacamoleGroupName = 128
	maxGuacamoleGroups    = 200
)

// GuacamoleGroups maps OIDC groups to Guacamole user group names according to
// cfg. The result is sorted and de-duplicated (case-insensitively); empty,
// over-long or control-character names are dropped.
func GuacamoleGroups(groups []string, cfg model.GuacamoleGroupMapping) []string {
	if !cfg.Enabled {
		return nil
	}
	mapped := make(map[string]string, len(cfg.Map))
	for k, v := range cfg.Map {
		mapped[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(k), "/"))] = strings.TrimSpace(v)
	}
	exclude := make(map[string]bool, len(cfg.Exclude))
	for _, e := range cfg.Exclude {
		exclude[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "/"))] = true
	}
	prefix := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cfg.Prefix), "/"))

	seen := map[string]bool{}
	var out []string
	for _, g := range groups {
		name := strings.TrimPrefix(strings.TrimSpace(g), "/")
		key := strings.ToLower(name)
		if name == "" || exclude[key] {
			continue
		}
		if target, ok := mapped[key]; ok {
			name = target
		} else if prefix != "" {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			if cfg.StripPrefix {
				name = name[len(prefix):]
			}
		}
		if !validGuacamoleGroup(name) || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
		if len(out) >= maxGuacamoleGroups {
			break
		}
	}
	sort.Strings(out)
	return out
}

func validGuacamoleGroup(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > maxGuacamoleGroupName || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// EncodeGuacamoleGroups renders the header value: a comma-separated list of
// percent-encoded (UTF-8) names. Encoding keeps non-ASCII names intact
// (servlet containers decode raw header bytes as ISO-8859-1) and allows commas
// inside names. Only RFC 3986 unreserved characters stay literal, so the
// value never contains "+" and decodes with java.net.URLDecoder.
func EncodeGuacamoleGroups(groups []string) string {
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		var b strings.Builder
		for i := 0; i < len(g); i++ {
			c := g[i]
			if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
				b.WriteByte(c)
				continue
			}
			b.WriteByte('%')
			b.WriteByte("0123456789ABCDEF"[c>>4])
			b.WriteByte("0123456789ABCDEF"[c&15])
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, ",")
}
