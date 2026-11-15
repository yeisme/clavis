# Clavis

Clavis is a keyboard-first, non-activating floating **Capsule** launcher and productivity assistant built with **Go + MyGo v0.9.0** (`mygo/ui`, `plugins/glass`, `plugins/sfsymbols`, `plugins/sqlite`, and `plugins/permissionflow`). It combines a mnemonic Leader key tree, fuzzy Omnibox search, multi-protocol AI profiles with multi-turn follow-up, smart context-aware IME switching with a cursor-side floating pill, tap-hold keymap remapping with a Vim-style navigation layer, inline snippet expansion, domain-glossary normalization, local SQLite FTS5 clipboard history, and window management.

## Feature Matrix

| Area | Capabilities |
| --- | --- |
| **Leader Key Tree** | Modifier double-tap activation (`right_option` / `right_command`), recursive mnemonic key groups, `/` instant jump to clean Omnibox search, smart temporary ASCII IME lock (`lock_ascii_on_leader`) with immediate IME restoration on exit or Omnibox downgrade |
| **Omnibox Launcher, Pinyin & Prefix Routing** | Tabbed search across **Actions**, **Clipboard**, **Snippets**, **AI**, and **Glossary** (`⌘1`–`⌘5` / `Ctrl+1`–`Ctrl+5`, `Tab` / `Shift+Tab`), zero-dependency **Mandarin Pinyin & initialism search** (`weixin` / `wx` / polyphones / `lv`→`绿`) with exact Hanzi highlighting, full-width operator/prefix normalization (`＝（２＋３）×４`, `：huojian`), prefix quick-routing (`>`, `;`, `@` / `cb `, `?`, `:`, `=`), direct row execution (`⌥1`–`⌥9` / `Alt+1`–`Alt+9`), primary (`↵`) & secondary (`⌘↵` / `Ctrl+↵`) dual actions, `⌘K` / `Ctrl+K` contextual Action Menu, toggleable **Split Preview Pane** (`⌘I` / `Ctrl+I`), and one-click **"Bind / Unbind Current App IME Rule"** |
| **Multi-Protocol AI & Follow-Up** | Built-in `[[ai.profiles]]` supporting `openai_compatible` (**DeepSeek** `deepseek-chat` default, **Ollama** `qwen2.5:7b` local keyless), `anthropic` (**Claude** `claude-3-7-sonnet-latest` Messages SSE), and `gemini` (**Gemini** `gemini-2.5-flash` `streamGenerateContent` SSE); header profile switcher (`⌘⇧M` / `Ctrl+Shift+M`) + IME-composition-safe multi-turn `Tab` follow-up prompt inside `PhaseAIStreaming`; automatic `<glossary>` prompt injection; per-profile OS credential storage & environment variable fallback |
| **Smart IME & Floating Pill** | Context-aware input source switching (`ime-rules.toml`) matching by `url_pattern` (`github.com`, `*.docs.rs`), regex `title_pattern`, `bundle_id`, or `process_name` with anti-race `switch_delay_ms` re-verification and optional mode-only (`preferred_mode = "ascii" \| "native"`) switching without hardcoding OS source IDs; non-activating, session-preserving cursor-side `"中 / EN / LAYER"` floating micro-pill (`IMEPillView`) |
| **Keymap Tap-Hold & Nav Layer** | Configurable `[[keymap.remaps]]` tap-vs-hold engine (e.g., tap `caps_lock` → `escape`, hold `caps_lock` → `nav` or `hyper`) with Vim-style `h/j/k/l` → Arrow keys, `u/d` → PageUp/PageDown, `0/4` → Home/End, live hold-layer floating pill feedback, and runtime hot-reload |
| **Visual Configuration Center** | Native `ui.Sidebar` Settings Center covering **General & Triggers** (live shortcut recorder), **IME & Keymap** (native input source & installed-app pickers + custom remaps CRUD), **Leader Tree** (recursive visual editor), **Snippets** (master-detail CRUD + placeholder chips + live preview), **Glossary & Clipboard**, **AI & Keychain** (multi-profile CRUD + connection test), **Permissions & Diagnostics**, and **Configs** — backed by atomic TOML persistence (`ConfigStore.Save*`) with SHA-256 self-write loop suppression |
| **First-Run Guide / 首次使用引导** | One welcome page on a machine's first launch, and from the tray's *Welcome Guide…*: live Accessibility / Input Monitoring status with a System Settings drag-in guide (`plugins/permissionflow`), a press-your-Leader-key check, and the configured shortcuts. The keyboard monitor starts as soon as the permissions allow it, or the guide offers a restart when macOS wants one. Completion is recorded per machine (`onboarding.json` in the user data directory), not in the synced TOML; installs with existing data skip it / 首次启动的欢迎页：权限实时状态与系统设置拖拽引导、Leader 键试按检测、快捷键速查；授权后自动启动键盘监听，需要时提示重启 |
| **Glossary Normalization** | Local longest-first alias normalization (`normalize_glossary`) on selected text (in-place replacement) or clipboard preview, plus searchable glossary term reference |
| **Clipboard History** | SQLite + FTS5 persistence (`plugins/sqlite`) with privacy filters (concealed types & excluded bundle IDs), pin (`⌘P` / `Ctrl+P`) and delete (`⌘⌫` / `Ctrl+⌫`) in both Clipboard tab and `@` prefix view |
| **Inline Snippets** | Boundary-aware trigger expansion with dynamic tokens (`{{cursor}}`, `{{clipboard}}`, `{{date}}`, `{{time}}`, `{{app}}`), full-width leading punctuation tolerance (`；sig` → `;sig`), active IME preedit suppression, mouse/focus/navigation context resets, and OS Secure Input protection |
| **Window Management** | `left_half`, `right_half`, `top_half`, `bottom_half`, `maximize`, `center`, `next_display`, `previous_display`, `restore` |
| **中文本地化 / Chinese i18n** | Pure-Go `en` / `zh-CN` string tables, `language = "auto" | "zh-CN" | "en"`, localized HUD/tray/settings/diagnostics, CJK fallback fonts, and IME-safe composition handling / 纯 Go 字符串表、中文 HUD/托盘/设置/诊断、CJK 字体回退，保留输入法组合态安全逻辑 |
| **动画 / Motion** | 60fps non-blocking capsule/HUD/list/AI/snippet motion with `full` / `reduced` / `off` modes and system Reduce Motion support / 支持完整、减少、关闭三档并尊重系统减少动态效果 |
| **Vector & Native Icons** | Unified currentColor SVG icons with 16/20/24 optical sizes, HUD/empty-state/tray glyphs, macOS `plugins/sfsymbols`, multi-size PNG and Windows ICO / 统一矢量图标、光学尺寸、多尺寸应用图标及 Windows ICO |

