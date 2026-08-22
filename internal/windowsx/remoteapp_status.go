package windowsx

import (
	"sort"
	"strings"
	"time"

	"github.com/example/sessionguard/internal/model"
)

// RemoteAppStatusByDesired overlays discovery results with SessionGuard desired
// state while preserving unrelated/manual RemoteApps for visibility.
func RemoteAppStatusByDesired(discovered []model.RemoteAppStatus, desired []model.RemoteAppSpec) []model.RemoteAppStatus {
	now := time.Now().UTC()
	byAlias := make(map[string]model.RemoteAppStatus, len(discovered))
	for _, st := range discovered {
		byAlias[strings.ToLower(st.Alias)] = st
	}
	out := make([]model.RemoteAppStatus, 0, len(desired)+len(discovered))
	seen := map[string]bool{}
	for _, want := range desired {
		key := strings.ToLower(want.Alias)
		st, ok := byAlias[key]
		if !ok {
			st = model.RemoteAppStatus{Alias: want.Alias, Path: want.Path, ObservedAt: now}
		}
		st.ResourceID = want.ResourceID
		st.DisplayName = want.DisplayName
		st.Managed = true
		pathMatches := strings.EqualFold(strings.TrimSpace(st.Path), strings.TrimSpace(want.Path)) || strings.EqualFold(strings.TrimSpace(st.VPath), strings.TrimSpace(want.Path))
		st.InSync = st.Published && st.PathExists && pathMatches && st.CommandLineSetting == want.CommandLineSetting && st.RequiredCommandLine == want.RequiredCommandLine
		if st.Error == "" {
			switch {
			case !st.Published:
				st.Error = "RemoteApp is not published"
			case !st.PathExists:
				st.Error = "RemoteApp executable is not available"
			case !st.InSync:
				st.Error = "RemoteApp registration differs from desired state"
			}
		}
		out = append(out, st)
		seen[key] = true
	}
	for _, st := range discovered {
		if !seen[strings.ToLower(st.Alias)] {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Alias) < strings.ToLower(out[j].Alias) })
	return out
}
