<#
.SYNOPSIS
  Install (or upgrade) the InternetMonitor agent (imonitord) on Windows 10/11
  or Windows Server 2019+.

.DESCRIPTION
  Run from an elevated PowerShell (Run as administrator):

    irm https://raw.githubusercontent.com/wasim-osman/InternetMonitor/main/scripts/install-agent.ps1 | iex

  or, after downloading this file:

    powershell -ExecutionPolicy Bypass -File .\install-agent.ps1 [-Listen :7878]

  Downloads the release asset for this CPU (amd64 / arm64), verifies it against
  SHA256SUMS and runs `imonitord.exe install`, which copies it to
  %ProgramFiles%\InternetMonitor, registers the "imonitord" Windows service
  (auto start, restart on failure), opens TCP 7878 in Windows Firewall and
  keeps state in %ProgramData%\InternetMonitor. Re-running upgrades in place.

  Environment overrides: IMONITOR_LISTEN, IMONITOR_RELEASE_URL.
#>
param(
  [string]$Listen = $env:IMONITOR_LISTEN,
  [string]$BaseUrl = $(if ($env:IMONITOR_RELEASE_URL) { $env:IMONITOR_RELEASE_URL } else { 'https://github.com/wasim-osman/InternetMonitor/releases/latest/download' })
)

function Install-IMonitorAgent {
  param([string]$Listen, [string]$BaseUrl)

  $ErrorActionPreference = 'Stop'
  $ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest is very slow with the progress bar in PS 5.1
  try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
  } catch { }

  $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
  if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'This installer must run from an elevated PowerShell (right-click PowerShell > Run as administrator).'
  }

  # OS architecture (not the PowerShell process's, which may be emulated x64 on ARM64).
  $osArch = $null
  try { $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { }
  if (-not $osArch) {
    $osArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
  }
  switch -Regex ($osArch) {
    '^(X64|AMD64)$' { $arch = 'amd64' }
    '^(Arm64|ARM64)$' { $arch = 'arm64' }
    default { throw "Unsupported Windows architecture '$osArch' (64-bit x64 or ARM64 Windows is required)." }
  }
  $asset = "imonitord-windows-$arch.exe"

  $tmp = Join-Path ([IO.Path]::GetTempPath()) ('imonitord-' + [Guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    $exe = Join-Path $tmp 'imonitord.exe'
    $sums = Join-Path $tmp 'SHA256SUMS'
    Write-Host "Downloading $asset from $BaseUrl ..."
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$asset" -OutFile $exe
    Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/SHA256SUMS" -OutFile $sums

    $want = $null
    foreach ($line in Get-Content -LiteralPath $sums) {
      $parts = $line.Trim() -split '\s+', 2
      if ($parts.Count -eq 2 -and $parts[1].TrimStart('*') -eq $asset) { $want = $parts[0].ToLowerInvariant(); break }
    }
    if (-not $want) { throw "$asset is not listed in SHA256SUMS" }
    $got = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $want) { throw "Checksum mismatch for $asset (expected $want, got $got)" }
    Write-Host "Checksum OK ($got)"
    Unblock-File -LiteralPath $exe -ErrorAction SilentlyContinue

    $installArgs = @('install')
    if ($Listen) { $installArgs += @('--listen', $Listen) }
    # Native commands: don't let PS 5.1 turn stderr output into terminating errors.
    $ErrorActionPreference = 'Continue'
    & $exe @installArgs
    $ErrorActionPreference = 'Stop'
    if ($LASTEXITCODE -ne 0) { throw "imonitord install failed (exit code $LASTEXITCODE)" }
  } finally {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }

  $installed = Join-Path $env:ProgramFiles 'InternetMonitor\imonitord.exe'
  if (Test-Path -LiteralPath $installed) {
    Write-Host ''
    $ErrorActionPreference = 'Continue'
    for ($i = 0; $i -lt 10; $i++) {
      & $installed pair-code 2>$null
      if ($LASTEXITCODE -eq 0) {
        Write-Host 'Enter this code in the InternetMonitor app to pair.'
        return
      }
      Start-Sleep -Seconds 1
    }
    Write-Host "The agent was installed. Get a pairing code with: & '$installed' pair-code"
  }
}

Install-IMonitorAgent -Listen $Listen -BaseUrl $BaseUrl
