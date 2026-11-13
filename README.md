# Clavis

Clavis is a keyboard-first, non-activating floating **Capsule** launcher and productivity assistant for macOS 14+. It combines a mnemonic Leader key tree, fuzzy Omnibox search, inline snippet expansion, domain-glossary-aware AI rewriting/prompting, local SQLite FTS5 clipboard history, window management, and per-application IME auto-switching.

## Feature Matrix

| Area | Capabilities |
| --- | --- |
| **Leader Key Tree** | Modifier double-tap activation (`right_option` / `right_command`), recursive mnemonic key groups, automatic fuzzy-search downgrade into Actions Omnibox on unmatched keys |
| **Omnibox Launcher** | Tabbed search across **Actions**, **Clipboard**, **Snippets**, **AI**, and **Glossary** (`⌘1`–`⌘5`, `Tab` / `Shift+Tab`), IME-composition-safe input handling |
| **AI Rewrite & Freeform Prompting** | OpenAI-compatible SSE streaming, preset rewrite/translate modes, freeform `"Ask AI"` / `"Rewrite with AI"` in AI & Actions tabs, automatic `<glossary>` prompt injection, Keychain API key storage (`OPENAI_API_KEY` fallback), Settings connection test |
| **Glossary Normalization** | Local longest-first alias normalization (`normalize_glossary`) on selected text (in-place replacement) or clipboard preview, plus searchable glossary term reference |
| **Clipboard History** | SQLite + FTS5 persistence with privacy filters (concealed types & excluded bundle IDs), pin (`⌘P`) and delete (`⌘⌫`) directly from the Capsule |
| **Inline Snippets** | Boundary-aware trigger expansion with dynamic tokens and cursor placement (`SnippetRenderer`), suppressed automatically when macOS Secure Input is active |
| **Window Management** | Accessibility-based `left_half`, `right_half`, `top_half`, `bottom_half`, `maximize`, `center`, `next_display`, `previous_display`, `restore` |
| **Per-App IME Rules** | Automatic input source switching by bundle ID (`ime-rules.toml`) with optional restore-on-leave and HUD feedback |

## Architecture

Clavis is structured as a Swift Package with three targets:

- **`ClavisCore`**: Platform-independent core engine, TOML parser/encoder, config models & validator, `CapsuleStateMachine`, `FuzzyMatcher`, `GlossaryEngine`, `SnippetEngine` / `SnippetRenderer`, `ClipboardStore` (SQLite3 + FTS5), `OpenAICompatibleClient` + `SSEStreamParser`, and `CLIActionExecutor`.
- **`ClavisMacOS`**: macOS integration layer using AppKit, Carbon, Accessibility (`AXUIElement`), `CGEvent` tap (`KeyboardMonitor` with automatic timeout recovery), `CursorPositionResolver`, `TextInjector`, `WindowManager`, `InputSourceManager`, `PasteboardWatcher`, `KeychainAPIKeyStore`, `ConfigWatcher`, and `CapsuleLayoutCalculator`.
- **`ClavisApp`**: Menu-bar (`LSUIElement`) macOS application (`AppCoordinator`, `NonActivatingPanel` / `CapsulePanelController`, `CapsuleView`, `SettingsView`, `SettingsWindowController`, `MenuBarController`).

## Build, Test & Package (CommandLineTools)

Clavis builds and tests with Apple CommandLineTools (`Swift 6.x`, macOS 14+ deployment target) without requiring full Xcode.

```bash
# Build
swift build

# Run unit tests (Swift Testing plugin path required on CommandLineTools-only macOS)
swift test -Xswiftc -plugin-path -Xswiftc /Library/Developer/CommandLineTools/usr/lib/swift/host/plugins/testing

# Package signed app bundle at build/Clavis.app
./scripts/package-app.sh

# Smoke check the packaged binary
./build/Clavis.app/Contents/MacOS/Clavis --smoke-check

# Preview or run installation to /Applications/Clavis.app
./scripts/install-app.sh --dry-run
./scripts/install-app.sh
```

To sign with a specific Developer ID instead of ad-hoc signing (`-`):

```bash
CODESIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)" ./scripts/package-app.sh
```

## Configuration (`~/.config/clavis/*.toml`)

On first launch, Clavis automatically creates `~/.config/clavis/` with default TOML files and hot-reloads changes via `ConfigWatcher`:

| File | Purpose |
| --- | --- |
| `config.toml` | `[trigger]`, `[appearance]`, `[ai]`, `[clipboard]`, and `[ime]` settings. Raw API keys (`api_key`) are rejected—store keys in macOS Keychain via **Settings → AI & Keychain** or `OPENAI_API_KEY`. |
| `leader.toml` | Recursive `[[nodes]]` key tree supporting `group`, `ai_rewrite`, `window`, `cli`, `open_app`, `switch_tab`, `snippet`, `toggle_ime`, and `normalize_glossary`. |
| `snippets.toml` | `[[snippets]]` inline expansion triggers, content templates, `word_boundary`, and optional `app_bundle_ids` scoping. |
| `glossary.toml` | `[[terms]]` canonical terms, `aliases`, `hint`, and `category` used for AI prompt injection and local `normalize_glossary` replacement. |
| `ime-rules.toml` | `[[rules]]` per-app `bundle_id`, `input_source_id`, `preferred_mode` (`ascii`, `native`, `keep`), and `restore_on_leave`. |

## Default Shortcuts

| Context | Shortcut | Action |
| --- | --- | --- |
| Global | Double-tap `Right Option` (or `Right Command`) | Open Leader Capsule at cursor |
| Global | `Option + Space` | Toggle Actions Omnibox at cursor |
| Leader | Configured mnemonic keys | Drill into group or execute action (unmatched key downgrades to Actions Omnibox) |
| Leader | `⌫` / `Esc` | Step up one group or close Capsule |
| Omnibox | `⌘1` – `⌘5` | Switch to Actions, Clipboard, Snippets, AI, or Glossary tab |
| Omnibox | `Tab` / `Shift + Tab` | Cycle tabs forward / backward |
| Omnibox | `↑` / `↓` or `⌃P` / `⌃N` | Move selection |
| Omnibox | `⏎` | Execute selected item (or run freeform AI prompt) |
| Omnibox (Clipboard) | `⌘P` | Pin / unpin selected clipboard item |
| Omnibox (Clipboard) | `⌘⌫` | Delete selected clipboard item |
| AI Streaming | `⏎` | Replace selection / inject AI output |
| AI Streaming | `⌘C` | Copy AI output to clipboard |
| AI Streaming | `Esc` | Cancel active stream or close Capsule |
