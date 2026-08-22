param(
  [string]$Binary = ".\bin\sessionguard-agent.exe",
  [string]$Config = ".\configs\agent.json"
)
$ErrorActionPreference = 'Stop'
$dest = 'C:\Program Files\SessionGuard'
$data = 'C:\ProgramData\SessionGuard'
New-Item -ItemType Directory -Force -Path $dest,$data | Out-Null
Copy-Item $Binary "$dest\sessionguard-agent.exe" -Force
Copy-Item $Config "$data\agent.json" -Force
icacls "$data\agent.json" /inheritance:r /grant:r 'SYSTEM:(R)' 'Administrators:(F)' | Out-Null
& "$dest\sessionguard-agent.exe" -config "$data\agent.json" -service install
& "$dest\sessionguard-agent.exe" -service start
Write-Host 'SessionGuard Agent installed and started.'
