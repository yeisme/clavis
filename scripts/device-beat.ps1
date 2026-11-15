# device-beat.ps1 — 跨设备心跳（Windows 侧）
# 用法：powershell -ExecutionPolicy Bypass -File scripts\device-beat.ps1
# 产出：docs/device-status/windows.json，然后自动 commit（不 push，由人确认后推）
$ErrorActionPreference = "Stop"
$repo = git rev-parse --show-toplevel
$device = "windows"
$branch = (git rev-parse --abbrev-ref HEAD).Trim()
$head = (git rev-parse --short HEAD).Trim()
$dirty = (git status --porcelain | Measure-Object -Line).Lines
$ahead = 0
try { $ahead = [int](git rev-list --count "@{u}..HEAD" 2>$null) } catch { $ahead = -1 }
$status = [ordered]@{
  device    = $device
  platform  = "windows"
  project   = "clavis"
  branch    = $branch
  head      = $head
  dirty     = $dirty
  ahead     = $ahead
  timestamp = (Get-Date).ToString("yyyy-MM-ddTHH:mm:ssK")
  tests     = $args[0]
  notes     = $args[1]
}
$out = Join-Path $repo "docs/device-status/$device.json"
$status | ConvertTo-Json | Set-Content -Path $out -Encoding UTF8
Write-Host "wrote $out"
git add $out
git commit -m "[win] chore(beat): device heartbeat $head" -- $out | Out-Null
Write-Host "committed (not pushed). Push when ready: git push origin $branch"
