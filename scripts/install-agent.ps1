param(
  [string]$Binary = ".\bin\sessionguard-agent.exe",
  [string]$Config = ".\configs\agent.json",
  [string]$GmsaAccount = ""
)
$ErrorActionPreference = 'Stop'
$service = 'SessionGuardAgent'
$dest = 'C:\Program Files\SessionGuard'
$data = 'C:\ProgramData\SessionGuard'
New-Item -ItemType Directory -Force -Path $dest,$data | Out-Null

$existing = Get-Service -Name $service -ErrorAction SilentlyContinue
if ($existing -and $existing.Status -ne 'Stopped') {
  Stop-Service -Name $service -Force
  $existing.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(20))
}

Copy-Item $Binary "$dest\sessionguard-agent.exe" -Force
if (Test-Path $Config) {
  Copy-Item $Config "$data\agent.json" -Force
} elseif (-not (Test-Path "$data\agent.json")) {
  throw "Config '$Config' does not exist and no existing $data\agent.json was found."
}

# Protect credentials, enrollment material and the persisted per-agent token/state.
icacls $data /inheritance:r /grant:r 'SYSTEM:(OI)(CI)(F)' 'Administrators:(OI)(CI)(F)' | Out-Null
if ($GmsaAccount) {
  icacls $data /grant "$GmsaAccount`:(OI)(CI)(F)" | Out-Null
}

if (-not $existing) {
  & "$dest\sessionguard-agent.exe" -config "$data\agent.json" -service install
}

# Enterprise service defaults: delayed start and automatic recovery after crashes.
& sc.exe config $service start= delayed-auto | Out-Null
& sc.exe failure $service reset= 86400 actions= restart/5000/restart/15000/restart/60000 | Out-Null
& sc.exe failureflag $service 1 | Out-Null

if ($GmsaAccount) {
  # gMSA accounts are specified as DOMAIN\name$ and use an empty service password.
  & sc.exe config $service obj= $GmsaAccount password= "" | Out-Null
}

Start-Service -Name $service
Write-Host "SessionGuard Agent installed/updated and started."
if ($GmsaAccount) { Write-Host "Service identity: $GmsaAccount" } else { Write-Host "Service identity: LocalSystem" }
