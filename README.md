# Clavis

Clavis is a keyboard-first, non-activating floating **Capsule** launcher and productivity assistant built with **Go + MyGo v0.9.0** (`mygo/ui`, `plugins/glass`, `plugins/sfsymbols`, `plugins/sqlite`, and `plugins/permissionflow`). It combines a mnemonic Leader key tree, fuzzy Omnibox search, multi-protocol AI profiles with multi-turn follow-up, smart context-aware IME switching with a cursor-side floating pill, tap-hold keymap remapping with a Vim-style navigation layer, inline snippet expansion, domain-glossary normalization, local SQLite FTS5 clipboard history, and window management.

## Feature Matrix

| Area | Capabilities |
| --- | --- |
| **Leader Key Tree** | Modifier double-tap activation (`right_option` / `right_command`), recursive mnemonic key groups, automatic ASCII IME lock during Leader navigation (`lock_ascii_on_leader`), automatic fuzzy-search downgrade into Actions Omnibox on unmatched keys |
| **Omnibox Launcher & Utilities** | Tabbed search across **Actions**, **Clipboard**, **Snippets**, **AI**, and **Glossary** (`⌘1`–`⌘5`, `Tab` / `Shift+Tab`), installed **App Launcher** with Retina icons, instant **Calculator** (`128 * 3.5`), **Developer Utilities** (`uuid`, `now` / Unix timestamp, `base64`, `hex`), and **Emoji Quick Search** (`:rocket` or Chinese keywords like `:火箭` / `笑`), IME-composition-safe input |
| **Multi-Protocol AI & Follow-Up** | Built-in `[[ai.profiles]]` supporting `openai_compatible` (**DeepSeek** `deepseek-chat` default, **Ollama** `qwen2.5-coder:7b` local keyless), `anthropic` (**Claude** `claude-3-5-sonnet-latest` Messages SSE), and `gemini` (**Gemini** `gemini-2.5-flash` `streamGenerateContent` SSE); multi-turn `Tab` follow-up prompt inside `PhaseAIStreaming`; automatic `<glossary>` prompt injection; per-profile OS credential storage & environment variable fallback |
| **Smart IME & Floating Pill** | Context-aware input source switching (`ime-rules.toml`) matching by `url_pattern` (`github.com`, `*.docs.rs`), regex `title_pattern`, `bundle_id`, or `process_name`; macOS Carbon TIS + browser AX URL sensing; Windows `HKL` + `ImmGetDefaultIMEWnd` / `WM_IME_CONTROL` Chinese/English conversion mode switching; non-activating cursor-side `"中 / EN"` floating micro-pill (`IMEPillView`); visual IME Rule Editor in Settings |
| **Keymap Tap-Hold & Nav Layer** | Configurable `[[keymap.remaps]]` tap-vs-hold engine (e.g., tap `caps_lock` → `escape`, hold `caps_lock` → `nav_layer`) with Vim-style `h/j/k/l` → Arrow keys, `u/d` → PageUp/PageDown, `0/$` → Home/End |
| **Glossary Normalization** | Local longest-first alias normalization (`normalize_glossary`) on selected text (in-place replacement) or clipboard preview, plus searchable glossary term reference |
| **Clipboard History** | SQLite + FTS5 persistence (`plugins/sqlite`) with privacy filters (concealed types & excluded bundle IDs), pin (`⌘P` / `Ctrl+P`) and delete (`⌘⌫` / `Ctrl+⌫`) directly from the Capsule |
| **Inline Snippets** | Boundary-aware trigger expansion with dynamic tokens and cursor placement, suppressed automatically when OS Secure Input is active |
| **Window Management** | `left_half`, `right_half`, `top_half`, `bottom_half`, `maximize`, `center`, `next_display`, `previous_display`, `restore` |
| **Vector & Native Icons** | Embedded vector SVG icon set (`ui.MustParseSVG`) across all platforms with automatic macOS `plugins/sfsymbols` rendering and Retina app icon extraction (`mygo.Shell.InstalledApps` / `FileIcon`) |

