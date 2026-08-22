//go:build windows

package windowsx

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"github.com/example/sessionguard/internal/model"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	wtsapi32                        = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSEnumerateSessionsW       = wtsapi32.NewProc("WTSEnumerateSessionsW")
	procWTSFreeMemory               = wtsapi32.NewProc("WTSFreeMemory")
	procWTSQuerySessionInformationW = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSQueryUserToken           = wtsapi32.NewProc("WTSQueryUserToken")
	userenv                         = windows.NewLazySystemDLL("userenv.dll")
	procDeleteProfileW              = userenv.NewProc("DeleteProfileW")
	kernel32                        = windows.NewLazySystemDLL("kernel32.dll")
	procGetTickCount64              = kernel32.NewProc("GetTickCount64")
	procGlobalMemoryStatusEx        = kernel32.NewProc("GlobalMemoryStatusEx")
	procExpandEnvironmentStringsW   = kernel32.NewProc("ExpandEnvironmentStringsW")
)

type wtsSessionInfo struct {
	SessionID      uint32
	WinStationName *uint16
	State          uint32
}

const (
	wtsUserName       = 5
	wtsWinStationName = 6
	wtsDomainName     = 7
	wtsClientName     = 10
)

var stateNames = map[uint32]string{
	0: "Active", 1: "Connected", 2: "ConnectQuery", 3: "Shadow", 4: "Disconnected",
	5: "Idle", 6: "Listen", 7: "Reset", 8: "Down", 9: "Init",
}

func Sessions() ([]model.Session, error) {
	var buf uintptr
	var count uint32
	r1, _, e := procWTSEnumerateSessionsW.Call(0, 0, 1, uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&count)))
	if r1 == 0 {
		return nil, fmt.Errorf("WTSEnumerateSessionsW: %w", e)
	}
	defer procWTSFreeMemory.Call(buf)
	rows := unsafe.Slice((*wtsSessionInfo)(unsafe.Pointer(buf)), int(count))
	out := make([]model.Session, 0, len(rows))
	for _, row := range rows {
		s := model.Session{ID: row.SessionID, State: stateNames[row.State]}
		if s.State == "" {
			s.State = fmt.Sprintf("State%d", row.State)
		}
		if row.WinStationName != nil {
			s.StationName = windows.UTF16PtrToString(row.WinStationName)
		}
		s.User, _ = queryString(row.SessionID, wtsUserName)
		s.Domain, _ = queryString(row.SessionID, wtsDomainName)
		s.ClientName, _ = queryString(row.SessionID, wtsClientName)
		if s.StationName == "" {
			s.StationName, _ = queryString(row.SessionID, wtsWinStationName)
		}
		if s.User != "" {
			var token windows.Token
			r, _, _ := procWTSQueryUserToken.Call(uintptr(row.SessionID), uintptr(unsafe.Pointer(&token)))
			if r != 0 {
				if tu, err := token.GetTokenUser(); err == nil && tu.User.Sid != nil {
					s.SID = tu.User.Sid.String()
				}
				_ = token.Close()
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func queryString(sessionID uint32, class uintptr) (string, error) {
	var p uintptr
	var bytes uint32
	r1, _, e := procWTSQuerySessionInformationW.Call(0, uintptr(sessionID), class, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&bytes)))
	if r1 == 0 {
		return "", e
	}
	defer procWTSFreeMemory.Call(p)
	if p == 0 || bytes < 2 {
		return "", nil
	}
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(p))), nil
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func Server() (model.ServerInfo, error) {
	host, _ := os.Hostname()
	m := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return model.ServerInfo{}, fmt.Errorf("GlobalMemoryStatusEx: %w", e)
	}
	ticks, _, _ := procGetTickCount64.Call()
	info := model.ServerInfo{Hostname: host, OS: "Windows", UptimeSeconds: uint64(ticks) / 1000, MemoryTotal: m.TotalPhys, MemoryAvailable: m.AvailPhys}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		defer k.Close()
		if v, _, err := k.GetStringValue("ProductName"); err == nil {
			info.OS = v
		}
		if v, _, err := k.GetStringValue("DisplayVersion"); err == nil {
			info.Version = v
		}
		if v, _, err := k.GetStringValue("CurrentBuildNumber"); err == nil {
			info.Build = v
		}
	}
	return info, nil
}

func ProfilePath(sid string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+sid, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	p, _, err := k.GetStringValue("ProfileImagePath")
	if err != nil {
		return "", err
	}
	return expandEnv(p), nil
}

func expandEnv(s string) string {
	in, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return s
	}
	n, _, _ := procExpandEnvironmentStringsW.Call(uintptr(unsafe.Pointer(in)), 0, 0)
	if n == 0 {
		return s
	}
	buf := make([]uint16, n)
	n2, _, _ := procExpandEnvironmentStringsW.Call(uintptr(unsafe.Pointer(in)), uintptr(unsafe.Pointer(&buf[0])), uintptr(n))
	if n2 == 0 || n2 > n {
		return s
	}
	return windows.UTF16ToString(buf)
}

func DeleteProfile(sid string) error {
	p, err := windows.UTF16PtrFromString(sid)
	if err != nil {
		return err
	}
	r, _, e := procDeleteProfileW.Call(uintptr(unsafe.Pointer(p)), 0, 0)
	if r == 0 {
		if e == syscall.Errno(0) {
			return fmt.Errorf("DeleteProfileW failed")
		}
		return fmt.Errorf("DeleteProfileW(%s): %w", sid, e)
	}
	return nil
}

func MachineID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	return strings.TrimSpace(v), err
}
