# 跨设备心跳协议

同一项目在两台以上设备并行开发时（当前：clavis = macOS mini + Windows 工作站），
herdr 只能看见装有 herdr 的机器。Windows 侧不可见，因此用「心跳文件」代替实时观测。

## 规则

1. 每台设备收尾时跑一次心跳脚本，产出 `docs/device-status/<device>.json` 并提交。
2. 心跳文件必须随 push 进 origin —— 不 push 等于没心跳（P10）。
3. 我（云婉）在 mini 上跑台账时读这份文件，读不到 → 该设备标记「失联」。
4. 心跳只写自己的文件，绝不改别人的心跳（避免冲突）。

## 各设备命令

```bash
# macOS / Linux
scripts/device-beat.sh mini "go test ./... 全绿" "合入 ux-polish，main 已推"
```

```powershell
# Windows
powershell -ExecutionPolicy Bypass -File scripts\device-beat.ps1 "Win 真机冒烟通过" "IMM32 组合检测已改"
```

## 字段

| 字段 | 含义 |
|---|---|
| device | 设备标识（mini / windows / air） |
| platform | macos / windows |
| branch / head | 当前分支与提交 |
| dirty | 未提交文件数 |
| ahead | 领先 upstream 的提交数（>0 表示别的设备看不见） |
| tests | 本次验证结论 |
| notes | 人读备注 |

## 为什么不用直接同步

Windows 上的 `mini` remote 直连本机属于旁路：mini 睡眠/换网就断。
主通道必须是 origin（GitHub，私有仓），双通道并存时以 origin 为准。
