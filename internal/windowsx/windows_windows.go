//go:build windows

package windowsx

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/example/sessionguard/internal/model"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	wtsapi32                        = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSEnumerateSessionsW       = wtsapi32.NewProc("WTSEnumerateSessionsW")
	procWTSEnumerateProcessesW      = wtsapi32.NewProc("WTSEnumerateProcessesW")
	procWTSFreeMemory               = wtsapi32.NewProc("WTSFreeMemory")
	procWTSQuerySessionInformationW = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSQueryUserToken           = wtsapi32.NewProc("WTSQueryUserToken")
	procWTSLogoffSession            = wtsapi32.NewProc("WTSLogoffSession")
	procWTSDisconnectSession        = wtsapi32.NewProc("WTSDisconnectSession")
	procWTSSendMessageW             = wtsapi32.NewProc("WTSSendMessageW")
	userenv                         = windows.NewLazySystemDLL("userenv.dll")
	procDeleteProfileW              = userenv.NewProc("DeleteProfileW")
	kernel32                        = windows.NewLazySystemDLL("kernel32.dll")
	procGetTickCount64              = kernel32.NewProc("GetTickCount64")
	procGlobalMemoryStatusEx        = kernel32.NewProc("GlobalMemoryStatusEx")
	procExpandEnvironmentStringsW   = kernel32.NewProc("ExpandEnvironmentStringsW")
	procGetSystemTimes              = kernel32.NewProc("GetSystemTimes")
	procGetDiskFreeSpaceExW         = kernel32.NewProc("GetDiskFreeSpaceExW")
	psapi                           = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo        = psapi.NewProc("GetProcessMemoryInfo")
)

type wtsSessionInfo struct {
	SessionID      uint32
	WinStationName *uint16
	State          uint32
}
type wtsProcessInfo struct {
	SessionID   uint32
	ProcessID   uint32
	ProcessName *uint16
	UserSID     *windows.SID
}
type wtsClientAddress struct {
	AddressFamily uint32
	Address       [20]byte
}

type wtsInfoExLevel1 struct {
	SessionID      uint32
	SessionState   uint32
	SessionFlags   int32
	WinStationName [33]uint16
	UserName       [21]uint16
	DomainName     [18]uint16
	LogonTime      int64
	ConnectTime    int64
	DisconnectTime int64
	LastInputTime  int64
	CurrentTime    int64
	IncomingBytes  uint32
	OutgoingBytes  uint32
	IncomingFrames uint32
	OutgoingFrames uint32
}

const (
	wtsUserName           = 5
	wtsWinStationName     = 6
	wtsDomainName         = 7
	wtsClientName         = 10
	wtsClientAddressClass = 14
	wtsSessionInfoEx      = 25
)