## Architecture & Project Layout

Clavis uses a unified Go codebase with a pure-stdlib core, zero-CGo native OS bridges, and native GPU-accelerated MyGo UI:

```text
clavis/
├── main.go                 # Desktop entrypoint, --smoke-check, MyGo Capsule/IMEPill/Settings/Tray lifecycle
├── main_test.go            # Smoke-check, Capsule & IME pill screen coordinate clamping, and tray icon tests
├── onboarding.go           # First-run guide window, per-machine completion marker, permission watcher
├── mygo.json               # MyGo v0.9.0 bundle manifest, NSIS installer config & macOS privacy usage descriptions
├── resources/icon.png      # High-craft squircle application icon (512x512 RGBA PNG)
├── resources/icon.ico      # Multi-entry Windows icon (16–256px PNG entries)
├── uiapp/                  # Native MyGo UI & controller (AppController, CapsuleView, IMEPillView, SettingsView, SVG/SF Symbols)
├── internal/
│   ├── apps/               # Installed application discovery & Retina icon cache (MyGoAppProvider + MockAppProvider)
│   ├── bridge/             # Zero-CGo OS host bridges (purego on macOS, x/sys/windows on Windows, Linux fallback)
│   ├── secrets/            # OS Keychain / Credential Manager / Secret Service via mygo.Secrets (+ per-profile env fallback)
│   ├── storage/            # Local clipboard persistence via mygo/plugins/sqlite + FTS5
│   └── watcher/            # Debounced TOML config hot-reloader (mygo.WatchFiles + polling fallback)
└── pkg/
    ├── core/               # Pure-stdlib core: toml, config, engine, clipboard, ai, actions, host
    └── mobile/             # gomobile-compatible Session & StreamCallback bindings for iOS / Android
```

