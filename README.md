# Clavis

Clavis is a keyboard-first, non-activating floating **Capsule** launcher for macOS, built with **Go + MyGo v0.9.0** (`mygo/ui`, `plugins/glass`, `plugins/sfsymbols`, `plugins/sqlite`, and `plugins/permissionflow`). It unifies a mnemonic Leader key tree, fuzzy 6-tab Omnibox search + 2 operational Hub overlays, a multi-protocol AI Workbench with Vision OCR, bilingual translation & inline continuation, region capture & Retina vector annotation, an AES-256-GCM Security Vault, a Herdr Agent control plane, an opt-in local second-IME Chinese input engine, context-aware IME switching, tap-hold keymap remapping, inline snippet expansion, domain-glossary normalization, SQLite FTS5 media clipboard history, and window management. The release scope and unverified capabilities are recorded in [`docs/design/scope-1.0.md`](docs/design/scope-1.0.md).

## 输入法定位（第二输入法）

Clavis 输入法工作在「系统输入法为英文」的前提下。中文由 Clavis 接管；切到系统中文输入法时 Clavis 自动让位。它不提供内联组字、不支持提权窗口，因此不适合作为唯一中文输入法。

## Feature Matrix

| Area | Capabilities |
| --- | --- |
| **Leader Key Tree** | Modifier double-tap activation (`right_option` / `right_command`), recursive mnemonic key groups, `/` instant jump to clean Omnibox search, smart temporary ASCII IME lock (`lock_ascii_on_leader`) with immediate IME restoration on exit or Omnibox downgrade |
| **6-Tab Omnibox & 2 Hub Overlays** | Tabbed search across **Actions**, **Clipboard**, **Snippets**, **AI**, **Glossary**, and **Agent** (`⌘1`–`⌘6` / `Ctrl+1`–`Ctrl+6`, `Tab` / `Shift+Tab`), plus **`⌘7` Stats** (AI usage, clipboard metrics, lexicon hits/top words, agent health) and **`⌘8` Manage** (local sync status, Media/Clipboard, Terminal/Agent, Lexicon, AI/Vision, Vault & QuickJump status). The former Assets surface is not part of the 1.0 product scope. Zero-dependency **Mandarin Pinyin & initialism search** (`weixin` / `wx` / polyphones / `lv`→`绿`), full-width prefix normalization, quick-routing (`>`, `;`, `@` / `cb `, `?`, `:`, `=`, `/`, `#`), direct row execution (`⌥1`–`⌥9`), primary (`↵`) & secondary (`⌘↵`) dual actions, `⌘K` contextual Action Menu, and toggleable **Split Preview Pane** (`⌘I`) |
| **AI Workbench, OCR, Translate & Continuation** | 5-state AI Interaction Center (`pending` / `running` / `awaiting_human` / `done` / `failed`) with truthful 3-step log, blocking diff-approval gate, and multi-line `@context` / `/command` Input Composer (IME-composition-safe, 30-entry history). Multi-protocol `[[ai.profiles]]` (`openai_compatible` DeepSeek/Ollama, `anthropic` Claude, `gemini` Gemini) with `⌘⇧M` profile switching, multimodal Vision OCR (`PrepareOCRImage` 2048px scaling, Plain Text / Markdown Table), bilingual Translate (`tr` / `fy` / `翻译` with auto script detection + `ZH → EN` / `EN → ZH` bar), explicit AI Continuation (rune-safe 200-char tail, 5s Backspace undo), floating Selection Bar, Secure Input blocking, auto-attached secret scrubbing, and asynchronous SQLite token usage telemetry (`ai_usage`) |
| **Region Capture & Vector Annotate** | Interactive region & full-screen capture on macOS (`screencapture`) and Linux (`grim+slurp` / `scrot` / `import`) paired with a standalone Retina vector annotator (**Rect**, **Arrow**, **Mosaic**, **Text** with system CJK + `goregular` fallback, 5 swatches, S/M/L stroke, Undo/Redo). Windows currently has full-screen capture code but no real-machine parity sign-off. Flattens lossless PNGs to clipboard + content-addressed `MediaStore` with self-write dedup, saves to `~/Pictures/Clavis`, or hands off directly to **AI OCR** / **Ask AI** |
| **Security Vault (`vault_secrets.enc`)** | AES-256-GCM encrypted secret store isolated from `ai-keys.toml` (32-byte master key in OS Keychain with `0600` `vault.key` fallback), metadata-only `SecretRef` / `CopyReceipt` contracts, agent-forbidden plaintext read boundary, user-only TTL clipboard copy with pasteboard ownership verification on expiry, and revoke-time ciphertext deletion |
| **Herdr Agent Control Plane & QuickJump** | Local multi-source discovery across **DSH** JSON/JSONL, **Codex** `state_5.sqlite` (strictly `mode=ro`, preserving WAL visibility), **Claude** JSONL, and live **Herdr** workspaces/panes; ANSI-stripped, secret-redacted terminal output preview; per-session isolated gated controls (workspace/pane focus, rename, 6-CLI allowlist start `claude`/`codex`/`pi`/`agy`/`gemini`/`opencode`, two-step confirmed `SendPrompt` with unified secret & option-injection blocking, non-retryable `StateUnknown` contract, idle completion watch); `#` QuickJump bookmarks (`quickjump.toml`) and `pkg/core/terminal` Resolver fallback |
| **Clipboard History & Media Store** | SQLite + FTS5 persistence (`plugins/sqlite`) with COW capsule state & 120Hz-budget row/preview caching, 6-category media search, content-addressed `MediaStore` blobs + 256px thumbnails with 512 MiB quota GC, privacy filters (concealed types, password-manager processes, excluded bundle IDs), pin (`⌘P`) and delete (`⌘⌫`) |
| **Own Input Engine, Smart IME & Floating Pill** | Opt-in Pinyin/Shuangpin second-IME engine with embedded + hot-reloaded user `.clvdict` dictionaries, bigram prefix matching, user lexicon learning & blocklist; context-aware `ime-rules.toml` switching (`url_pattern`, `title_pattern`, `bundle_id`, `process_name`) with `switch_delay_ms` anti-race re-verification and cursor-side `"中 / EN / LAYER"` floating micro-pill (`IMEPillView`). It is not a replacement for the system input method and is not enabled by default. |
| **Keymap Tap-Hold & Nav Layer** | Configurable `[[keymap.remaps]]` tap-vs-hold engine (e.g., tap `caps_lock` → `escape`, hold `caps_lock` → `nav` or `hyper`) with Vim-style `h/j/k/l` → Arrow keys, `u/d` → PageUp/PageDown, `0/4` → Home/End, live hold-layer floating pill feedback, and runtime hot-reload |
| **Visual Configuration Center & Onboarding** | Native `ui.Sidebar` Settings Center (General & Triggers, IME & Keymap, Input Engine, Leader Tree, Snippets, Glossary & Clipboard, AI & Keychain, Security Vault, Sync, Permissions & Diagnostics, Configs) backed by atomic TOML persistence with SHA-256 self-write loop suppression; first-run onboarding guide (`onboarding.json`) with live permission drag-in (`plugins/permissionflow`) |
| **Inline Snippets, Glossary & Window Mgmt** | Boundary-aware trigger expansion (`{{cursor}}`, `{{clipboard}}`, `{{date}}`, `{{time}}`, `{{app}}`) with 5s Backspace undo & OS Secure Input protection; longest-first local glossary normalization (`normalize_glossary`); 9 window tiling commands (`left_half`…`restore`) |
| **i18n, Motion & Vector Icons** | Pure-Go `en` / `zh-CN` string tables (`language = "auto" \| "zh-CN" \| "en"`), `classic` / `liquid_glass` / `spatial_3d` presets with `full` / `reduced` / `off` motion + Reduce Motion support, currentColor SVG icons + macOS `plugins/sfsymbols` |

