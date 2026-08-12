# install.ps1 - one-line installer for ccswitch.
#
#   irm https://raw.githubusercontent.com/NotAProgrammer187/claude-code-profiles/main/install.ps1 | iex
#
# Downloads the prebuilt ccswitch.exe from the latest GitHub release, drops it
# in ~\bin, and adds that to your user PATH. No Go, no git, no build step.

$ErrorActionPreference = "Stop"
$repo = "NotAProgrammer187/claude-code-profiles"
$bin  = Join-Path $env:USERPROFILE "bin"

function Info($m) { Write-Host $m -ForegroundColor Cyan }
function Ok($m)   { Write-Host $m -ForegroundColor Green }
function Warn($m) { Write-Host $m -ForegroundColor Yellow }
function Die($m)  { Write-Host $m -ForegroundColor Red; exit 1 }

Write-Host ""
Info "Installing ccswitch..."
Write-Host ""

# --- Requirement checks --------------------------------------------------

# PowerShell 5+ (needed for Invoke-RestMethod / Expand-Archive semantics).
if ($PSVersionTable.PSVersion.Major -lt 5) {
    Die "PowerShell 5 or newer is required (you have $($PSVersionTable.PSVersion))."
}

# Windows on 64-bit, matching the amd64 binary we ship.
$isWin = $env:OS -eq "Windows_NT" -or $PSVersionTable.Platform -eq "Win32NT" -or $null -eq $PSVersionTable.Platform
if (-not $isWin) {
    Die "This installer builds a Windows binary. On macOS/Linux, build from source with 'go build' instead."
}
if (-not [Environment]::Is64BitOperatingSystem) {
    Die "A 64-bit (amd64) version of Windows is required."
}
Ok "  [ok] Windows 64-bit, PowerShell $($PSVersionTable.PSVersion.Major)"

# Claude Code itself - ccswitch launches 'claude', so warn (don't block) if absent.
if (Get-Command claude -ErrorAction SilentlyContinue) {
    Ok "  [ok] Claude Code (claude) found on PATH"
} else {
    Warn "  [!]  Claude Code (the 'claude' command) was not found on PATH."
    Warn "       ccswitch launches Claude Code, so install it first:"
    Warn "       https://docs.claude.com/en/docs/claude-code/setup"
}

# --- Download latest release asset --------------------------------------

Write-Host ""
Info "Finding the latest release..."
$headers = @{ "User-Agent" = "ccswitch-installer"; "Accept" = "application/vnd.github+json" }
try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers $headers
} catch {
    Die "Could not reach GitHub releases for $repo. Check your connection, or that a release has been published."
}

$asset = $release.assets | Where-Object { $_.name -eq "ccswitch.exe" } | Select-Object -First 1
if (-not $asset) {
    Die "The latest release ($($release.tag_name)) has no ccswitch.exe asset attached."
}
$sums = $release.assets | Where-Object { $_.name -eq "SHA256SUMS" } | Select-Object -First 1

# Download to a temp file first, so a failed checksum never leaves a bad
# binary on the PATH.
New-Item -ItemType Directory -Force -Path $bin | Out-Null
$dest = Join-Path $bin "ccswitch.exe"
$tmp  = Join-Path $env:TEMP ("ccswitch-" + [IO.Path]::GetRandomFileName() + ".exe")
Info "Downloading ccswitch.exe ($($release.tag_name))..."
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tmp -Headers $headers

# --- Checksum ------------------------------------------------------------

# Releases before the manifest existed have nothing to verify against, so a
# missing SHA256SUMS is a loud warning; once one is present, any mismatch stops
# the install.
if ($sums) {
    $manifest = (Invoke-WebRequest -Uri $sums.browser_download_url -Headers $headers -UseBasicParsing).Content
    if ($manifest -is [byte[]]) { $manifest = [Text.Encoding]::UTF8.GetString($manifest) }
    $expected = $null
    foreach ($line in ($manifest -split "`n")) {
        $parts = ($line.Trim() -split "\s+")
        if ($parts.Count -eq 2 -and $parts[1].TrimStart("*") -eq "ccswitch.exe") {
            $expected = $parts[0].ToLower()
        }
    }
    if (-not $expected) {
        Remove-Item $tmp -Force
        Die "SHA256SUMS in release $($release.tag_name) does not list ccswitch.exe - refusing to install."
    }
    $actual = (Get-FileHash -Path $tmp -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $expected) {
        Remove-Item $tmp -Force
        Die "Checksum mismatch for ccswitch.exe (got $actual, want $expected) - refusing to install."
    }
    Ok "  [ok] SHA256 checksum verified"
} else {
    Warn "  [!]  Release $($release.tag_name) has no SHA256SUMS manifest - checksum not verified."
}

Move-Item -Path $tmp -Destination $dest -Force
Ok "Installed to $dest"

# --- PATH ----------------------------------------------------------------

# Read the *User* scope specifically. $env:Path is merged machine+user, so
# writing that back into User scope would duplicate every system entry.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$bin*") {
    $sep = if ($userPath -and -not $userPath.EndsWith(";")) { ";" } else { "" }
    [Environment]::SetEnvironmentVariable("Path", "$userPath$sep$bin", "User")
    Ok "Added $bin to your user PATH."
} else {
    Ok "$bin is already on your user PATH."
}

Write-Host ""
Ok "Done. Open a NEW terminal, then run:  ccswitch"
Write-Host ""
