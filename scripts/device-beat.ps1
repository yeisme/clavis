# device-beat.ps1 — 跨设备心跳（Windows 侧）
# 用法：powershell -ExecutionPolicy Bypass -File scripts\device-beat.ps1 [-Tests "..."] [-Notes "..."] [-Device "windows"] [-NoCommit]
# 产出：docs/device-status/windows.json，默认自动 commit（传 -NoCommit 可跳过；不 push，由人确认后推）
param(
  [Parameter(Position = 0)]
  [string]$Tests = "",
  [Parameter(Position = 1)]
  [string]$Notes = "",
  [string]$Device = "windows",
  [switch]$NoCommit
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($Device) -or $Device -match '[/\\\s]' -or $Device.Contains("..")) {
  throw "Invalid device name: '$Device'"
}

$repo = (git rev-parse --show-toplevel).Trim()
$branch = (git rev-parse --abbrev-ref HEAD).Trim()
$head = (git rev-parse --short HEAD).Trim()
$dirty = @(git status --porcelain).Count
$ahead = -1
try {
  $aheadRaw = git rev-list --count "@{u}..HEAD" 2>$null
  if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($aheadRaw)) {
    $ahead = [int]$aheadRaw.Trim()
  }
} catch {
  $ahead = -1
}

$status = [ordered]@{
  device    = $Device
  platform  = "windows"
  project   = "clavis"
  branch    = $branch
  head      = $head
  dirty     = $dirty
  ahead     = $ahead
  timestamp = (Get-Date).ToString("yyyy-MM-ddTHH:mm:ssK")
  tests     = $Tests
  notes     = $Notes
}

$outDir = Join-Path $repo "docs/device-status"
if (-not (Test-Path $outDir)) {
  New-Item -ItemType Directory -Path $outDir -Force | Out-Null
}
$out = Join-Path $outDir "$Device.json"
$status | ConvertTo-Json -Depth 4 | Set-Content -Path $out -Encoding UTF8
Write-Host "wrote $out"

if ($NoCommit) {
  Write-Host "skipped git commit (-NoCommit)"
  exit 0
}

git add $out
git commit -m "[win] chore(beat): device heartbeat $head" -- $out | Out-Null
Write-Host "committed (not pushed). Push when ready: git push origin $branch"
