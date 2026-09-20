# ycode installer (Windows PowerShell). Usage:
#   irm https://raw.githubusercontent.com/Yash-K-Jagani/ycode/main/scripts/install.ps1 | iex
$ErrorActionPreference = "Stop"
$Repo = "Yash-K-Jagani/ycode"
$BinDir = if ($env:BIN_DIR) { $env:BIN_DIR } else { "$env:LOCALAPPDATA\ycode\bin" }
$Arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { throw "32-bit not supported" }
$Tag = if ($env:YCODE_VERSION) { $env:YCODE_VERSION } else { "latest" }
if ($Tag -eq "latest") {
  $Url = "https://github.com/$Repo/releases/latest/download/ycode_Windows_${Arch}.tar.gz"
} else {
  $Url = "https://github.com/$Repo/releases/download/$Tag/ycode_Windows_${Arch}.tar.gz"
}
Write-Host "downloading $Url"
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
$tmp = Join-Path $env:TEMP ("ycode-" + [Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Invoke-WebRequest -Uri $Url -OutFile (Join-Path $tmp "ycode.tar.gz")
  tar -xzf (Join-Path $tmp "ycode.tar.gz") -C $tmp
  Copy-Item (Join-Path $tmp "ycode.exe") (Join-Path $BinDir "ycode.exe") -Force
  Write-Host "installed to $BinDir\ycode.exe (ensure it is on PATH)"
  & (Join-Path $BinDir "ycode.exe") version
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
