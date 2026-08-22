$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force -Path .\bin | Out-Null
$env:CGO_ENABLED='0'
$env:GOOS='windows'
$env:GOARCH='amd64'
go build -trimpath -o .\bin\sessionguard-agent.exe .\cmd\agent
$env:GOOS='linux'
$env:GOARCH='amd64'
go build -trimpath -o .\bin\sessionguard-master-linux-amd64 .\cmd\master
Write-Host 'Builds written to .\bin'