### Pure-Go Zero-CGo System Bridges

All desktop targets compile with zero CGo dependencies:

- **macOS (`internal/bridge/bridge_darwin.go`)**: Uses `github.com/ebitengine/purego` (`dlopen`/`dlsym`/`objc_msgSend`) for `CoreFoundation`, `CoreGraphics` (`CGEventTap` with tap-hold keymap interception and automatic timeout recovery), `ApplicationServices` (`AXUIElement` caret bounds, selected text, browser `AXURL`/`AXDocument` active URL inspection, and window tiling), `Carbon` (`TISInputSource` & `IsSecureEventInputEnabled`), `AppKit` (`NSPasteboard` snapshot/restore), and `mygo/plugins/permissionflow`.
- **Windows (`internal/bridge/bridge_windows.go`)**: Uses `golang.org/x/sys/windows` (`user32`, `imm32`, `kernel32`, `dwmapi`, `shell32`) for `WH_KEYBOARD_LL` hooks with tap-hold keymap remapping, `SendInput` (`SyntheticEventTag`), `GetGUIThreadInfo` caret anchoring, DWM-aware window tiling, `HKL` keyboard layout switching + `ImmGetDefaultIMEWnd` (`IMC_GETCONVERSIONMODE` / `IMC_SETCONVERSIONMODE` / `IMC_SETOPENSTATUS`) Chinese/English toggling, and `ExcludeClipboardContentFromMonitorProcessing` filtering.
- **Linux (`internal/bridge/bridge_linux.go`)**: Uses `mygo.Clipboard`, `mygo.Shell`, `mygo.Secrets`, and `mygo.GlobalShortcut` for Wayland/X11 desktop integration without CGo.

## Cross-Platform Support Matrix

| Target | UI / Runtime | Core & Storage | System Bridge |
| --- | --- | --- | --- |
| **macOS (`darwin/arm64`, `amd64`)** | MyGo v0.9.0 `Panel` + `VibrancyGlass` (`plugins/glass`) + `plugins/sfsymbols` + Menu Bar Tray | `pkg/core` + `plugins/sqlite` + Keychain | Full (`purego`: CGEventTap, Keymap Engine, AXUIElement + Browser URL, Carbon TIS, NSPasteboard) |
| **Windows (`windows/amd64`, `arm64`)** | MyGo v0.9.0 `Panel` + `VibrancyGlass` + Embedded SVG Icons + System Tray | `pkg/core` + `plugins/sqlite` + Credential Manager | Full (`x/sys/windows`: WH_KEYBOARD_LL, Keymap Engine, SendInput, DWM, HKL + IMM32 IME Mode) |
| **Linux (`linux/amd64`)** | MyGo v0.9.0 Native UI + Embedded SVG Icons + System Tray | `pkg/core` + `plugins/sqlite` + Secret Service | Clipboard, Shell, Keychain & `mygo.GlobalShortcut` fallback |
| **Android / iOS (`pkg/mobile`)** | Native Kotlin / Swift UI | Shared `pkg/core` via `gomobile` (`Session`) | Host app implements `StreamCallback` & `CancelToken` |

## Development, Testing & Packaging

### Test & Vet

