# Windows parity audit (darwin vs windows)

基线 `3166819`（2026-11-25，Phase 4.1 Windows Parity Hardening）。

Scope: `internal/bridge/bridge_darwin.go` vs `internal/bridge/bridge_windows.go` (+ `input_engine_windows.go`, `ports_windows.go`, `hook_health_windows.go`, `winime*.go`, `bridge_windows_helpers.go`), `internal/capture/capture_windows.go`, and the shared `internal/bridge/{bridge,engines,triggers,selection_probe}.go`, `uiapp/`, `main.go`, `pkg/core/`.

Legend: **✅ implemented** · **🟡 partial** (what is missing) · **❌ not implemented** (user-visible effect)

Verification legend:
- **Unit/Cross-compiled**: verified via cross-platform unit tests (`go test -race`), `GOOS=windows GOARCH=amd64 go vet ./...`, and `GOOS=windows GOARCH=amd64 go test -c` (`bridge_windows_test.go`, `capture_test.go`).
- **Needs Windows HW**: code path calls Win32 APIs (`WH_KEYBOARD_LL`, `WH_MOUSE_LL`, `SendInput`, `ShellExecuteW`, `OpenClipboard`, `ms-screenclip:`, IMM32) and requires end-to-end verification on a real Windows 10/11 desktop.

Most product logic (`pkg/core/engine`, `pkg/core/config`, `uiapp`) is platform neutral. `HostService`, `ExtendedHostService`, `InlineIMEForcer`, and `SelectionProber` (`internal/bridge/bridge.go`, `selection_probe.go`) are the OS seams, so parity comes down to how each bridge implements those contracts.

## Summary

