//go:build windows

package windowsx

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/example/sessionguard/internal/model"
)

var remoteAppAliasRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

type remoteAppReconcileRequest struct {
	Desired       []model.RemoteAppSpec `json:"desired"`
	RemoveAliases []string              `json:"remove_aliases,omitempty"`
}

// DiscoverRemoteApps returns all RemoteApp registrations known by the local
// RD Session Host provider. It is read-only and does not require SessionGuard
// to own the entries.
func DiscoverRemoteApps() ([]model.RemoteAppStatus, error) {
	const script = `$ErrorActionPreference='Stop'
$items = @(Get-WmiObject -Namespace 'root\cimv2\TerminalServices' -Class Win32_TSPublishedApplication -Authentication PacketPrivacy | ForEach-Object {
  [pscustomobject]@{
    resource_id = ''
    alias = [string]$_.Alias
    display_name = [string]$_.Name
    path = [string]$_.Path
    vpath = [string]$_.VPath
    path_exists = [bool]$_.PathExists
    published = $true
    managed = $false
    in_sync = $true
    command_line_setting = [uint32]$_.CommandLineSetting
    required_command_line = [string]$_.RequiredCommandLine
    error = ''
    observed_at = [DateTime]::UtcNow.ToString('o')
  }
})
ConvertTo-Json -InputObject @($items) -Compress -Depth 4`
	var out []model.RemoteAppStatus
	if err := runPowerShellJSON(script, &out); err != nil {
		return nil, fmt.Errorf("discover RemoteApps: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Alias) < strings.ToLower(out[j].Alias) })
	return out, nil
}

// ReconcileRemoteApps applies only the explicitly desired SessionGuard-owned
// registrations and removes only aliases that the caller identifies as
// previously SessionGuard-managed. Existing unrelated RemoteApps are left
// untouched.
func ReconcileRemoteApps(desired []model.RemoteAppSpec, removeAliases []string) ([]model.RemoteAppStatus, error) {
	for _, app := range desired {
		if !remoteAppAliasRE.MatchString(app.Alias) {
			return nil, fmt.Errorf("invalid RemoteApp alias %q", app.Alias)
		}
		if strings.TrimSpace(app.Path) == "" {
			return nil, fmt.Errorf("RemoteApp %q has empty path", app.Alias)
		}
		if app.CommandLineSetting > 2 {
			return nil, fmt.Errorf("RemoteApp %q has invalid command-line setting %d", app.Alias, app.CommandLineSetting)
		}
	}
	cleanRemove := make([]string, 0, len(removeAliases))
	for _, alias := range removeAliases {
		if remoteAppAliasRE.MatchString(alias) {
			cleanRemove = append(cleanRemove, alias)
		}
	}
	req := remoteAppReconcileRequest{Desired: desired, RemoveAliases: cleanRemove}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	payload := base64.StdEncoding.EncodeToString(b)
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'
$raw=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))
$req=$raw | ConvertFrom-Json
$ns='root\cimv2\TerminalServices'

function Get-App([string]$Alias) {
  $safe=$Alias.Replace("'", "''")
  return Get-WmiObject -Namespace $ns -Class Win32_TSPublishedApplication -Authentication PacketPrivacy -Filter ("Alias='"+$safe+"'") | Select-Object -First 1
}
function New-RdpFile($a) {
  $display=[string]$a.display_name
  if ([string]::IsNullOrWhiteSpace($display)) { $display=[string]$a.alias }
  $args=''
  if ([uint32]$a.command_line_setting -eq 2) { $args=[string]$a.required_command_line }
  return (@(
    'screen mode id:i:2',
    'use multimon:i:0',
    'session bpp:i:32',
    'compression:i:1',
    'keyboardhook:i:2',
    'audiocapturemode:i:0',
    'videoplaybackmode:i:1',
    'networkautodetect:i:1',
    'bandwidthautodetect:i:1',
    'displayconnectionbar:i:1',
    'redirectprinters:i:1',
    'redirectsmartcards:i:1',
    'redirectclipboard:i:1',
    'autoreconnection enabled:i:1',
    'authentication level:i:2',
    'prompt for credentials:i:1',
    'negotiate security layer:i:1',
    'alternate shell:s:rdpinit.exe',
    'remoteapplicationmode:i:1',
    ('remoteapplicationprogram:s:||'+[string]$a.alias),
    ('remoteapplicationname:s:'+$display),
    ('remoteapplicationcmdline:s:'+$args),
    'full address:s:localhost'
  ) -join [Environment]::NewLine)
}

foreach($alias in @($req.remove_aliases)) {
  if ([string]::IsNullOrWhiteSpace([string]$alias)) { continue }
  $old=Get-App ([string]$alias)
  if ($null -ne $old) { $null=$old.Delete() }
}

$results=@()
foreach($a in @($req.desired)) {
  $alias=[string]$a.alias
  $path=[Environment]::ExpandEnvironmentVariables([string]$a.path)
  $icon=[Environment]::ExpandEnvironmentVariables([string]$a.icon_path)
  if ([string]::IsNullOrWhiteSpace($icon)) { $icon=$path }
  $status=[ordered]@{
    resource_id=[string]$a.resource_id; alias=$alias; display_name=[string]$a.display_name;
    path=$path; vpath=[string]$a.path; path_exists=$false; published=$false; managed=$true; owned=$false; in_sync=$false;
    command_line_setting=[uint32]$a.command_line_setting; required_command_line=[string]$a.required_command_line;
    error=''; observed_at=[DateTime]::UtcNow.ToString('o')
  }
  try {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Executable not found: $path" }
    $status.path_exists=$true
    $obj=Get-App $alias
    if ($null -eq $obj) {
      $class=Get-WmiObject -Namespace $ns -List -Class Win32_TSPublishedApplication -Authentication PacketPrivacy
      $obj=$class.CreateInstance()
      $obj.Alias=$alias
      $status.owned=$true
    }
    $obj.Path=$path
    $obj.VPath=[string]$a.path
    $obj.IconPath=$icon
    $obj.IconIndex=[int]$a.icon_index
    $obj.CommandLineSetting=[uint32]$a.command_line_setting
    $obj.RequiredCommandLine=[string]$a.required_command_line
    $obj.ShowInPortal=[bool]$a.show_in_portal
    $obj.RDPFileContents=New-RdpFile $a
    $null=$obj.Put()
    $check=Get-App $alias
    if ($null -eq $check) { throw 'RemoteApp provider did not return the registration after Put()' }
    $status.published=$true
    $status.path_exists=[bool]$check.PathExists
    $status.in_sync=([string]$check.Path -ieq $path) -and ([uint32]$check.CommandLineSetting -eq [uint32]$a.command_line_setting) -and ([string]$check.RequiredCommandLine -ceq [string]$a.required_command_line)
    if (-not $status.in_sync) { $status.error='RemoteApp registration differs from desired state after reconciliation' }
  } catch {
    $status.error=$_.Exception.Message
  }
  $results += [pscustomobject]$status
}
ConvertTo-Json -InputObject @($results) -Compress -Depth 5`, payload)

	var out []model.RemoteAppStatus
	if err := runPowerShellJSON(script, &out); err != nil {
		return nil, fmt.Errorf("reconcile RemoteApps: %w", err)
	}
	return out, nil
}

func runPowerShellJSON(script string, out any) error {
	encoded := encodePowerShell(script)
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	b, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("PowerShell: %s", msg)
	}
	raw := strings.TrimSpace(string(b))
	if raw == "" {
		raw = "[]"
	}
	// ConvertTo-Json emits an object instead of an array when there is exactly
	// one item on older Windows PowerShell. Accept both forms.
	if strings.HasPrefix(raw, "{") {
		raw = "[" + raw + "]"
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("decode PowerShell JSON: %w (output=%q)", err, raw)
	}
	return nil
}

func encodePowerShell(script string) string {
	u16 := utf16.Encode([]rune(script))
	b := make([]byte, len(u16)*2)
	for i, v := range u16 {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
