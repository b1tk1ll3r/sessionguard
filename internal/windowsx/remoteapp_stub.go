//go:build !windows

package windowsx

import "github.com/example/sessionguard/internal/model"

func DiscoverRemoteApps() ([]model.RemoteAppStatus, error) { return nil, ErrUnsupported }
func ReconcileRemoteApps([]model.RemoteAppSpec, []string) ([]model.RemoteAppStatus, error) {
	return nil, ErrUnsupported
}