## Architecture & Project Layout

Clavis uses a unified Go codebase with a pure-stdlib core, zero-CGo native OS bridges, and native GPU-accelerated MyGo UI (see [`docs/ROADMAP.md`](docs/ROADMAP.md) for current phase status and [`docs/design/`](docs/design/) for detailed specs):

```text
clavis/
├── main.go                 # Desktop entrypoint, --smoke-check, --self-check, Capsule/Annotate/IMEPill/Settings/Tray lifecycle
├── selfcheck.go            # Strictly read-only local diagnostic (--self-check)
├── onboarding.go           # First-run guide window, per-machine completion marker, permission watcher
├── mygo.json               # MyGo v0.9.0 bundle manifest & macOS usage descriptions (incl. NSScreenCaptureUsageDescription)
├── resources/              # Application icons (icon.png 512x512, multi-entry icon.ico)
├── cmd/
│   ├── clavis/             # CLI companion entrypoint
│   └── clavis-dictgen/     # Rime/custom lexicon compiler for *.clvdict dictionaries
├── uiapp/                  # Native MyGo UI & AppController (Capsule, Hubs ⌘7–⌘8, AI Center, Composer, Annotate, OCR/Translate, Agent Controls, Settings)
├── internal/
│   ├── agentsession/       # Platform Codex state_5.sqlite read-only SQLite adapter (mode=ro, WAL-safe)
│   ├── apps/               # Installed application discovery & Retina icon cache
│   ├── bridge/             # Zero-CGo OS host bridges (purego on macOS, x/sys/windows on Windows, Linux fallback)
│   ├── capture/            # Platform screenshot capture (macOS screencapture, Linux grim/slurp/scrot, Windows GDI)
│   ├── daemon/             # Resident daemon lifecycle and wiring
│   ├── secrets/            # AES-256-GCM EncryptedVaultStore (vault_secrets.enc), FileStore (ai-keys.toml), LayeredStore & OS Keychain
│   ├── storage/            # Local SQLite + FTS5 clipboard, learning, AI usage telemetry & sync stores
│   ├── sync/               # Sync watermark & recovery persistence
│   ├── watcher/            # Debounced TOML config & .clvdict dictionary hot-reloaders
│   └── mcp/, plugins/, skills/  # MCP client (Phase 4) and frozen extension packages (see docs/ROADMAP.md D2)
└── pkg/
    ├── core/               # Pure-stdlib core: actions, agentsession, ai, annotate, automation, clipboard, config, engine, host, i18n, ime, inputengine, inputmigration, jsonschema, prompt, runtime, sync, template, terminal, toml
    └── mobile/             # gomobile-compatible Session & StreamCallback bindings for iOS / Android
```

