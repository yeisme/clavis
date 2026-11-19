# 设计：多设备使用（跨设备同步）

Status: `observed` 现状 + `inferred` 设计。基线 HEAD `21ac2db`。事实以本仓代码为准，逐条给 `path:line`。

## 0. 结论

**协议骨架值得保留（签名/墓碑/序列/硬拒绝都是对的），但三条地基是错的：加密是单向的、身份不落盘、Client 锁死在一个内存 Server 上。这三条必须先改，否则补多少传输都没用。**

判断依据：

1. 值得留的：AAD 把 account/device/object/version/class 全绑进签名域、墓碑 append-only + 逐批确认 + watermark、`passwords`/`key_manager` 双层硬拒、恢复码门控。这些重写只会更差。
2. **致命伤一（G1）**：`sync.go:910` 用 `sha256(发送方私钥)` 当 AES 密钥。B 设备收到的密文是 A 用 A 的私钥派生的密钥加密的，B 没有 A 的私钥 → **物理上解不开**。这不是「缺 pull」，是加密模型错：必须改成 per-account 数据密钥，用 ECDH/HKDF 给每台设备包裹一份。
3. **致命伤二（G4）**：`NewClient` 每次启动重新生成 ed25519 密钥（`sync.go:731`），私钥/credential/watermark 无持久化 → 重启就是一台新设备；而服务端公钥不可覆盖（`sync.go:507`）→ 旧设备条目永远挂着。
4. **致命伤三（G3/G5）**：`Client.server` 是 `*Server` 具体指针（`sync.go:714`），`SyncStore` 接口接不进去，已写好的 `SQLiteSyncStore`（含 schema + 迁移）是死代码。
5. 顺带：`Client` 根本没有下行方法（方法表 `sync.go:741-1113` 只有 Login/Pair/Delete/Sync/Upload/Recovery/Fingerprint），所以「A 改了 B 看得见」今天不成立。
6. 一个界面入口都没有，i18n 表里 `sync`/`同步` 零命中。

押注：**局域网直连（自签 TLS + 设备指纹 pin）为主，云盘只放密文冷备**。不做常驻中继服务，不做 mDNS/DNS-SD 依赖。

顺带一条纪律：`pkg/core/sync` 的 `NewServer()` 请在注释里标死「仅仿真/测试」，禁止把仿真器当生产接进真链路 —— 它没有持久化、没有并发隔离、审计会跨账号泄漏（`Server.Audit()` 无参返回全部账号）。

## 1. 现状盘点

### 1.1 已实现（`observed`）

| 能力 | 证据 | 说明 |
| --- | --- | --- |
| 数据分类 | `sync.go:29-40`（8 类）、`sync.go:85 Items()` | `input_method / learning_data / clipboard_text / media / application_context / sensitive_vault / passwords / key_manager` |
| 硬拒绝 | `sync.go:886-891` | `passwords`、`key_manager` 永远拒绝上传 |
| 策略门 | `sync.go:62 Validate()`、`sync.go:893` | 每类需显式允许；`sensitive_vault` 还需 Vault 授权 |
| 共享基线 / 设备覆盖 | `sync.go:92 PersonalBaseline`、`sync.go:125 DeviceOverlay` | baseline 的 `Values` 用 `json:"-"` 故意不序列化 |
| 密文封装 | `sync.go:221 EncryptedEnvelope`（`aad()`/`canonical()`/`Verify()`）、`sync.go:905 NewEncryptedEnvelope` | 12 字节 nonce、AAD 绑定 account/device/object/version/class |
| 删除与防重放 | `sync.go:166 DeleteInstruction`、`sync.go:202 Tombstone`、`sync.go:517 SubmitDelete`、`sync.go:552 SyncTombstones`、`sync.go:592 ConfirmTombstones` | 单调序列 + 逐批确认 + watermark（`sync.go:963`） |
| 版本单调 | `sync.go:671` | 服务端拒绝 `version <= current`：**只能前进，不能回滚** |
| 配对 | `sync.go:491 PairDevice`、`sync.go:513 PairDeviceWithCredential` | 只登记设备公钥，无带外校验 |
| 保险库 | `sync.go:967 Vault`、`sync.go:985 deriveVaultKey`、`sync.go:1021 ConfigureAuthorization` | root + 独立授权因子派生；revoke 轮换 root 使旧密文失效 |
| 恢复码 | `sync.go:927 PrepareRecoveryCode`、`sync.go:940 ConfirmRecoveryCode`、`sync.go:958 SyncEnabled` | 未确认恢复码前同步不可用 |
| 本地缓存清理 | `sync.go:312 LocalCache`、`sync.go:350 ApplyTombstones` | 墓碑驱动清明文/密文/媒体/备份/导出 |
| 落库抽象 | `internal/storage/sync_sqlite_store.go`、`sync.go:spec store.go:18` | `SyncStore` 接口 + SQLite 实现 |
| 迁移差异模型 | `inputmigration/migration.go:201 Export`、`:229 Preview`、`:221 Diff`、`:159 DeviceCapabilities`、`:234 FieldConflict` | 已有「基线+覆盖+可解释差异」的模型，但没接同步 |

