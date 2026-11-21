# 跨设备心跳协议

同一项目在两台以上设备并行开发时（当前：clavis = macOS mini + Windows 工作站），
herdr 只能看见装有 herdr 的机器。Windows 侧不可见，因此用「心跳文件」代替实时观测。

## 规则

1. 每台设备收尾时跑一次心跳脚本，产出 `docs/device-status/<device>.json` 并提交。
2. 心跳文件必须随 push 进 origin —— 不 push 等于没心跳（P10）。
3. 每次任务收尾必须先写心跳，再逐个 `git add` 自己的交付物、提交并 push；不能把“稍后补心跳”当作收尾完成。
4. 我（云婉）在 mini 上跑台账时读这份文件，读不到 → 该设备标记「失联」。
5. 心跳只写自己的文件，绝不改别人的心跳（避免冲突）。

## 各设备命令

```bash
# macOS / Linux
scripts/device-beat.sh mini "go test ./... 全绿" "合入 ux-polish，main 已推"
```

```powershell
# Windows 一次性收尾命令：先同步远端，再生成 windows.json，最后推送心跳
cd C:\work\clavis
git fetch origin
git status --short
powershell -ExecutionPolicy Bypass -File scripts\device-beat.ps1 "Win 真机冒烟通过" "IMM32 组合检测已改"
git push origin HEAD
```

> `device-beat.ps1` 会读取当前 HEAD、dirty、ahead、timestamp，并只提交
> `docs/device-status/windows.json`。如果工作区有其他并行改动，先不要让脚本把它们
> 混入提交；应在干净或已隔离的收尾状态执行，并确认 `git show --stat --oneline HEAD`
> 只有心跳文件。`git fetch` 只更新远端引用，不会替代 `git push`；不 push 仍算无心跳。

## 字段

| 字段 | 含义 |
|---|---|
| device | 设备标识（mini / windows / air） |
| platform | macos / windows |
| branch / head | 当前分支与提交 |
| dirty | 未提交文件数 |
| ahead | 领先 upstream 的提交数（>0 表示别的设备看不见） |
| project | 项目标识（用于防止读错仓库） |
| timestamp | 心跳生成时间（带时区） |
| tests | 本次验证结论 |
| notes | 人读备注 |

## 为什么不用直接同步

Windows 上的 `mini` remote 直连本机属于旁路：mini 睡眠/换网就断。
主通道必须是 origin（GitHub，私有仓），双通道并存时以 origin 为准。

## 收尾约定

每台设备在任务收尾时必须按以下顺序执行：

1. 完成本机验证，确认工作区状态与当前 HEAD。
2. 运行对应的 `device-beat`，只写自己的 `docs/device-status/<device>.json`。
3. 逐个路径 `git add` 本轮交付物，提交并 `git push origin HEAD`。
4. 用 `git status --short` 和 `git show --stat --oneline HEAD` 确认没有漏报心跳，也没有把其他设备或并行任务的文件带入提交。

**判定标准：只生成本地心跳文件但没有 push，不算收尾，也不算设备在线。**