### Pure-Go Zero-CGo System Bridges

All desktop targets compile with zero CGo dependencies:

- **macOS (`internal/bridge/bridge_darwin.go`)**: Uses `github.com/ebitengine/purego` (`dlopen`/`dlsym`/`objc_msgSend`) for `CoreFoundation`, `CoreGraphics` (`CGEventTap` with tap-hold keymap interception, second-IME inline hook, and automatic timeout recovery), `ApplicationServices` (`AXUIElement` caret bounds, selected text, browser `AXURL`/`AXDocument` active URL inspection, and window tiling), `Carbon` (`TISInputSource` & `IsSecureEventInputEnabled`), `AppKit` (`NSPasteboard` change-count dedup & snapshot/restore), and `mygo/plugins/permissionflow`.
- **Windows (`internal/bridge/bridge_windows.go`)**: Uses `golang.org/x/sys/windows` (`user32`, `imm32`, `kernel32`, `dwmapi`, `shell32`, `gdi32`) for `WH_KEYBOARD_LL` hooks with tap-hold keymap remapping, `SendInput` (`SyntheticEventTag`), `GetGUIThreadInfo` caret anchoring, DWM-aware window tiling, `HKL` keyboard layout switching + `ImmGetDefaultIMEWnd` Chinese/English toggling, GDI screen capture, and `ExcludeClipboardContentFromMonitorProcessing` filtering.
- **Linux (`internal/bridge/bridge_linux.go`)**: Uses `mygo.Clipboard`, `mygo.Shell`, `mygo.Secrets`, `mygo.GlobalShortcut`, and Wayland/X11 capture utilities (`grim`+`slurp`, `scrot`, `import`) without CGo.