```bash
# Run all unit and UI tests (including headless ui.NewTester frames)
go test -count=1 ./...

# Race detector verification across core, internal bridges/storage, and UI controller
go test -count=1 -race ./pkg/... ./internal/... ./uiapp/...

# Static verification across macOS, Windows, and Linux targets
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=linux GOARCH=amd64 go vet ./...
```

### Run & Smoke Check

```bash
# Run directly in development
go run .

# Validate config/bridge initialization without touching ~/.config/clavis
go run . --smoke-check
```

### Cross-Platform Packaging (`mygo build`)

```bash
# Build native bundles and Windows NSIS installers for macOS, Windows (amd64 & arm64), and Linux
go tool mygo build -platform darwin/arm64,windows/amd64,windows/arm64,linux/amd64 -skip-dmg

# Verify macOS bundle metadata, code signature, and smoke check
plutil -lint build/darwin-arm64/Clavis.app/Contents/Info.plist
codesign --verify --deep --strict --verbose=2 build/darwin-arm64/Clavis.app
./build/darwin-arm64/Clavis.app/Contents/MacOS/Clavis --smoke-check
```

## Configuration (`~/.config/clavis/*.toml`)

On first launch, Clavis automatically creates `~/.config/clavis/` with default TOML files and hot-reloads changes via `watcher.ConfigWatcher`:

| File | Purpose |
| --- | --- |
| `config.toml` | `[trigger]`, `[appearance]` (`language`, `animations`, `show_preview_pane`), `[ai]` + `[[ai.profiles]]` (`deepseek` default, `ollama`, `claude`, `gemini`), `[clipboard]`, `[ime]` (`lock_ascii_on_leader`, `show_floating_pill`, `switch_delay_ms`), and `[keymap]` + `[[keymap.remaps]]` settings. Raw API keys (`api_key`) are rejected—store keys in the OS credential store via **Settings → AI & Keychain** or the profile's `api_key_env_var` (`DEEPSEEK_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`). |
| `leader.toml` | Recursive `[[nodes]]` key tree supporting `group`, `ai_rewrite`, `window`, `cli`, `open_app`, `switch_tab`, `snippet`, `toggle_ime`, and `normalize_glossary`. Editable visually under **Settings → Leader Tree**. |
| `snippets.toml` | `[[snippets]]` inline expansion triggers, content templates, `word_boundary`, and optional `app_bundle_ids` / `process_names` scoping. Editable visually under **Settings → Snippets**. |
| `glossary.toml` | `[[terms]]` canonical terms, `aliases`, `hint`, and `category` used for AI prompt injection and local `normalize_glossary` replacement. Editable visually under **Settings → Glossary & Clipboard**. |
| `ime-rules.toml` | `[[rules]]` per-app and per-context rules with `bundle_id`, `process_name`, `url_pattern`, `title_pattern`, optional `input_source_id`, `preferred_mode` (`ascii`, `native`, `keep`), and `restore_on_leave`. When `input_source_id` is omitted, Clavis switches directly by `preferred_mode`. Editable visually under **Settings → IME & Keymap** or bound instantly from Omnibox (`>Bind Current App`). |

## Language & Motion / 语言与动画

Clavis keeps configuration keys and enum values stable while translating only display text. Set the language in `config.toml`:

```toml
[appearance]
language = "auto"       # auto follows macOS/Windows/Linux UI language
# language = "zh-CN"    # force Simplified Chinese
# language = "en"       # force English
animations = "auto"     # auto | full | reduced | off
```

`auto` language uses the system preference; `auto` animation mode respects the platform Reduce Motion preference. `full` enables capsule scale/fade, staggered results, HUD slide/fade, AI cursor pulse, error shake and snippet flash. `reduced` keeps subtle opacity/color changes and removes spatial motion; `off` jumps to settled frames. The setting is hot-reloadable and does not rewrite older config files until the user explicitly saves.

`auto` 会跟随 macOS/Windows/Linux 的系统语言；动画 `auto` 会尊重系统“减少动态效果”。`full` 启用胶囊缩放淡入、列表交错、HUD 滑入淡出、AI 光标脉冲、错误抖动与片段高亮；`reduced` 保留轻微透明度/颜色变化并移除位移动画；`off` 直接显示最终状态。

