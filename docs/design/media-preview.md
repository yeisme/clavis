# 设计：剪贴板图片与视频预览

基线 `458d7fe`。范围：Windows 优先；macOS 本轮 **不实现** 图片/视频采集（见 §8）。

## 1. 现状（证据）

- 数据模型：`pkg/core/clipboard/models.go` `ClipboardItem{ID, Content, ContentHash, SourceBundleID, SourceAppName, CreatedAt, UpdatedAt, IsPinned}`、`CaptureCandidate{Content, SourceBundleID, SourceAppName, PasteboardTypes, Timestamp}`，只有文本。
- SQLite：`internal/storage/sqlite_store.go:81` `initSchema`（表 @92，FTS5 @103，触发器 @109-120），`Record` @132，`PruneExpired` @373。
- 隐私：`pkg/core/clipboard/filter.go:63` `ShouldIgnore`，空白内容直接忽略（@66-73）——图片的 `Content` 为空，会被误杀，需要按类型分支。`ExcludedBundleIDs` 只有 macOS bundle ID（`main_config.go:897-900`）。
- Windows 采集：`internal/bridge/bridge_windows.go:1410` `PollClipboardCandidate` 只读 `CF_UNICODETEXT`（`readWinClipboardUnicodeText` @1440），文本为空就返回 nil（@1423）；序列号 `winClipboardSequenceNumber` @1435，自写抑制 `IgnoreSelfPasteboardChange` @1406。
- 轮询：`main.go:752` `runClipboardPoller`（`clipPollInterval=400ms`，`main.go:38`）。
- UI：行图标 `uiapp/capsule_view.go:697` `renderOmniboxRowLeadingIcon`；预览 `uiapp/capsule_preview.go:376` 剪贴板分支；位图缓存模式 `uiapp/controller.go:382` `AppIconBitmap`（`ui.DecodeBitmap` + 256 项上限）。
- 粘贴：`pkg/core/engine/capsule.go:1058` 产生 `EffectPasteClipboardItem`，`uiapp/controller.go:559` 走 `injectTextAsync`（只适合文本）。
- **MyGo 已提供跨格式剪贴板**（无需新依赖）：`mygo.Clipboard.Read/Write(transfer.Data)`、`ReadFiles/WriteFiles`；Windows 后端（`mygo@v0.9.0/internal/windows/clipboard.go:100-140`）把 `CF_DIB/CF_DIBV5/"PNG"` 统一成 `image/png`（优先注册的 "PNG" 以保留 alpha），`CF_HDROP` 映射为 `FileList`；写 `image/png` 时同时提供 DIB（`Flush` 注释 "PNG -> DIB"）。读上限 64 MiB。

## 2. 数据模型

`ClipboardItem` / `CaptureCandidate` 新增字段（零值 = 旧文本行为）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `Kind` | `"text"\|"image"\|"file"\|"video"` | 空串视为 text |
| `MediaPath` | string | image：blob 绝对路径；file/video：原始文件路径（不复制） |
| `MediaMime` | string | `image/png`、`video/mp4` … |
| `Width`/`Height` | int | 像素 |
| `BlobSHA` | string | blob 的 sha256（去重键）；file/video 用路径列表 sha |
| `ThumbPath` | string | 缩略图 PNG 路径 |
| `DurationMs` | int64 | 视频时长（P3） |
| `ByteSize` | int64 | blob/文件字节数（配额） |

`CaptureCandidate` 新增 `Kind`、`ImagePNG []byte`、`FilePaths []string`。

去重键：text 仍是 `sha256(content)`；image 为 `sha256(png)`；file/video 为 `sha256("files\x00"+join(paths))`。统一写进 `content_hash`，现有唯一约束/更新时间逻辑不变。

## 3. SQLite 平滑升级

- `initSchema` 后执行 `PRAGMA table_info(clipboard_items)`，缺哪列就 `ALTER TABLE ADD COLUMN ... DEFAULT ...`（kind 默认 `'text'`）。幂等，旧库自动升级，失败回滚为只读文本模式不影响启动。
- FTS 只索引 `content`：image 的 content 为空；file/video 的 content 为文件名（换行分隔），因此按文件名可搜，二进制永不入 FTS。触发器不变。
- 回退：新列有默认值，老版本二进制打开新库只会忽略新列（读到的是空 content 的行，显示为空白条目；可接受，且不会损坏数据）。

## 4. 存储布局 / 配额 / 清理

```text
<PathUserData>/media/<sha256>.png
<PathUserData>/media/thumbs/<sha256>.png
```

- 写入：先写临时文件再 `rename`，同 sha 已存在则跳过（天然去重）。
- 配额：`media_quota_mb`（默认 512）。超额时按 `updated_at` 从旧到新删除 **未置顶** 的媒体行（连带 blob）。
- `PruneExpired`/`Delete`/`Clear` 之后执行 `GCMedia`：列出 `media/` 下文件，删除不被任何行引用的 blob/缩略图（引用集合由 `SELECT media_path, thumb_path` 得到）。file/video 的原文件 **绝不删除**，只删缩略图。
- 内存存储（`MemoryStore`）只保存元数据，blob 仍由 `MediaStore` 管理，保证无 sqlite 时行为一致。