### 1.2 缺口（本设计要补）

| # | 缺口 | 严重度 |
| --- | --- | --- |
| G1 | **信封密钥是单向的**：`sync.go:910` 用 `sha256(发送方私钥)` 做 AES 密钥 → 接收设备即使收到密文也解不开。**不是缺 pull 的问题，是加密模型必须换** | 阻断 |
| G2 | **没有对象下载**：`Client` 无 `Pull`/`Download`（`sync.go:741-1113` 方法表里没有），`GetEnvelope` 只在服务端侧（`server_store.go:44`、`sync_sqlite_store.go:969`） | 阻断 |
| G3 | **没有传输**：`Client.server` 是 `*Server` 具体指针（`sync.go:714`），不是接口；本包不 import `net`/`os`，唯一线格式是 JSON 编解码 | 阻断 |
| G4 | **客户端身份不持久化**：`NewClient` 每次启动都 `ed25519.GenerateKey`（`sync.go:731`）；私钥/credential/watermark 全在内存 → **重启即变成新设备**，而服务端「公钥不可覆盖」（`sync.go:507-509`）会让旧条目永久残留 | 阻断 |
| G5 | **持久化实现接不进去**：`SyncStore` 接口有、`SQLiteSyncStore` 也有（含 schema 与迁移），但 `Client` 只吃 `*Server` 具体类型 → 已写好的持久化层是死代码 | 高 |
| G6 | **没有配对带外校验**：`pairDevice` 只校验 credential + 公钥长度，能拿到 credential 的进程都能加设备；无短码/二维码、无设备吊销（`Server.accounts` 只增不减） | 高 |
| G7 | **没有冲突表达**：服务端只有「版本必须更新」；上传被拒时客户端只拿到一个 error 字符串 | 高 |
| G8 | **DataClass 不覆盖真正要同步的东西**：片段/模板/术语表/配置基线/`usage_frecency`/`prompt_history` 全无 class；`application_context` 与 `InputMethod` 两个 class **无生产者** | 高 |
| G9 | **删除版本语义错位**：`DeleteInstruction.Version` 取 `c.watermark+1`（`sync.go:795`，墓碑游标），服务端却当对象版本边界用（`sync.go:527-534`） | 中 |
| G10 | **两套同名 `PersonalBaseline`**：`sync.go:92` 与 `inputmigration/migration.go:119` 互不兼容，无转换代码；`inputmigration.MergeBaselines`（`migration.go:1136-1188`）字段级冲突合并**已实现但无调用者** | 中 |
| G11 | **恢复码不参与密钥派生**：只在 `sync.go:721/934/943` 当开关用，设备丢失即数据不可达（无 key wrapping） | 中 |
| G12 | **Vault 只有内存**：root 随机生成不落盘（`sync.go:978`），重启即丢；vault 密文从未上传（`Vault.Ciphertext` 无调用者） | 中 |
| G13 | **没有界面**：无设备页/开关/冲突列表/审计视图；i18n 表里 `sync`/`同步` 零命中 | 中 |
| G14 | 未知 class 无处理规则（`ProtocolVersion != ProtocolVersion` 直接整批拒绝） | 低 |
| G15 | 同步侧不查剪贴板隐私过滤：`pkg/core/clipboard/filter.go` 与 `pkg/core/sync` 无任何引用关系 | 中 |

