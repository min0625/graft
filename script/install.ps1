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
    $env:GRAFT_VERSION = 'v1.0.0'
    irm https://raw.githubusercontent.com/min0625/graft/main/script/install.ps1 | iex
#>

[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$Repo       = 'min0625/graft'
$Binary     = 'graft'
$InstallDir = if ($env:GRAFT_INSTALL_DIR) { $env:GRAFT_INSTALL_DIR } else { Join-Path $HOME '.local\bin' }

function Write-Info    { param($Msg) Write-Host $Msg }
function Write-Success { param($Msg) Write-Host "[ok] $Msg" -ForegroundColor Green }
function Write-Warn    { param($Msg) Write-Host "[!]  $Msg" -ForegroundColor Yellow }

function Get-Arch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    switch ($arch) {
        'X64'   { return 'amd64' }
        'Arm64' { return 'arm64' }
        default { throw "Unsupported architecture: $arch. Only x86_64 (amd64) and arm64 are supported on Windows." }
    }
}

function Get-LatestVersion {
    $api      = "https://api.github.com/repos/$Repo/releases/latest"
    $response = Invoke-RestMethod -Uri $api -UseBasicParsing
    return $response.tag_name
}

function Get-RemoteFile {
    param([string]$Url, [string]$Dest)
    Invoke-WebRequest -Uri $Url -OutFile $Dest -UseBasicParsing
}

function Test-PathContains {
    param([string]$Dir)
    ($env:PATH -split ';') -contains $Dir
}

function Show-PathHint {
    param([string]$Dir)
    Write-Warn "$Dir is not in your PATH"
    Write-Host ""
    Write-Host "  Add it permanently (current user):"
    Write-Host ""
    Write-Host "    [Environment]::SetEnvironmentVariable('PATH', `"$Dir;`$env:PATH`", 'User')"
    Write-Host ""
    Write-Host "  Or for the current session only:"
    Write-Host ""
    Write-Host "    `$env:PATH = `"$Dir;`$env:PATH`""
    Write-Host ""
}

# =============================================================================
# main
# =============================================================================

Write-Info "Installing graft..."
Write-Host ""

$arch = Get-Arch

$version = if ($env:GRAFT_VERSION) { $env:GRAFT_VERSION } else { $null }
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
    $checksumPath = Join-Path $tmpDir 'SHA256SUMS'
    try {
        Get-RemoteFile -Url $checksumUrl -Dest $checksumPath
        $line = Get-Content $checksumPath | Where-Object { $_ -match [regex]::Escape($archive) }
        if ($line) {
            $expected = ($line -split '\s+')[0].ToLower()
            $actual   = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
            if ($expected -ne $actual) {
                throw "Checksum mismatch — download may be corrupted"
            }
            Write-Success "Checksum verified"
        }
    } catch {
        if ($_.Exception.Message -match 'Checksum mismatch') { throw }
        Write-Warn "Could not verify checksum: $($_.Exception.Message)"
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

    if (Test-PathContains $InstallDir) {
        Write-Host "Run " -NoNewline
        Write-Host "graft --help" -ForegroundColor Cyan -NoNewline
        Write-Host " to get started."
    } else {
        Show-PathHint $InstallDir
    }
} finally {
    Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
}