## 5. Windows 采集

优先级（单次轮询）：

1. 隐私标记（`ExcludeClipboardContentFromMonitorProcessing` / `CanIncludeInClipboardHistory=0`）→ 整体忽略（复用 `filter.go` 标记表）。
2. `CF_UNICODETEXT` 非空 → 文本（**保持现状**，Excel 同时带位图时仍记为文本，避免回归）。
3. `CF_HDROP` → file/video（P3）。
4. `"PNG"` > `CF_DIBV5` > `CF_DIB` → image（P2）。

实现：`bridge_windows.go` 只负责「序列号去重 + 枚举格式 + 前台进程」，用纯函数 `classifyClipboardFormats(formats)`（无 build tag，可单测）决定 kind；文本照旧读。图片/文件的字节由 `main.go` 轮询器通过 `mygo.Clipboard.Read(transfer.PNG)` / `ReadFiles()` 拉取（复用 MyGo 的 DIB→PNG 转换，不新写 DIB 解析）。读完再比对序列号，若期间变化则丢弃本次结果（防止混读）。

隐私过滤扩展：

- `ShouldIgnore` 对非文本 kind 跳过「空白内容」检查，其余检查（标记、排除来源）全部生效。
- 来源排除比较改为大小写不敏感，并内置 Windows 密码管理器进程名：`1password.exe`、`keepass.exe`、`keepassxc.exe`、`bitwarden.exe`、`enpass.exe`、`dashlane.exe`、`lastpass.exe`、`roboform.exe`、`credentialuibroker.exe`。这些进程复制的 **图片**（二维码、恢复码截图）同样被过滤。
- 单图上限：PNG 字节 > 40 MiB 或宽/高 > 8000px（先用 `image.DecodeConfig` 读头，不解码像素）→ 不记录。

## 6. 缩略图

- 图片：标准库 `image/png`（+ `image/jpeg`、`image/gif` 注册）解码，自写盒式缩放到长边 ≤256，写 PNG。解码前先过 §5 尺寸上限，防解压炸弹。
- 视频（P3）：`IShellItemImageFactory::GetImage`（`shell32` `SHCreateItemFromParsingName`，`SIIGBF_THUMBNAILONLY`）拿首帧 HBITMAP → `GetDIBits` 转 RGBA；时长/尺寸用 `IShellItem2::GetUInt64(PKEY_Media_Duration)` / `GetUInt32(PKEY_Video_FrameWidth/Height)`。全部 `NewLazySystemDLL` + vtable `syscall`，无 cgo。失败（无解码器、损坏文件）→ 回退为文件类型图标，不报错。备选 WIC 仅在 Shell 不可用时考虑（工作量大、对视频无帮助，本轮不做）。

## 7. UI 与粘贴回写

- 列表：`OmniboxClipboardEntry`/`OmniboxItem` 增加 `ClipboardKind`、`ThumbPath`、尺寸、时长。`renderOmniboxRowLeadingIcon` 对 image/video 显示 22×22 缩略图（控制器 `ThumbBitmap(path)`，同 `AppIconBitmap` 的缓存/上限），标题显示 `图片 1920×1080` / 文件名。
- 预览：`capsule_preview.go` 剪贴板分支对 image 显示大图（按预览宽度等比，懒加载：只在选中时解码缩略图；原图仅在「预览大图」时解码并受上限保护），video 显示首帧 + 时长 + 尺寸。
- 缓存失效：位图缓存 key = 路径；行被删除/GC 后路径不存在 → 解码失败返回 nil → 显示类型图标。
- 粘贴：新增 `EffectPasteClipboardMedia`。控制器用 `mygo.Clipboard.Write(transfer.Bytes(PNG,…))`（MyGo 自动同时提供 `"PNG"` + `CF_DIB`），或 `WriteFiles(paths…)`（`CF_HDROP`）；`Flush()` 让延迟渲染落地后读取序列号并 `IgnoreSelfPasteboardChange`，再发送 Ctrl+V（Windows：可选接口 `PasteShortcutSender`；macOS 不实现 → 仅复制并提示）。粘贴格式选择是纯函数 `PasteFormatsFor(kind)`，单测覆盖。

## 8. macOS

`bridge_darwin.go:2069` 文本为空即返回 nil；本轮不改 darwin 行为分支，因此 macOS **不采集** 图片/视频。数据模型、存储、UI、粘贴回写是跨平台代码，日后只需在 darwin 轮询里补 `NSPasteboardTypePNG/fileURL` 判定即可。

## 9. 风险与回退

| 风险 | 缓解 / 回退 |
| --- | --- |
| 大图拖慢轮询（400ms） | 读字节在轮询 goroutine，不在 UI 线程；40 MiB/8000px 上限；缩略图异步生成失败不影响记录。 |
| 磁盘膨胀 | 配额 + GC；`media_capture=false` 一键关闭（只记文本，与旧版一致）。 |
| 隐私 | §5 标记 + 进程排除；配额外不持久化任何临时文件。 |
| 迁移失败 | ALTER 失败不致命，媒体功能自动禁用，文本照常。 |
| 自写回流 | 序列号抑制 + sha 去重双保险。 |