## 2. 目标场景（每条都是验收用例）

| # | 场景 | 期望 |
| --- | --- | --- |
| U1 | mini + Air 同一个人 | 配置基线共享；按键层（caps lock tap/hold）作为设备覆盖各自独立；新装 Air 30 秒内可用 |
| U2 | Windows 工作站 | 输入法学习（词频+bigram）双向同步 —— 同一份引擎要两平台词库一致；剪贴板媒体默认不同步（体积） |
| U3 | 模板 pin 不同 | 显示「设备差异」不自动覆盖（见 `template-platform.md` §8） |
| U4 | 删除传播 | A 删片段 → B 同步后消失且不可复活（墓碑永久） |
| U5 | 离线冲突 | 两台都改同一片段 → **两份都保留**（`<name> (mini)` 副本），让用户二选一或都留 |
| U6 | 新设备接入 | baseline 全量拉取 + overlay 空；不允许继承对方的设备专属覆盖 |
| U7 | 撤销设备 | 踢掉 Windows → 服务端删其公钥；该设备现有数据仍在本地但无法再推 |

U5 的立场：**默认「都保留」而不是 LWW**。LWW 在「两台离线各自编辑」时必然静默丢数据，而片段/模板这类东西丢了用户很难察觉。代价是可能产生重复副本，需要 `冲突` 列表收口 —— 这个代价比静默丢数据小。

## 3. 传输层选型

| 方案 | 优点 | 缺点 | 判定 |
| --- | --- | --- | --- |
| A. 局域网直连（自签 TLS + 指纹 pin + 手动地址） | 零运维、零数据出户、纯 stdlib（`crypto/tls` + `net`）、离线可用 | 跨网段/外出不可用；要处理防火墙与地址变化 | **主方案** |
| B. 自建中继（s101 跑 clavis-sync） | 任意网络可用、可审计 | 要运维一个服务、要证书、隐私重心从设备移到服务器 | 备选（仅在 A 不够时） |
| C. 云盘同步文件（iCloud/WebDAV/S3） | 最省事 | 文件级 LWW 会直接毁 SQLite（WAL 不一致）；冲突不可表达 | **只允许密文冷备**，不承载实时同步 |

A 的地址问题不引入 DNS-SD：配对时交换并记录「设备 ID → 候选地址（IP/hostname + 端口）」，之后每 5 秒一个 UDP 广播 beacon（自实现 256B，含 deviceID、指纹前 8 位、端口、协议版本），收到即更新地址。不解析 mDNS 报文、不依赖 Bonjour —— 三个设备、自己可控的网络，不值得为服务发现引一个依赖。

TLS：自签证书（`ed25519` 身份密钥直接签），对端用「配对时记录的指纹」做 pin，不走系统信任链，不复用 CA。

## 4. 密码学重做（先做这个，否则传输白搭）

现状：`NewEncryptedEnvelope` 用 `sha256(发送方 ed25519 私钥)` 当 AES-256-GCM 密钥（`sync.go:905-921`）。发送方与接收方不是同一台机器、密钥不同 → 收方解不开。

改成标准的多设备模型，三步：