## Cross-Platform Support Matrix

| Target | UI / Runtime | Core & Storage | System Bridge & Capture |
| --- | --- | --- | --- |
| **macOS (`darwin/arm64`, `amd64`)** | MyGo v0.9.0 `Panel` + `VibrancyGlass` (`plugins/glass`) + `plugins/sfsymbols` + Menu Bar Tray | `pkg/core` + `plugins/sqlite` + Keychain / `vault_secrets.enc` | Full (`purego`: CGEventTap, Keymap & Input Engine, AXUIElement + Browser URL, Carbon TIS, NSPasteboard, `/usr/sbin/screencapture`) |
| **Windows (`windows/amd64`, `arm64`)** | MyGo v0.9.0 `Panel` + `VibrancyGlass` + Embedded SVG Icons + System Tray | `pkg/core` + `plugins/sqlite` + Credential Manager / `vault_secrets.enc` | Cross-build and vet coverage exist for `WH_KEYBOARD_LL`, Keymap Engine, SendInput, DWM, HKL + IMM32, and GDI full-screen capture. Real-machine parity is pending and is outside the macOS 1.0 promise. |
| **Linux (`linux/amd64`)** | MyGo v0.9.0 Native UI + Embedded SVG Icons + System Tray | `pkg/core` + `plugins/sqlite` + Secret Service / `vault_secrets.enc` | Clipboard, Shell, Keychain, `mygo.GlobalShortcut`, `grim+slurp` / `scrot` / `import` |
| **Android / iOS (`pkg/mobile`)** | Native Kotlin / Swift UI | Shared `pkg/core` via `gomobile` (`Session`) | Host app implements `StreamCallback` & `CancelToken` |

## Development, Testing & Packaging

### Test & Vet

See [`docs/CI.md`](docs/CI.md) for full CI workflow details and troubleshooting.

```bash
# Quick local pre-commit check (whitespace, vet, core/storage/sync/watcher/CLI)
make test-fast

# Full PR parity check (all tests, -race incl. uiapp/watcher, 3-OS vet/cross-build)
make ci-pr

# Run all unit and UI tests (including headless ui.NewTester frames)
go test -count=1 ./...

# Race detector verification across core, internal bridges/storage, watcher, and UI controller
make ci-race
```

### Local Signing & Safe `/Applications` Install (macOS)

```bash
# One-time setup: create local "Clavis Local Signing" cert so upgrades keep macOS TCC permissions
make setup-signing

# Build, sign, back up existing /Applications/Clavis.app, swap, verify, and relaunch
make install-local
```

### Run, Smoke Check & Self-Check

```bash
# Run directly in development
go run .

# Validate config/bridge initialization without touching ~/.config/clavis
go run . --smoke-check

# Read-only diagnostic report (config, input engine probe, permissions, AI key source)
go run . --self-check
```

### Cross-Platform Packaging (`mygo build`)

```bash
# Build cross-platform artifacts for macOS, Windows (amd64 & arm64), and Linux
go tool mygo build -platform darwin/arm64,windows/amd64,windows/arm64,linux/amd64 -skip-dmg

# Verify macOS bundle metadata, code signature, and smoke check
plutil -lint build/darwin-arm64/Clavis.app/Contents/Info.plist
codesign --verify --deep --strict --verbose=2 build/darwin-arm64/Clavis.app
./build/darwin-arm64/Clavis.app/Contents/MacOS/Clavis --smoke-check
```

## Configuration (`~/.config/clavis/`)

On first launch, Clavis creates `~/.config/clavis/` with default TOML files and hot-reloads edits via `watcher.ConfigWatcher`:

| File | Purpose |
| --- | --- |
| `config.toml` | `[trigger]` (including `screenshot_shortcut`), `[appearance]` (`language`, `animations`, `show_preview_pane`, `visual_preset`, `stage_intensity`, `oblique_rows`, `glass_clarity`), `[ai]` + `[[ai.profiles]]` (`vision_profile_id`, `continue_enabled`, `capabilities.vision`, `reasoning_effort`), `[clipboard]`, `[ime]`, `[input_engine]` (`enabled`, `mode`, `scheme`, `candidate_count`, `fuzzy_pinyin`, `custom_dict_paths`, `user_learning`, `shift_toggle_ascii`), `[keymap]`, `[terminal]`, and `[selection_bar]`. Raw API keys (`api_key`) are rejected. |
| `leader.toml` | Recursive `[[nodes]]` key tree supporting `group`, `ai_rewrite`, `window`, `cli`, `open_app`, `switch_tab`, `snippet`, `toggle_ime`, and `normalize_glossary`. |
| `snippets.toml` | `[[snippets]]` inline expansion triggers, content templates, `word_boundary`, and optional `app_bundle_ids` / `process_names` scoping. |
| `glossary.toml` | `[[terms]]` canonical terms, `aliases`, `hint`, and `category` used for AI prompt injection and local `normalize_glossary` replacement. |
| `ime-rules.toml` | `[[rules]]` per-app and per-context IME rules (`bundle_id`, `process_name`, `url_pattern`, `title_pattern`, `input_source_id`, `preferred_mode`, `restore_on_leave`). |
| `prompts.toml` | Reusable `/` prompt templates, default optimizer preset, and inline optimize prefix. |
| `quickjump.toml` | `[[bookmarks]]` destinations (`dir`, `worktree`, `url` with `{query}`, `workspace`) surfaced under `#` QuickJump alongside live Herdr workspaces/panes. |
| `components.toml` & `workflows.toml` | Optional widget/source/script toggles and declarative workflow definitions. |
| `ai-keys.toml` & `vault_secrets.enc` | `0600` provider API key store (`ai-keys.toml`, with `DEEPSEEK_API_KEY`/`ANTHROPIC_API_KEY`/`GEMINI_API_KEY` env fallback) strictly separated from AES-256-GCM Security Vault storage (`vault_secrets.enc` + Keychain / `0600` `vault.key`). |

## Language & Motion / 语言与动画

Clavis keeps configuration keys and enum values stable while translating only display text. Set the language in `config.toml`:

```toml
[appearance]
language = "auto"       # auto follows macOS/Windows/Linux UI language
# language = "zh-CN"    # force Simplified Chinese
# language = "en"       # force English
animations = "auto"     # auto | full | reduced | off

# Spatial look (see docs/design/visual-language.md)
visual_preset = "liquid_glass" # classic | liquid_glass | spatial_3d
stage_intensity = 0.7          # 0 flattens the geometry, keeps the materials; 1 is full
oblique_rows = false           # fan each result row; false keeps every row parallel
glass_clarity = 0.18           # 0..1, how much the vibrancy shows through
```

`auto` language uses the system preference; `auto` animation mode respects the platform Reduce Motion preference. `visual_preset` picks flat `classic`, default `liquid_glass`, or `spatial_3d`, and all appearance controls are live in **Settings → General → Appearance**. Design contract lives in [`docs/design/visual-language.md`](docs/design/visual-language.md).

## Omnibox Prefix Quick-Routing

Type a leading prefix in the **Actions** Omnibox (both half-width and Chinese IME full-width prefixes are accepted):

| Prefix | Full-Width Alias | Scope | Examples |
| --- | --- | --- | --- |
| `>` | `＞` | Commands, Leader actions, Capture/Annotate/OCR, Listening Ports, App IME rules, Installed Apps & Settings | `>left`, `>截图`, `>vscode` |
| `;` | `；` | Snippets (matches trigger, description & Pinyin) | `;`, `;sig`, `；sig` |
| `@` or `cb ` | `＠` | Inline Clipboard History search (supports Pin & Delete) | `@`, `@token`, `cb url` |
| `?` | `？` | Freeform AI Ask / Rewrite & AI preset modes | `?summarize in 3 bullets`, `？润色语气` |
| `:` | `：` | Emoji search (EN/ZH/Pinyin) & listening port quick-kill | `:rocket`, `：huojian`, `:3000` |
| `=` | `＝` | Calculator & Developer Utilities (UUID, Unix/ISO time, Base64, Hex, JSON/URL/Hash) | `=`, `=(2+3)*4^2`, `＝（２＋３）×４` |
| `/` | `／` | Configured Prompt templates (`prompts.toml`) | `/review`, `/sql` |
| `#` | `＃` | QuickJump across live Herdr workspaces/agents & `quickjump.toml` bookmarks | `#clavis`, `#gh search` |
| `tr ` / `fy ` / `翻译 ` | — | Bilingual Technical Translate (`zh->en` / `en->zh` auto-detected) | `tr hello world`, `翻译 连接超时` |