var stateNames = map[uint32]string{0: "Active", 1: "Connected", 2: "ConnectQuery", 3: "Shadow", 4: "Disconnected", 5: "Idle", 6: "Listen", 7: "Reset", 8: "Down", 9: "Init"}

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
		if ca, err := queryClientAddress(row.SessionID); err == nil {
			s.ClientAddress = ca
		}
		if ex, err := querySessionInfoEx(row.SessionID); err == nil {
			s.LogonAt = fileTimePtr(ex.LogonTime)
			s.ConnectAt = fileTimePtr(ex.ConnectTime)
			s.LastInputAt = fileTimePtr(ex.LastInputTime)
			if s.State == "Disconnected" {
				s.DisconnectedSince = fileTimePtr(ex.DisconnectTime)
			}
			if s.LastInputAt != nil {
				idle := time.Since(*s.LastInputAt)
				if idle > 0 {
					s.IdleSeconds = int64(idle / time.Second)
				}
			}
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
func queryClientAddress(sessionID uint32) (string, error) {
	var p uintptr
	var bytes uint32
	r, _, e := procWTSQuerySessionInformationW.Call(0, uintptr(sessionID), wtsClientAddressClass, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&bytes)))
	if r == 0 {
		return "", e
	}
	defer procWTSFreeMemory.Call(p)
	if p == 0 || bytes < uint32(unsafe.Sizeof(wtsClientAddress{})) {
		return "", nil
	}
	a := (*wtsClientAddress)(unsafe.Pointer(p))
	if a.AddressFamily == 2 {
		return fmt.Sprintf("%d.%d.%d.%d", a.Address[2], a.Address[3], a.Address[4], a.Address[5]), nil
	}
	return "", nil
}
func querySessionInfoEx(sessionID uint32) (wtsInfoExLevel1, error) {
	var out wtsInfoExLevel1
	var p uintptr
	var bytes uint32
	r, _, e := procWTSQuerySessionInformationW.Call(0, uintptr(sessionID), wtsSessionInfoEx, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&bytes)))
	if r == 0 {
		return out, e
	}
	defer procWTSFreeMemory.Call(p)
	if p == 0 || bytes < 8+uint32(unsafe.Sizeof(out)) {
		return out, fmt.Errorf("WTSSessionInfoEx buffer too small")
	}
	level := *(*uint32)(unsafe.Pointer(p))
	if level != 1 {
		return out, fmt.Errorf("unsupported WTSSessionInfoEx level %d", level)
	}
	out = *(*wtsInfoExLevel1)(unsafe.Pointer(p + 8))
	return out, nil
}
func fileTimePtr(v int64) *time.Time {
	if v <= 0 {
		return nil
	}
	const unixDelta = 116444736000000000
	ns := (v - unixDelta) * 100
	if ns <= 0 {
		return nil
	}
	t := time.Unix(0, ns).UTC()
	return &t
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
type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

var cpuMu sync.Mutex
var prevIdle, prevKernel, prevUser uint64

func ft64(f filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
func cpuPercent() float64 {
	var idle, kernel, user filetime
	r, _, _ := procGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if r == 0 {
		return 0
	}
	i, k, u := ft64(idle), ft64(kernel), ft64(user)
	cpuMu.Lock()
	defer cpuMu.Unlock()
	pi, pk, pu := prevIdle, prevKernel, prevUser
	prevIdle, prevKernel, prevUser = i, k, u
	if pk == 0 {
		return 0
	}
	total := (k - pk) + (u - pu)
	if total == 0 {
		return 0
	}
	busy := total - (i - pi)
	return float64(busy) * 100 / float64(total)
}

func Server() (model.ServerInfo, error) {
	host, _ := os.Hostname()
	m := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	r, _, e := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m)))
	if r == 0 {
		return model.ServerInfo{}, fmt.Errorf("GlobalMemoryStatusEx: %w", e)
	}
	ticks, _, _ := procGetTickCount64.Call()
	info := model.ServerInfo{Hostname: host, OS: "Windows", UptimeSeconds: uint64(ticks) / 1000, MemoryTotal: m.TotalPhys, MemoryAvailable: m.AvailPhys, CPUPercent: cpuPercent()}
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = `C:`
	}
	root, err := windows.UTF16PtrFromString(drive + `\`)
	if err == nil {
		var avail, total, free uint64
		if rr, _, _ := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&avail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free))); rr != 0 {
			info.DiskTotal = total
			info.DiskFree = free
		}
	}
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

func Processes() ([]model.ProcessInfo, error) {
	var buf uintptr
	var count uint32
	r, _, e := procWTSEnumerateProcessesW.Call(0, 0, 1, uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&count)))
	if r == 0 {
		return nil, fmt.Errorf("WTSEnumerateProcessesW: %w", e)
	}
	defer procWTSFreeMemory.Call(buf)
	rows := unsafe.Slice((*wtsProcessInfo)(unsafe.Pointer(buf)), int(count))
	out := make([]model.ProcessInfo, 0, len(rows))
	for _, row := range rows {
		p := model.ProcessInfo{PID: row.ProcessID, SessionID: row.SessionID}
		if row.ProcessName != nil {
			p.Name = windows.UTF16PtrToString(row.ProcessName)
		}
		if row.UserSID != nil {
			p.UserSID = row.UserSID.String()
		}
		p.MemoryBytes = processMemory(row.ProcessID)
		out = append(out, p)
	}
	return out, nil
}

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func processMemory(pid uint32) uint64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	m := processMemoryCounters{CB: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&m)), uintptr(m.CB))
	if r == 0 {
		return 0
	}
	return uint64(m.WorkingSetSize)
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
func LogoffSession(id uint32) error {
	return boolCallSession(procWTSLogoffSession, "WTSLogoffSession", id)
}
func DisconnectSession(id uint32) error {
	return boolCallSession(procWTSDisconnectSession, "WTSDisconnectSession", id)
}
func boolCallSession(p *windows.LazyProc, name string, id uint32) error {
	r, _, e := p.Call(0, uintptr(id), 0)
	if r == 0 {
		if e == syscall.Errno(0) {
			return fmt.Errorf("%s(%d) failed", name, id)
		}
		return fmt.Errorf("%s(%d): %w", name, id, e)
	}
	return nil
}
func SendMessage(sessionID uint32, title, message string) error {
	if strings.TrimSpace(title) == "" {
		title = "SessionGuard"
	}
	t, err := windows.UTF16FromString(title)
	if err != nil {
		return err
	}
	m, err := windows.UTF16FromString(message)
	if err != nil {
		return err
	}
	var response uint32
	r, _, e := procWTSSendMessageW.Call(0, uintptr(sessionID), uintptr(unsafe.Pointer(&t[0])), uintptr((len(t)-1)*2), uintptr(unsafe.Pointer(&m[0])), uintptr((len(m)-1)*2), 0, 60, uintptr(unsafe.Pointer(&response)), 0)
	if r == 0 {
		if e == syscall.Errno(0) {
			return fmt.Errorf("WTSSendMessageW(%d) failed", sessionID)
		}
		return fmt.Errorf("WTSSendMessageW(%d): %w", sessionID, e)
	}
	return nil
}
func TerminateProcess(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}
func RestartServer(reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "SessionGuard maintenance restart"
	}
	return exec.Command("shutdown.exe", "/r", "/t", "0", "/d", "p:4:1", "/c", reason).Run()
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