| Feature | Windows | Verification | Notes |
|---|---|---|---|
| Leader key | ✅ | Unit/Cross-compiled · Needs Windows HW | shared `triggerRouter` + `WH_KEYBOARD_LL` |
| Double-tap | ✅ | Unit/Cross-compiled · Needs Windows HW | `WH_MOUSE_LL` click resets wired; double-tap resets `winShiftTracker` cleanly |
| Own Input Engine (inline hook) | ✅ | Unit/Cross-compiled · Needs Windows HW | Real `ForceTakeover` (`SetInlineIMEForced`/`InlineIMEForced`), eligibility-gated Shift toggle, modifier-safe `winShiftTracker`, dead-key safe `ToUnicodeEx` (`wFlags=0x4`), app-switch & mouse-click reset |
| Capsule / Omnibox tabs | ✅ | Unit/Cross-compiled | engine + uiapp are shared; Ctrl/Alt glyphs on Windows; `FastCaretAnchor` wired to `resolveWinCaretAnchor` + mouse fallback |
| Clipboard shortcut (alt+shift+c) | ✅ | Unit/Cross-compiled | router + MyGo fallback accelerator |
| Snippets | 🟡 | Unit/Cross-compiled · Needs Windows HW | `WH_MOUSE_LL` click resets ✅; fail-closed Win32 `Edit`/`ES_PASSWORD` + password-manager secure-input guard ✅; Chromium/Electron UIA `IsPassword` and UIPI elevated windows still limited |
| Keymap / remap & bindings | ✅ | Unit/Cross-compiled | per-app remaps (`remapRuleMatchesApp`) and `[[keymap.bindings]]` (`bindingAppMatches`) both normalize `.exe` suffix and case |
| Window commands | ✅ | Unit/Cross-compiled · Needs Windows HW | DWM frame compensation; UIPI still blocks moving elevated windows |
| Open app | 🟡 | Unit/Cross-compiled · Needs Windows HW | `cmd /C start` shell-injection fallback removed; target validation + `ShellExecuteW` + safe direct `.exe` `LookPath` fallback; macOS bundle-id presets still do not map automatically |
| Clipboard polling / history / restore | 🟡 | Unit/Cross-compiled · Needs Windows HW | Non-`HGLOBAL` GDI/display/private formats filtered before `GlobalLock`/`SetClipboardData`; `OpenClipboard` retry; `IgnoreSelfPasteboardChange` pre-registered before `EmptyClipboard`/`SetClipboardData`; text + media kind supported; Windows default `excluded_bundle_ids` & `GetClipboardOwner` still follow-ups |
| Selection probe & AI rewrite | 🟡 | Unit/Cross-compiled · Needs Windows HW | `SelectionProber` (`ProbeSelection`) implemented via `CopyProbe` (`Ctrl+C` + `CF_UNICODETEXT` + restore, VS Code `isFromEmptySelection` filter, skips console/terminal/password/self windows); UIA `TextPattern` (`SelectionKnown`) not yet implemented |
| In-place transform & Selection bar | ✅ | Unit/Cross-compiled · Needs Windows HW | `FastCaretAnchor`, `CaptureEditableField`, and `ReplaceEditableField` implemented via tagged `SendInput` (`Ctrl+A`/`Ctrl+C`/`Ctrl+V`) with clipboard snapshot/restore, Secure Input guard, and console guard |
| Glossary | ✅ | Unit/Cross-compiled · Needs Windows HW | Normalize-selection unblocked by `ProbeSelection` |
| Screenshot capture | ✅ | Unit/Cross-compiled · Needs Windows HW | Interactive region snip via `ms-screenclip:` clipboard PNG/DIB capture with `ctx.Err()` cancellation, Esc dismiss detection, and clean fallback to GDI `CaptureFull` |
| IME (system auto-switch) | 🟡 | Unit/Cross-compiled · Needs Windows HW | IMM32 conversion mode + HKL fallback + `imeChangeTracker`; TSF-only IMEs limited; no `ActiveURL` (`url_pattern`) yet; no `ToggleInputSource` |
| Hook robustness & lifecycle | ✅ | Unit/Cross-compiled · Needs Windows HW | `WH_MOUSE_LL` click resets, `GetLastInputInfo` watchdog (`TapRecoveryCount`), fail-closed `hookSecureInput` + off-hook `currentSecureInput` refresh, `sync.WaitGroup` clean `Stop()` shutdown |
| Tray & Settings | ✅ | Unit/Cross-compiled | MyGo tray + Windows-aware Settings IME card |
| Permissions / diagnostics | 🟡 | Unit/Cross-compiled · Needs Windows HW | Hook reinstall count, mouse-hook status, and live secure-input flag reported in `Details`; UIPI elevation hint still missing |
| Smoke-check | ✅ | Unit/Cross-compiled | platform-neutral |

## Details (Phase 4.1 Hardening Highlights)

### Own Input Engine & Keyboard Hook (`input_engine_windows.go`, `bridge_windows.go`, `bridge_windows_helpers.go`) — ✅ implemented (Needs Windows HW)
- **Eligibility-gated Shift ASCII toggle**: `inputEngineHandleKey` now checks `inputEngineEligibleAt` before processing Shift taps, so password fields, Clavis's own windows, and active native Windows IMEs never mutate `eng.ascii`.
- **Modifier-safe Shift tracking (`winShiftTracker`)**: Pressing `Ctrl`, `Alt`, `Win`, `CapsLock`, a second Shift key, or any non-modifier key disarms pending Shift taps so Windows' `Alt+Shift` / `Ctrl+Shift` input-language shortcuts and `Shift+key` chords never falsely toggle Clavis ASCII mode. Double-tap Shift (`TriggerInputEngineToggle` / `selectionEvent`) and `resetInputEngine` call `shiftTracker.Reset()`, eliminating stale `shiftAlone=true` state. Dead `shiftDown`/`shiftHadKey` struct fields were removed.
- **Real `ForceTakeover`**: `SetInlineIMEForced` / `InlineIMEForced` are backed by `windowsBridge.inlineForced` (`atomic.Bool`), synced by `ConfigureInputEngine` and `ReloadInputEngine`, and honored in `winInputEngineEligible` (overriding native Windows IME while keeping Secure Input and Clavis self-app guards active).
- **Dead-key safety**: `rawWinVKToUnicode` passes `wFlags = 0x4` (`ToUnicodeEx` no-state-mutation flag, Windows 10 1607+) so international dead-key layouts are not corrupted when Clavis inspects keystrokes.
- **App switch reset**: `runIMEObserver` calls `b.resetInputEngine()` on every foreground app switch (including switches into Clavis windows), matching darwin's `stepIMEObserver`.

