package master

import (
	"errors"
	"net/http"
	"strings"

	"github.com/example/sessionguard/internal/auth"
	"github.com/example/sessionguard/internal/httpx"
	"github.com/example/sessionguard/internal/model"
)

const (
	defaultMaxMonitors = 2
	maxMonitorsLimit   = 4
	// guacd clamps RDP display-update sizes to 200..8192 px per dimension
	// (GUAC_RDP_DISP_MAX_SIZE in guacamole-server 1.6).
	guacdMaxDisplaySize = 8192
)

func normalizeMultiMonitor(x *model.Resource) error {
	if !x.MultiMonitor {
		x.MaxMonitors = 0
		x.MultiMonitorGroups = nil
		return nil
	}
	if x.MaxMonitors == 0 {
		x.MaxMonitors = defaultMaxMonitors
	}
	if x.MaxMonitors < 2 || x.MaxMonitors > maxMonitorsLimit {
		return errors.New("max_monitors must be between 2 and 4")
	}
	groups := make([]string, 0, len(x.MultiMonitorGroups))
	for _, g := range x.MultiMonitorGroups {
		g = strings.TrimSpace(g)
		if g == "" || containsFold(groups, g) {
			continue
		}
		if len(g) > 256 {
			return errors.New("multi_monitor_groups entries must not exceed 256 characters")
		}
		groups = append(groups, g)
	}
	if len(groups) > 64 {
		return errors.New("at most 64 multi_monitor_groups are allowed")
	}
	x.MultiMonitorGroups = groups
	return nil
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// displayPolicy is served below the Guacamole access-auth prefix
// (/_sessionguard/auth/display-policy) and requires a valid access session.
// Unknown/unmapped connections intentionally get the same "disabled" answer
// so the endpoint does not reveal which Guacamole connections are brokered.
func (a *App) displayPolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := a.access.SessionFromRequest(r)
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "access session required")
		return
	}
	q := r.URL.Query()
	req := model.BrokerRequest{
		Username:       sess.Username,
		ConnectionID:   limitString(q.Get("connection_id"), 256),
		ConnectionName: limitString(q.Get("connection_name"), 256),
	}
	httpx.JSON(w, http.StatusOK, a.displayPolicyFor(sess, req))
}

func (a *App) displayPolicyFor(sess model.AuthSession, req model.BrokerRequest) model.DisplayPolicy {
	out := model.DisplayPolicy{MaxMonitors: 1, MaxWidth: guacdMaxDisplaySize, MaxHeight: guacdMaxDisplaySize}
	if req.ConnectionID == "" && req.ConnectionName == "" {
		return out
	}
	a.store.mu.RLock()
	res, found := a.findResourceLocked(req)
	a.store.mu.RUnlock()
	if !found || !res.MultiMonitor {
		return out
	}
	if len(res.MultiMonitorGroups) > 0 && !groupsIntersect(sess.Groups, res.MultiMonitorGroups) {
		return out
	}
	out.MultiMonitor = true
	out.MaxMonitors = res.MaxMonitors
	if out.MaxMonitors < 2 {
		out.MaxMonitors = defaultMaxMonitors
	}
	out.ResourceID = res.ID
	return out
}

func groupsIntersect(got, allowed []string) bool {
	for _, g := range got {
		for _, a := range allowed {
			if auth.GroupMatches(g, a) {
				return true
			}
		}
	}
	return false
}

func limitString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
