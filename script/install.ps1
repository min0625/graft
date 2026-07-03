#Requires -Version 5.1
<#
.SYNOPSIS
    Installs the graft CLI for Windows.
.DESCRIPTION
    Downloads the latest (or pinned) graft binary from GitHub Releases,
    verifies the SHA-256 checksum, and installs it to GRAFT_INSTALL_DIR
    (default: $HOME\.local\bin).
.EXAMPLE
    irm https://raw.githubusercontent.com/min0625/graft/main/script/install.ps1 | iex
.EXAMPLE
    $env:GRAFT_VERSION = 'v0.0.1-beta.1'
    irm https://raw.githubusercontent.com/min0625/graft/main/script/install.ps1 | iex
#>

[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Windows PowerShell 5.1 may default to TLS 1.0, which GitHub rejects; force TLS 1.2.
[Net.ServicePointManager]::SecurityProtocol =
    [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# The progress bar throttles Invoke-WebRequest downloads 10-50x in PowerShell 5.1.
$ProgressPreference = 'SilentlyContinue'

$Repo       = 'min0625/graft'
$Binary     = 'graft'
$InstallDir = if ($env:GRAFT_INSTALL_DIR) { $env:GRAFT_INSTALL_DIR } else { Join-Path $HOME '.local\bin' }

function Write-Info    { param($Msg) Write-Host $Msg }
function Write-Success { param($Msg) Write-Host "[ok] $Msg" -ForegroundColor Green }
function Write-Warn    { param($Msg) Write-Host "[!]  $Msg" -ForegroundColor Yellow }

function Get-Arch {
    # PROCESSOR_ARCHITEW6432 is set by Windows when a 32-bit process runs on a 64-bit OS (WOW64);
    # it reflects the native OS architecture. For 64-bit processes it is empty, so fall back to
    # PROCESSOR_ARCHITECTURE which is always the process/OS architecture for 64-bit processes.
    $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($arch) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { throw "Unsupported architecture: $arch. Only x86_64 (amd64) and arm64 are supported on Windows." }
    }
}

function Get-LatestVersion {
    $api = "https://api.github.com/repos/$Repo/releases/latest"
    try {
        $response = Invoke-RestMethod -Uri $api -UseBasicParsing
        return $response.tag_name
    } catch {
        # Covers both a failed request and a response whose JSON lacks tag_name
        # (accessing a missing property throws under Set-StrictMode Latest).
        throw "Could not determine latest version. Set GRAFT_VERSION manually."
    }
}

function Get-RemoteFile {
    param([string]$Url, [string]$Dest)
    Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing
}

function Test-PathContains {
    param([string]$Dir)
    ($env:PATH -split ';') -contains $Dir
}

function Add-ToUserPath {
    param([string]$Dir)
    # Read the persisted user PATH from the registry (not $env:PATH, which is the
    # merged Machine+User+process value); prepend $Dir if it isn't already there.
    $userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
    $entries  = if ($userPath) { $userPath -split ';' } else { @() }
    if ($entries -notcontains $Dir) {
        $newPath = if ($userPath) { "$Dir;$userPath" } else { $Dir }
        # SetEnvironmentVariable at User scope persists the change and broadcasts
        # WM_SETTINGCHANGE so newly launched processes pick it up.
        [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
        Write-Success "Added $Dir to your user PATH"
    }
    # Make graft usable in the current session immediately, without a new terminal.
    if (($env:PATH -split ';') -notcontains $Dir) {
        $env:PATH = "$Dir;$env:PATH"
    }
    Write-Warn "Open a new terminal for the PATH change to apply everywhere."
}

# =============================================================================
# main
# =============================================================================

Write-Info "Installing graft..."
Write-Host ""

$arch = Get-Arch

$version = $env:GRAFT_VERSION
if (-not $version) {
    Write-Info "Fetching latest version..."
    $version = Get-LatestVersion
    if (-not $version) { throw "Could not determine latest version. Set GRAFT_VERSION manually." }
}

$archive     = "${Binary}_windows_${arch}.zip"
$downloadUrl = "https://github.com/$Repo/releases/download/$version/$archive"
$checksumUrl = "https://github.com/$Repo/releases/download/$version/SHA256SUMS"

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmpDir | Out-Null

try {
    Write-Info "Downloading $Binary $version (windows/$arch)..."
    $archivePath = Join-Path $tmpDir $archive
    try {
        Get-RemoteFile -Url $downloadUrl -Dest $archivePath
    } catch {
        throw "Download failed. Make sure $version exists: https://github.com/$Repo/releases"
    }

    # --- verify checksum -------------------------------------------------------
    Write-Info "Verifying checksum..."
    $checksumPath      = Join-Path $tmpDir 'SHA256SUMS'
    $checksumAvailable = $false
    try {
        Get-RemoteFile -Url $checksumUrl -Dest $checksumPath
        $checksumAvailable = $true
    } catch {
        Write-Warn "Could not download checksum file: $($_.Exception.Message)"
    }

    if ($checksumAvailable) {
        $line = Get-Content $checksumPath |
            Where-Object { $_ -match "^[0-9a-f]+\s+$([regex]::Escape($archive))\s*$" }
        if ($line) {
            $expected = ($line -split '\s+')[0].ToLower()
            $actual   = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
            if ($expected -ne $actual) {
                throw "Checksum mismatch — download may be corrupted"
            }
            Write-Success "Checksum verified"
        } else {
            Write-Warn "No checksum entry found for $archive — skipping verification"
        }
    }

    # --- extract ---------------------------------------------------------------
    Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force

    # --- install ---------------------------------------------------------------
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir | Out-Null
    }
    $exeName = "$Binary.exe"
    $dstExe  = Join-Path $InstallDir $exeName
    Move-Item -Path (Join-Path $tmpDir $exeName) -Destination $dstExe -Force

    Write-Host ""
    Write-Success "$Binary $version installed to $dstExe"
    Write-Host ""

    if (-not (Test-PathContains $InstallDir)) {
        Add-ToUserPath $InstallDir
        Write-Host ""
    }
    Write-Host "Run " -NoNewline
    Write-Host "graft --help" -ForegroundColor Cyan -NoNewline
    Write-Host " to get started."
} finally {
    Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
}