```text
① 账号数据密钥（ADK）
   ADK = 32 字节随机，只在设备内存与本地 keystore 里，永不上传明文
   恢复码 = ADK 的封装形式（Argon2id/Scrypt 派生 KEK → AES-GCM 包 ADK）
   这样「丢失所有设备」也能用恢复码取回；恢复码不再是纯开关（修 G11）

② 每设备密钥（持久化）
   device_priv/pub = ed25519，落 keystore：`internal/secrets/secrets.go:78 NewMyGoSecretsStore`（底层 mygo.Secrets → macOS Keychain / Windows Credential Manager）
   已有 `SaveKey/ReadKey/DeleteKey`（`secrets.go:136/120/151`）可直接复用，键名用 `clavis.sync.device.<deviceID>.priv`
   pub 走配对流程登记；私钥重启后照样是同一台设备（修 G4）

③ 包裹（wrap）
   黑客噪音：每个 device 存一份 wrapped_ADK = ECDH(account_pub?, device_pub) ... 
   实际做法：wrapped_ADK = AES-GCM(KEK = HKDF(ECDH(sender_priv, receiver_pub), "clavis.sync.wrap.v2"), ADK)
   上传新设备时，由任一台已授权设备把 wrapped_ADK 交给它
   服务端只存 wrapped_ADK 与密文，依然零知识
```

加密的对象 payload 一律用 **ADK**（不再是发送方私钥），签名仍用设备私钥（不可否认性保留）。信封里加 `KeyID`（ADK 版本）与 `WrappedFor`（接收方设备指纹），`validClass`/AAD 一并升到 v2。

收益与代价：收益是终于能双向解密、恢复码真正能救命；代价是要做密钥迁移（v1 密文无法直接复用 → 首次升级要么重传，要么由设备重加密一次）。**建议直接重传**（对象都是小配置/片段，不存在历史包袱），不要写 v1→v2 密文迁移器。

## 5. 配对与信任

```text
A（已授权设备）                      B（新设备）
[生成配对包]                        [扫码/输入短码]
  payload = {account, device, pub, addrs, fingerprint, expires=120s}
  → 二维码 + 8 位短码（指纹前 8 位）
                                     POST /pair {payload, 自己的 pub}
  校验短码 + 指纹 → 确认             ← 返回 {credential, wrapped_ADK}
  服务端登记两条公钥                 → 拉 baseline，overlay 置空
```

硬规则：

- 短码 120 秒过期、一次性；同一 account 同时只允许一个未完成配对。
- 拒绝「只凭 account 名 + 公钥」的静默配对（G6）。`PairDevice` 必须带一个由 A 签名的授权票（`ed25519` 签 `{account,device,pub,expires,nonce}`），并携带 A 为该设备生成的 `wrapped_ADK`。
- 配对的信道校验是**双向**的：B 也要显示自己的指纹前 8 位让 A 确认，防中间人。
- 设备列表可撤销：撤销删公钥 + 记审计；已同步的密文不清（其他设备仍要读历史）。新增 `Server.RemoveDevice`（现在 `accounts` 只增不减）。
- 恢复码是**最后手段**：新设备用恢复码解出 ADK，走「恢复」流程，事件标红进审计。

## 6. 同步协议（在现有协议上加最小必要件）

### 6.1 对象模型

```text
objectID  = "<class>/<scope>/<id>"        # 例：snippet/personal/2f1c、ime.learning/device/win
version   = 单调递增 uint64（服务端保证严格递增）
tombstone = {objectID, version, deletedAt, signature}
payload   = 已由 Envelope 加密的不透明字节
```

### 6.2 补 G2 + G1：Pull（并按 ADK 解密）

新增 `Client.Pull(after map[string]uint64) ([]Object, error)`：

- 请求带「每个 object 我当前版本」的摘要不会太小也可只带 watermark（服务端按 `version > since` 返回变更）；
- 服务端返回 `{objectID, version, class, envelope}`，客户端**先验签再解密**，验签失败整批拒收并告警；
- 拉取后本地写入走 `SyncStore`，与墓碑应用同一事务顺序（先墓碑后对象，防复活）。

这一步同时解决 G4：有了 pull，冲突就能在客户端被判成「本地 v5 / 远端 v7，且本地 v5 的修改未推成功」→ 进入冲突列表，而不是一个 error 字符串。

### 6.3 版本冲突的显式处理

上传被拒（`version <= current`）时不再直接报错，走 rebase：

1. pull 该对象最新版本；
2. 与本地版本做字段级 diff（模板/片段这类小对象，diff 很便宜）；
3. 双方都改过同一字段 → 生成保留副本（`<id>@<device>.<ts>`）+ 冲突条目；
4. 只有一方改过 → 采用远端版本，本地改动标记为已合并。