### Hook Robustness, Secure Input & Shutdown (`hook_health_windows.go`, `bridge_windows.go`) — ✅ implemented (Needs Windows HW)
- **`hookSecureInput` fails closed**: On the hot `WH_KEYBOARD_LL` callback path, stale or never-observed secure-input state now fails closed via `cachedFlag.activeOrUnknown(now, secureFieldMaxAge)` instead of failing open.
- **Off-hook refresh (`currentSecureInput`)**: Off-hook callers (`ProbeSelection`, `CaptureEditableField`, `ReplaceEditableField`, `hookHealthDetails`) refresh the secure-field check synchronously when stale and still fail closed if the foreground window cannot be inspected.
- **Clean `Stop()` shutdown**: `runHookWatchdog` and `runMouseResetWorker` are tracked by `windowsBridge.bgWG` (`sync.WaitGroup`) and awaited cleanly in `Stop()`.

### Keymap Bindings (`.exe` normalization) (`triggers.go`) — ✅ implemented (Unit-tested)
- `bindingAppMatches` now normalizes `.exe` suffixes and case via `normalizeProcessName` (and supports `*` wildcard), matching `remapRuleMatchesApp` in `engines.go`. Verified in `internal/bridge/triggers_test.go`.

### Safe Clipboard Capture / Restore (`bridge_windows.go`, `bridge_windows_helpers.go`) — ✅ implemented (Unit-tested · Needs Windows HW)
- **Non-`HGLOBAL` filter**: `isWinHGlobalClipboardFormat` skips `CF_BITMAP` (2), `CF_METAFILEPICT` (3), `CF_PALETTE` (9), `CF_ENHMETAFILE` (14), `CF_OWNERDISPLAY` (`0x0080`), `CF_DSP*` (`0x0081..0x0083`, `0x008E`), and private/GDI handle ranges (`0x0200..0x03FF`) before calling `GlobalLock`/`GlobalSize`/`SetClipboardData`.
- **Contention & poller safety**: `openWinClipboard` retries briefly on transient clipboard lock contention; `Restore` and `writeClipboardUnicodeText` call `IgnoreSelfPasteboardChange` before `EmptyClipboard` and before/after each `SetClipboardData` so restored clipboards never re-trigger the clipboard history poller.
- **VS Code empty-line detection**: `windowsPasteboardBackend.Capture` maps registered format `"vscode-editor-data"` by name (and `Restore` resolves it back via `RegisterClipboardFormatW`) so `CopyProbe` filters VS Code's empty-selection line copies on Windows just as on macOS.

### Safe `OpenApp` (`bridge_windows.go`, `bridge_windows_helpers.go`) — 🟡 partial (Unit-tested · Needs Windows HW)
- Removed the unsafe `cmd /C start "" <target>` fallback completely.
- `validateWinOpenAppTarget` rejects empty inputs, control characters, and shell metacharacters; `OpenApp` tries `ShellExecuteW("open")` first and falls back only to direct `.exe`/`.com` execution via `exec.LookPath` + `os.Stat` without spawning `cmd.exe`.
- Still partial: macOS bundle-ID defaults (e.g. `com.apple.Terminal`) still need Windows target presets.