## Architecture & Project Layout

Clavis uses a unified Go codebase with a pure-stdlib core, zero-CGo native OS bridges, and native GPU-accelerated MyGo UI:

```text
clavis/
├── main.go                 # Desktop entrypoint, --smoke-check, MyGo Capsule/IMEPill/Settings/Tray lifecycle
├── main_test.go            # Smoke-check, Capsule & IME pill screen coordinate clamping, and tray icon tests
├── mygo.json               # MyGo v0.9.0 bundle manifest, NSIS installer config & macOS privacy usage descriptions
├── resources/icon.png      # High-craft squircle application icon (512x512 RGBA PNG)
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
| `config.toml` | `[trigger]`, `[appearance]`, `[ai]` + `[[ai.profiles]]` (`deepseek` default, `ollama`, `claude`, `gemini`), `[clipboard]`, `[ime]` (`lock_ascii_on_leader`, `show_floating_pill`), and `[keymap]` + `[[keymap.remaps]]` settings. Raw API keys (`api_key`) are rejected—store keys in the OS credential store via **Settings → AI & Keychain** or the profile's `api_key_env_var` (`DEEPSEEK_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`). |
| `leader.toml` | Recursive `[[nodes]]` key tree supporting `group`, `ai_rewrite`, `window`, `cli`, `open_app`, `switch_tab`, `snippet`, `toggle_ime`, and `normalize_glossary`. |
| `snippets.toml` | `[[snippets]]` inline expansion triggers, content templates, `word_boundary`, and optional `app_bundle_ids` scoping. |
| `glossary.toml` | `[[terms]]` canonical terms, `aliases`, `hint`, and `category` used for AI prompt injection and local `normalize_glossary` replacement. |
| `ime-rules.toml` | `[[rules]]` per-app and per-context rules with `bundle_id`, `process_name`, `url_pattern`, `title_pattern`, `input_source_id`, `preferred_mode` (`ascii`, `native`, `keep`), and `restore_on_leave`. Editable visually under **Settings → IME & Keymap**. |

## Default Shortcuts

| Context | Shortcut | Action |
| --- | --- | --- |
| Global | Double-tap `Right Option` (or `Right Command`) | Open Leader Capsule at caret/cursor (with automatic ASCII IME lock) |
| Global | `Option + Space` (`Alt + Space`) | Toggle Actions Omnibox at caret/cursor |
| Global (Keymap) | Tap `CapsLock` / Hold `CapsLock + h/j/k/l` | Tap sends `Escape`; hold activates Nav Layer (`←/↓/↑/→`, `u/d` PageUp/Down, `0/$` Home/End) |
| Leader | Configured mnemonic keys | Drill into group or execute action (unmatched key downgrades to Actions Omnibox) |
| Leader | `⌫` / `Esc` | Step up one group or close Capsule (restoring previous IME) |
| Omnibox | `⌘1` – `⌘5` | Switch to Actions, Clipboard, Snippets, AI, or Glossary tab |
| Omnibox | `Tab` / `Shift + Tab` | Cycle tabs forward / backward |
| Omnibox | `↑` / `↓` or `⌃P` / `⌃N` | Move selection |
| Omnibox | `⏎` | Execute selected action/app/utility/emoji (or run freeform AI prompt) |
| Omnibox (Clipboard) | `⌘P` / `Ctrl+P` | Pin / unpin selected clipboard item |
| Omnibox (Clipboard) | `⌘⌫` / `Ctrl+⌫` | Delete selected clipboard item |
| AI Streaming | `Tab` | Focus multi-turn follow-up input (submit with `⏎`) |
| AI Streaming | `⏎` | Replace selection / inject AI output |
| AI Streaming | `⌘C` / `Ctrl+C` | Copy AI output to clipboard |
| AI Streaming | `Esc` | Cancel active stream or close Capsule |
