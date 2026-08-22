package windowsx

import (
	"testing"

	"github.com/example/sessionguard/internal/model"
)

func TestRemoteAppStatusByDesiredMarksReadyManagedApp(t *testing.T) {
	got := RemoteAppStatusByDesired([]model.RemoteAppStatus{{
		Alias: "Sage", Path: `C:\Program Files\Sage\Sage.exe`, PathExists: true,
		Published: true, InSync: true, CommandLineSetting: 0,
	}}, []model.RemoteAppSpec{{
		ResourceID: "sage", Alias: "Sage", DisplayName: "Sage 100", Path: `C:\Program Files\Sage\Sage.exe`, CommandLineSetting: 0,
	}})
	if len(got) != 1 || !got[0].Managed || !got[0].InSync || got[0].ResourceID != "sage" || got[0].Error != "" {
		t.Fatalf("unexpected ready status: %#v", got)
	}
}

func TestRemoteAppStatusByDesiredFailsMissingExecutable(t *testing.T) {
	got := RemoteAppStatusByDesired([]model.RemoteAppStatus{{
		Alias: "Sage", Path: `C:\Program Files\Sage\Sage.exe`, Published: true, PathExists: false,
	}}, []model.RemoteAppSpec{{ResourceID: "sage", Alias: "Sage", Path: `C:\Program Files\Sage\Sage.exe`}})
	if len(got) != 1 || got[0].InSync || got[0].Error == "" {
		t.Fatalf("missing executable was not reported fail-closed: %#v", got)
	}
}

func TestRemoteAppStatusByDesiredPreservesUnmanagedDiscovery(t *testing.T) {
	got := RemoteAppStatusByDesired([]model.RemoteAppStatus{{Alias: "Manual", Published: true, PathExists: true}}, nil)
	if len(got) != 1 || got[0].Managed || got[0].Alias != "Manual" {
		t.Fatalf("manual RemoteApp should remain visible and unmanaged: %#v", got)
	}
}

func TestRemoteAppStatusByDesiredAcceptsMatchingVirtualPath(t *testing.T) {
	got := RemoteAppStatusByDesired([]model.RemoteAppStatus{{
		Alias: "App", Path: `C:\\Program Files\\Vendor\\App.exe`, VPath: `%ProgramFiles%\\Vendor\\App.exe`,
		PathExists: true, Published: true,
	}}, []model.RemoteAppSpec{{ResourceID: "app", Alias: "App", Path: `%ProgramFiles%\\Vendor\\App.exe`}})
	if len(got) != 1 || !got[0].InSync || got[0].Error != "" {
		t.Fatalf("matching virtual path should be in sync: %#v", got)
	}
}