### 6.4 补 G14：未知 class 与协议版本

协议版本从 1 升到 2，同时改两处语义：

- 收到未知 class：**不报错、不落盘、记一条 `unsupported_class` 诊断**（老设备遇到新类只是不参与，不应整批失败）；
- `ProtocolVersion` 只用于行为不兼容的语义变化，不用于新增枚举值。

## 7. DataClass 扩展

| 现有 class | 承载 | 默认 |
| --- | --- | --- |
| `input_method` | 输入法引擎配置（方案/模糊音/候选数/标点） | 开 |
| `learning_data` | 词频、bigram、用户词库 | 开 |
| `clipboard_text` | 剪贴板文本历史 | 开（敏感类型过滤沿用现有 privacy filter） |
| `media` | 剪贴板图片/文件 | **关**（体积；需要时手动） |
| `application_context` | 应用绑定规则、IME 规则 | 开 |
| `sensitive_vault` | 敏感条目 | 关（需 Vault 授权） |
| `passwords` / `key_manager` | — | 永久禁止 |

新增（G8）：

| 新 class | 承载 | 默认 | 说明 |
| --- | --- | --- | --- |
| `templates` | 导入清单 + 覆盖层（不含正文） | 开 | 正文按 digest 从目录取，天然一致 |
| `snippets` | 片段（触发词 + 正文） | 开 | 冲突走 §6.3 |
| `glossary` | 术语表 | 开 | |
| `config_baseline` | Leader 树、窗口管理、外观等共享配置 | 开 | 按键层/重映射**不进这个类**，走 `input_method` 的设备覆盖 |

按键层为什么不算 baseline：caps lock tap-hold 的可用性取决于具体键盘与 OS，强行同步会让另一台机器误触。这类放 `DeviceOverlay`，不跨设备。

## 8. 界面（G13）

设置中心新增「设备」页：

```text
本机：mini（指纹 a1b2c3d4）        状态：已启用同步 · 上次同步 2 分钟前
[配对新设备]  [用恢复码接入]  [暂停同步]

设备列表
  mini     · macOS   · 在线（beacon 3s 前）· 版本 v12  · [撤销]
  windows  · Windows · 离线（2 天）        · 版本 v9   · [撤销]
  air      · macOS   · 在线                · 版本 v12  · [撤销]

同步内容（每类一个开关，敏感项灰掉并注明原因）
  [x] 输入法配置   [x] 学习数据   [x] 片段   [x] 模板清单
  [x] 术语表       [x] 应用规则   [ ] 剪贴板文本   [ ] 媒体
  [ ] 敏感保险库（需授权）   密码/钥匙串：永不同步

冲突（2）
  snippet:2f1c  「mini 版 / windows 版」  [保留 mini] [保留 windows] [都保留]
  ...

审计（最近 20 条，只含元数据）
  09:12 windows → snippet/2f1c v8 已合并
  09:10 air ← template 清单 3 项已更新（无正文）
```

离线/异常必须显示**原因**，不能只显示「同步失败」：`需要重新配对`、`证书指纹变更`、`端口不可达`、`版本落后需 rebase`。

## 9. 与另外两条线的耦合

- **模板线**：`templates` class 只同步清单与覆盖层（`template-platform.md` §2/§8）；正文按 digest 从目录读，不需要搬字节。这让模板同步变成「几十 KB」的廉价操作。
- **输入法线**：`learning_data` 是跨平台引擎的最大资产（Windows 和 macOS 用同一份 `pkg/core/inputengine`），必须双向；`input_method` 同步方案与模糊音但不含键位层。
- **herdr**：`docs/device-status/*.json` 心跳是**开发协作**心跳，与本产品同步无关，两套东西不要混。设备页可以顺带显示「开发心跳」状态，但必须分开标注。

## 10. 批次与验收

顺序不可换：**密码学 → 身份落盘 → Client 可替换 → 传输 → 配对 → UI**。前四步任何一步跳过，后面都是空转。

