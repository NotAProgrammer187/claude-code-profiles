# Build ccswitch.exe — a single self-contained binary, no runtime needed.
#
#   PS> .\build.ps1
#   PS> .\build.ps1 -Install     # also copy to ~\bin and add it to PATH
#
# Requires Go 1.22+ (winget install GoLang.Go), nothing else.

param([switch]$Install)

$ErrorActionPreference = "Stop"

Write-Host "Fetching dependencies..." -ForegroundColor Cyan
go mod tidy

Write-Host "Building ccswitch.exe..." -ForegroundColor Cyan
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -ldflags "-s -w" -o ccswitch.exe .\cmd\ccswitch

$size = [math]::Round((Get-Item ccswitch.exe).Length / 1MB, 1)
Write-Host "Built ccswitch.exe ($size MB)" -ForegroundColor Green

if (-not $Install) {
    Write-Host ""
    Write-Host "Run .\build.ps1 -Install to put it on your PATH." -ForegroundColor Yellow
    exit 0
}

$bin = Join-Path $env:USERPROFILE "bin"
New-Item -ItemType Directory -Force -Path $bin | Out-Null
Copy-Item ccswitch.exe $bin -Force

# Read the *User* scope specifically. $env:Path is the merged machine+user
# value, so writing that back into User scope duplicates every system entry.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$bin*") {
    $sep = if ($userPath -and -not $userPath.EndsWith(";")) { ";" } else { "" }
    [Environment]::SetEnvironmentVariable("Path", "$userPath$sep$bin", "User")
    Write-Host "Added $bin to your user PATH." -ForegroundColor Green
} else {
    Write-Host "$bin is already on your user PATH." -ForegroundColor Green
}

Write-Host ""
Write-Host "Open a new terminal, then run: ccswitch"