### Selection Probing & Editable Field Parity (`ports_windows.go`, `bridge_windows_helpers.go`) — ✅ implemented (Unit-tested · Needs Windows HW)
- **`FastCaretAnchor`**: Wired to `resolveWinCaretAnchor(hwnd)` with `resolveWinMouseAnchor()` fallback, honoring context cancellation.
- **`SelectionProber` (`ProbeSelection`)**: Uses shared `CopyProbe` with `windowsPasteboardBackend`, tagged `Ctrl+C`, `IgnoreSelfPasteboardChange`, and `500ms` `LateWatch`. Skips console/terminal processes and window classes (`conhost`, `WindowsTerminal`, `cmd`, `powershell`, `pwsh`, `ConsoleWindowClass`, `CASCADIA_HOSTING_WINDOW_CLASS`, etc.), secure/password fields, and Clavis windows.
- **`CaptureEditableField` & `ReplaceEditableField`**: Implemented via `captureWinEditableFieldWithOps` and `replaceWinEditableFieldWithOps` using tagged `SendInput` (`Ctrl+A`/`Ctrl+C`/`Ctrl+V`/`Backspace`) with clipboard snapshot/restore, Secure Input guard, and console guard. Preserves existing selections when text is already highlighted.

### Screenshot Capture (`internal/capture/capture_windows.go`) — ✅ implemented (Unit-tested · Needs Windows HW)
- `CaptureInteractive` launches `ms-screenclip:` via `ShellExecuteW`, polls the clipboard for `"PNG"` or `CF_DIB` (converted to PNG), detects Esc dismissal when `ScreenClippingHost.exe`/`SnippingTool.exe` closes without an image, honors `ctx.Done()`, and falls back cleanly to GDI `CaptureFull` when `ms-screenclip:` is unavailable or headless.
- Both `CaptureInteractive` and `CaptureFull` check `ctx.Err()` before running.

### Automated Verification Coverage
- **Cross-platform pure-Go tests (run on macOS, Linux, Windows)**:
  - `internal/bridge/bridge_windows_helpers_test.go` — HGLOBAL format filter, `winShiftTracker` state transitions, `winInputEngineEligible`, `OpenApp` target validation, console/copy-probe safety guard, editable-field capture/replace workflows.
  - `internal/bridge/triggers_test.go` — `.exe` suffix normalization in `bindingAppMatches` / `bindingFor`.
  - `internal/bridge/hook_health_test.go`, `selection_probe_test.go`, `winime_test.go`, `keydown_unicode_test.go`.
  - `internal/capture/capture_test.go` — context cancellation, Linux `slurp` runner routing, Windows interactive/cancel/fallback runner mocks.
- **Windows-targeted tests (`//go:build windows`)**:
  - `internal/bridge/bridge_windows_test.go` — interface satisfaction (`ExtendedHostService`, `InlineIMEForcer`, `SelectionProber`, etc.), `SetInlineIMEForced` / `ForceTakeover`, `hookSecureInput` fail-closed, eligibility-gated Shift toggle, `FastCaretAnchor`, secure-input/cancellation guards, `OpenApp` injection rejection, and `Stop()` `WaitGroup` cleanup. Cross-compiled via `GOOS=windows GOARCH=amd64 go test -c`.

## Remaining Follow-ups (Real Windows Hardware & Feature Gaps)
1. **Real Windows 10/11 desktop hardware verification**: Run `go test -race ./internal/bridge/... ./internal/capture/...` natively on Windows and manually verify `WH_KEYBOARD_LL` / `WH_MOUSE_LL`, `Alt+Shift` / `Ctrl+Shift` coexistence, `ForceTakeover`, `ProbeSelection` / In-Place Optimize across Notepad, Chrome, VS Code, and Windows Terminal, and `ms-screenclip:` interactive capture.
2. **UIA `TextPattern` & `IsPassword`**: Populate `ContextSnapshot.SelectionKnown` without `Ctrl+C` where UIA exposes selection directly, and detect Chromium/Electron password inputs via UIA `IsPassword`.
3. **Browser `ActiveURL` (`url_pattern` IME rules)**: Query browser address bar via UIA so `url_pattern` rules match on Windows.
4. **Windows default presets**: Provide Windows defaults for leader `open_app` targets and clipboard `excluded_bundle_ids` (`1Password.exe`, `KeePassXC.exe`, case-insensitive process matching) + `GetClipboardOwner`.
5. **UIPI elevation diagnostics**: Detect elevated foreground windows and surface an actionable hint in Diagnostics.