| 批 | 内容 | 估时 | 验收 |
| --- | --- | --- | --- |
| S1 | 密码学 v2：ADK + ECDH 包裹 + 恢复码封装 ADK + 信封加 `KeyID`/`WrappedFor` | 12h | 两台设备（不同密钥）互传对象，各自都能解密；恢复码能把 ADK 解出来；发送方私钥换了仍能读历史密文 |
| S2 | 身份与状态落盘：设备密钥对、credential、watermark、ADK 进 keystore/SQLite | 6h | 进程重启后 `Fingerprint()` 不变，不需要重新配对；服务端不会残留孤儿设备 |
| S3 | `Client` 改吃 `SyncStore` 接口（而非 `*Server`）+ `Pull` + rebase 冲突判定 | 10h | SQLite 实现真正接入（不再死代码）；两客户端同对象并发改 → 一推一拉后进冲突列表且两份都在 |
| S4 | `Transport` 接口 + 局域网 TCP/TLS（自签 + 指纹 pin）+ UDP beacon | 14h | 两台本机进程（不同端口/数据目录）互相同步；改指纹 → 连接必须失败并给出明确原因 |
| S5 | 配对（短码 + 二维码 + 授权票 + 双向指纹 + 撤销 `RemoveDevice`） | 10h | 短码过期必失败；无授权票的 `PairDevice` 必拒；撤销后上传必失败 |
| S6 | DataClass 扩展 + 未知类容错 + 协议 v2 迁移 + 隐私过滤接入 | 8h | 老客户端收到新类不崩、不落盘、记诊断；剪贴板敏感类型不进同步（复用 `filter.go`） |
| S7 | 设置页「设备」+ 冲突 UI + 审计视图 + i18n | 12h | `ui.NewTester` 帧断言：设备列表/开关/冲突项可渲染；敏感项开关不可点；中英文案都在 |
| S8 | 云盘密文冷备（可选，P2） | 8h | 冷备包可被另一台恢复出与在线同步一致的对象集；不得含明文（正则断言） |

合计约 80h。对比前一版估的 58h，多出来的 22h 全在「密码学重做 + 身份落盘」—— 这两件原来被漏掉了。

## 11. 风险与回退

| 风险 | 缓解 / 回退 |
| --- | --- |
| 密码学重做打断既有 v1 密文 | 对象都很小（配置/片段/清单），**直接重传**，不写 v1→v2 密文迁移器；升级时把 v1 对象清掉重发 |
| ADK 丢失（keystore 被清 + 恢复码也丢） | 数据不可恢复，这是设计选择；设置页必须明示「恢复码是你唯一的后路」，并在开启同步时强制用户抄写 |
| 自签 TLS + pin 的证书轮换把设备锁死 | 指纹变更时进入「需人工确认」而不是直接拒；确认需在已授权设备上操作 |
| Windows 防火墙默认拦截 | 首次配对时给出明确的入站规则指引；失败原因必须区分「连接被拒」与「端口不可达」 |
| SQLite 在同步中损坏 | 同步写走单连接串行 + 事务；媒体/大对象不进同一个库 |
| 版本单调导致「老设备永远推不上去」 | rebase 流程兜底（§6.3），并在设备页显示「落后 N 个版本」 |
| 剪贴板同步把密码带到别的设备 | 复用现有 privacy filter + 类型黑名单；`clipboard_text` 默认虽是开，但首次开启要求确认 |
| 内存 Server 被误当生产 | `NewServer()` 注释标死「仅测试/仿真」；真实链路必须走 `Transport`；`Server.Audit()` 无参跨账号返回全部审计（`sync.go:684-697`），生产实现必须去掉这个重载 |

## 12. 明确不做

- 不做账号体系/云端登录（只有设备互信 + 恢复码）。
- 不做团队共享（这是单人多设备设计）。
- 不做冲突的自动三方合并（只做 rebase + 保留副本）。
- 不做实时协同（无 CRDT、无 OT）。
- 不同步密码/钥匙串（永久红线）。
- 不做 mDNS/DNS-SD、不引第三方服务发现库。
- 不做 v1 密文迁移器（直接重传）。