## Default Shortcuts

| Context | Shortcut | Action |
| --- | --- | --- |
| Global | Double-tap `Right Option` (or `Right Command`) | Open Leader Capsule at caret/cursor (with temporary ASCII IME lock) |
| Global | `Option + Space` (`Alt + Space`) | Toggle Actions Omnibox at caret/cursor |
| Global (Keymap) | Tap `CapsLock` / Hold `CapsLock + h/j/k/l` | Tap sends `Escape`; hold activates Nav Layer (`←/↓/↑/→`, `u/d` PageUp/Down, `0/4` Home/End) |
| Leader | Configured mnemonic keys / `/` / `⌫` / `Esc` | Drill into group, jump to clean Omnibox (`/`), step up (`⌫`), or dismiss |
| Omnibox | `⌘1` – `⌘6` (`Ctrl+1` – `Ctrl+6`) | Switch to Actions, Clipboard, Snippets, AI, Glossary, or Agent tab |
| Omnibox Hubs | `⌘7` / `⌘8` (`Ctrl+7` / `8`) | Toggle **Stats** or **Manage** Hub overlay (`⌘R` refreshes active hub) |
| Omnibox | `⌥1` – `⌥9` (`Alt+1` – `Alt+9`) | Instantly execute visible result row 1–9 (add `⌘`/`Ctrl` for Secondary Action) |
| Omnibox | `Tab` / `Shift + Tab`, `↑` / `↓`, `⏎` / `⌘⏎` | Cycle tabs, move selection, Primary / Secondary action |
| Omnibox | `⌘K` / `⌘I` (`Ctrl+K` / `Ctrl+I`) | Toggle contextual Action Menu / Split Preview Pane |
| Omnibox (Clipboard / `@`) | `⌘P` / `⌘⌫` (`Ctrl+P` / `Ctrl+⌫`) | Pin/unpin or delete selected clipboard item |
| AI Center | `⌘⇧M` / `⌘.` / `⌘R` / `⌘C` / `⌘D` | Cycle AI profile / Stop stream / Retry / Copy output / Toggle diff review |
| AI Composer | `Tab`, `⏎`, `⇧⏎`, `↑`/`↓`, `@`, `/` | Focus composer, send, newline, recall history, attach context chip, insert slash command |
| Annotate Window | `⌘Z` / `⌘⇧Z` / `⌘C` / `⌘S` / `Esc` | Undo / Redo / Flatten & Copy PNG / Save PNG / Close |

## Design & Architecture Specs

- [`docs/ROADMAP.md`](docs/ROADMAP.md) — Unified five-phase roadmap (`CLV-P0`–`CLV-P4`) and locked decisions (`D1`–`D4`)
- [`docs/design/capture-annotate-spec.md`](docs/design/capture-annotate-spec.md) — Region Capture, Retina Vector Annotate & `MediaStore` dedup
- [`docs/design/herdr-control-plane-spec.md`](docs/design/herdr-control-plane-spec.md) — Herdr Control Plane, Agent Discovery, Codex SQLite & Terminal Resolver
- [`docs/design/ai-workbench-spec.md`](docs/design/ai-workbench-spec.md) — 5-State AI Center, `@`/`/` Composer, Vision OCR, Translate, Continuation & Telemetry
- [`docs/design/visual-language.md`](docs/design/visual-language.md) — Spatial 3D & Liquid Glass visual language contract