## Omnibox Prefix Quick-Routing

Type a leading prefix in the **Actions** Omnibox (both half-width and Chinese IME full-width prefixes are accepted):

| Prefix | Full-Width Alias | Scope | Examples |
| --- | --- | --- | --- |
| `>` | `＞` | Commands, Leader actions, App IME bind/unbind, Installed Apps & Settings | `>left`, `>英文输入法`, `>vscode` |
| `;` | `；` | Snippets (matches both trigger & description/Pinyin) | `;`, `;sig`, `；sig` |
| `@` or `cb ` | `＠` | Inline Clipboard History search (supports Pin & Delete) | `@`, `@token`, `cb url` |
| `?` | `？` | Freeform AI Ask / Rewrite & AI preset modes | `?summarize in 3 bullets`, `？润色语气` |
| `:` | `：` | Emoji search by English, Chinese, or Pinyin | `:rocket`, `：火箭`, `：huojian`, `:hj` |
| `=` | `＝` | Calculator & Developer Utilities (UUID, Unix/ISO time, Base64, Hex) | `=`, `=(2+3)*4^2`, `＝（２＋３）×４` |

## Default Shortcuts

| Context | Shortcut | Action |
| --- | --- | --- |
| Global | Double-tap `Right Option` (or `Right Command`) | Open Leader Capsule at caret/cursor (with temporary ASCII IME lock) |
| Global | `Option + Space` (`Alt + Space`) | Toggle Actions Omnibox at caret/cursor |
| Global (Keymap) | Tap `CapsLock` / Hold `CapsLock + h/j/k/l` | Tap sends `Escape`; hold activates Nav Layer (`←/↓/↑/→`, `u/d` PageUp/Down, `0/4` Home/End) |
| Leader | Configured mnemonic keys | Drill into group or execute action (unmatched key downgrades to Actions Omnibox & restores IME) |
| Leader | `/` (or `／`) | Jump directly to clean Actions Omnibox search bar (restoring previous IME) |
| Leader | `⌫` / `Esc` | Step up one group or close Capsule (restoring previous IME) |
| Omnibox | `⌘1` – `⌘5` (`Ctrl+1` – `Ctrl+5`) | Switch to Actions, Clipboard, Snippets, AI, or Glossary tab |
| Omnibox | `⌥1` – `⌥9` (`Alt+1` – `Alt+9`) | Instantly execute visible result row 1–9 (add `⌘`/`Ctrl` for Secondary Action) |
| Omnibox | `Tab` / `Shift + Tab` | Cycle tabs forward / backward |
| Omnibox | `↑` / `↓` or `⌃P` / `⌃N` | Move selection |
| Omnibox | `⏎` | Primary action (run / paste / insert / copy utility) |
| Omnibox | `⌘⏎` / `Ctrl+⏎` | Secondary action (copy clipboard/snippet/emoji, insert utility/glossary at cursor, ask AI without selection) |
| Omnibox | `⌘K` / `Ctrl+K` | Toggle contextual Action Menu for selected item |
| Omnibox | `⌘I` / `Ctrl+I` | Toggle multi-line Split Preview Pane |
| Omnibox (Clipboard / `@`) | `⌘P` / `Ctrl+P` | Pin / unpin selected clipboard item |
| Omnibox (Clipboard / `@`) | `⌘⌫` / `Ctrl+⌫` | Delete selected clipboard item |
| AI Streaming | `⌘⇧M` / `Ctrl+Shift+M` | Cycle active AI provider/model profile |
| AI Streaming | `Tab` | Focus IME-safe multi-turn follow-up input (submit with `⏎`) |
| AI Streaming | `⏎` | Replace selection / inject AI output (when follow-up input is empty) |
| AI Streaming | `⌘C` / `Ctrl+C` | Copy AI output to clipboard |
| AI Streaming | `Esc` | Cancel active stream or close Capsule |
