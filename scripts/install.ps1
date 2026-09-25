# ycode installer (Windows PowerShell). Usage:
#   irm https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.ps1 | iex
#
# Env overrides:
#   $env:YCODE_VERSION="v0.11.0"   pin a specific release tag (default: latest)
#   $env:BIN_DIR="C:\tools"        install location (default: $env:LOCALAPPDATA\ycode\bin)
#   $env:YCODE_SKIP_CHECKSUM="1"   skip SHA256 verification (not recommended)
$ErrorActionPreference = "Stop"

$Repo = "Yash-K-Jagani/ycode"
$BinDir = if ($env:BIN_DIR) { $env:BIN_DIR } else { "$env:LOCALAPPDATA\ycode\bin" }

# Detect the OS architecture, not just "is 64 bit": a 64-bit OS can host a
# 32-bit PowerShell (WOW64), and Apple Silicon / Windows-on-ARM need arm64.
$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  "x86" {
    if ([Environment]::Is64BitOperatingSystem) {
      Write-Warning "32-bit PowerShell detected; assuming 64-bit OS (amd64)."
      "amd64"
    } else {
      throw "32-bit Windows is not supported."
    }
  }
  default { "amd64" }
}

$Tag = if ($env:YCODE_VERSION) { $env:YCODE_VERSION } else { "latest" }
if ($Tag -eq "latest") {
  $Base = "https://github.com/$Repo/releases/latest/download"
} else {
  $Base = "https://github.com/$Repo/releases/download/$Tag"
}

# goreleaser packages Windows as zip (archive format override) from v0.12.0
# onward; earlier releases shipped tar.gz. Try each in turn so pinning an older
# tag with YCODE_VERSION still works. Probing with -Method Head is not an option:
# PowerShell 5.1 does not follow the cross-host redirect that release downloads
# issue, so every probe would report 404.
$Stem = "ycode_windows_$Arch"
$Candidates = @("$Stem.zip", "$Stem.tar.gz")

Write-Host "ycode installer"
Write-Host "  os/arch : windows/$Arch"
Write-Host "  version : $Tag"
Write-Host "  dest    : $BinDir\ycode.exe"
Write-Host ""

New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
$tmp = Join-Path $env:TEMP ("ycode-" + [Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
  # Download the first candidate that exists.
  $Asset = $null
  $archive = $null
  foreach ($c in $Candidates) {
    $dest = Join-Path $tmp $c
    try {
      Write-Host "downloading $Base/$c"
      Invoke-WebRequest -Uri "$Base/$c" -OutFile $dest -ErrorAction Stop
      $Asset = $c
      $archive = $dest
      break
    } catch {
      Write-Host "  not available, trying the next archive format"
    }
  }
  if (-not $Asset) {
    throw "no release asset found for windows/$Arch at $Base (tried: $($Candidates -join ', ')). Check that YCODE_VERSION names a real tag."
  }
  $SumUrl = "$Base/checksums.txt"

  # Verify SHA256 against the release checksums before executing anything.
  if ($env:YCODE_SKIP_CHECKSUM -eq "1") {
    Write-Warning "checksum verification skipped (YCODE_SKIP_CHECKSUM=1)"
  } else {
    $sumFile = Join-Path $tmp "checksums.txt"
    try {
      Invoke-WebRequest -Uri $SumUrl -OutFile $sumFile
    } catch {
      throw "could not download checksums.txt from $SumUrl - refusing to install. (set YCODE_SKIP_CHECKSUM=1 to bypass at your own risk)"
    }
    $line = Get-Content $sumFile | Where-Object { $_ -match "\s\*?$([regex]::Escape($Asset))\s*$" } | Select-Object -First 1
    if (-not $line) { throw "no checksum published for $Asset - refusing to install" }
    $expected = ($line -split '\s+')[0].ToLower()
    $actual = (Get-FileHash -Path (Join-Path $tmp $Asset) -Algorithm SHA256).Hash.ToLower()
    if ($expected -ne $actual) {
      throw "checksum mismatch for $Asset`n  expected $expected`n  actual   $actual`nrefusing to install. The download may be corrupted or tampered with."
    }
    Write-Host "checksum verified"
  }

  # zip from v0.12.0 onward, tar.gz before that.
  if ($Asset.EndsWith(".zip")) {
    Expand-Archive -Path $archive -DestinationPath $tmp -Force
  } else {
    tar -xzf $archive -C $tmp
    if ($LASTEXITCODE -ne 0) { throw "failed to extract $Asset" }
  }
  if (-not (Test-Path (Join-Path $tmp "ycode.exe"))) {
    throw "ycode.exe not found inside $Asset"
  }
  Copy-Item (Join-Path $tmp "ycode.exe") (Join-Path $BinDir "ycode.exe") -Force

  Write-Host ""
  Write-Host "installed to $BinDir\ycode.exe"
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ($userPath -notlike "*$BinDir*") {
    Write-Host "note: $BinDir is not on your PATH - add it to use 'ycode' directly"
  }
  & (Join-Path $BinDir "ycode.exe") version
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
