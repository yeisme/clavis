# 输入法与系统输入法共存（hook 路线的冲突最小化）

2026-11-20。背景：目标是让 Clavis 输入法「不和系统的冲突」。IMK（成为系统输入法菜单里的
第三项）这条正路在 macOS 27 上验证失败（见 §4），因此继续走 hook 路线，并把它能做的
冲突消除手段做到位。

## 1. 冲突从哪来

hook 路线（route B：CGEventTap 拦截 + 自绘候选窗 + 注入）本质上是在系统输入法**下面**
再挂一层。两者同时活跃时会互相干扰：

| 冲突现象 | 机制 |
|---|---|
| 两个候选窗同时出现 | 系统 IME 和 Clavis 都在为同一批按键画候选 |
| 引擎「像死的」 | `inlineIMEGate()` 要求「系统源是 ASCII」或「forced」，系统源是拼音时引擎主动让位 |
| 每次启动都要重新摸手势 | `inlineForced` 是内存 `atomic.Bool`，重启归零 |
| 用户不知道能接管 | 只有双击右 Shift 一条入口 |

## 2. 三个改动

### force_takeover：接管状态持久化

`[input_engine] force_takeover` 落盘，启动时在 `ConfigureInputEngine` 之前还原；
双击右 Shift 切换后写回。解决「重启后输入法像死的」。

### auto_takeover：源切到中文就自动接管（默认开）

`NotifyIMESwitch` 收到非 ASCII 源时自动 `SetInlineIMEForced(true)`。
理由：「用户主动切到拼音」就是「我要打中文」的明确信号，不该再要求他摸一个隐藏手势。
`wasTakeover` 为真时不重复触发——避免和用户自己的开关打架。

### lock_system_source：接管时把系统源推回 ASCII（默认开）

引擎接管后调 `LockTemporaryASCII`。这是两个候选窗并存的根治办法：引擎无法抑制系统 IME
的窗口，只能把源挪开。用 `LockTemporaryASCII` 而非直接 `SelectInputSource`，因为它记住
原来源、可恢复，不覆写用户选择。

## 3. 配置

```toml
[input_engine]
force_takeover = false      # 只在非默认时写入
auto_takeover = true
lock_system_source = true
```

## 4. IMK 路线为什么被否（实测记录）

目标是像鼠须管那样成为系统输入法菜单里的独立一项。实际验证了全部环节：

| 环节 | 结果 |
|---|---|
| clang + InputMethodKit 工具链 | 可用（CommandLineTools SDK；**无 IMK 头文件**，需手写 `@interface`） |
| 编译 IMK 二进制 | 通过（51KB） |
| `IMKServer` 初始化 | `server=ok` |
| 系统认它是输入法进程 | imklaunchagent 日志出现 `Received setIMKXPCEndpoint... from InputMethod` |
| 签名 | `valid on disk`、满足 DR |
| LaunchServices 注册（`.app`） | 成功（`bundle id: Clavis输入法`） |
| LaunchServices 注册（`.inputmethod`） | **失败，`lsregister` 报 -10811 = `kLSNotAnApplicationErr`** |
| 补齐 `InputMethodServerDelegateClass` / `NSSupportsSuddenTermination` / `TISIconIsTemplate` | 仍不注册 |
| 补齐 `PkgInfo`(`APPL????`) / `version.plist` / `Resources` / 真 TIFF 图标 | 仍不注册 |
| bundle 结构与 Apple 自带 `PluginIM` 完全一致 | 仍不注册 |
| **TIS 输入源列表** | **始终 331 项，从头到尾没有 Clavis** |

`TISRegisterInputSource` 全程返回 0（成功）但不产生任何效果——API 在骗人。

结论：问题不在 bundle 本身。两个可能，倾向第一个：
1. macOS 27 要求真实 Apple 开发者签名 + 公证，自签名（`TeamIdentifier=not set`）被静默拒绝
2. 需要登出/登录刷新 TIS 表（未验证：登出会杀掉本机其他会话）

代价对比：要真正成为系统输入法需 $99/年开发者账号 + 公证 + 重写一套 ObjC 输入法 +
引擎 IPC 桥接，数周投入；而 hook 路线加上本文三个改动后冲突已基本消除。

## 5. 仍存在的限制（不粉饰）

- **不是系统输入法**：不出现在 macOS 输入法菜单里，不能给别的 App 提供输入法
- 依赖「系统源是 ASCII」或「forced」——`lock_system_source` 让它在接管期间保持 ASCII
- 如果用户在接管期间手动切回拼音，会自动重新接管（`auto_takeover`），可能感觉「抢」
- 无法使用 IMK 的内联预编辑（marked text），预编辑只能画在自己的候选窗里

## 6. 如果以后要走 IMK

前置条件：Apple 开发者账号 + 公证能力。技术骨架已验证可用（见 §4 前六行），
届时需要补的是：ObjC 侧 `IMKInputController` 实现、与 Go 引擎的 IPC（可复用
`pkg/core/pluginprotocol` 的分帧协议，但要把 stdio 换成 Unix socket）、
以及把候选窗改为 NSPanel 或改用 `client.setMarkedText`。
