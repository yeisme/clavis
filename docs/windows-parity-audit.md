# Windows parity audit (darwin vs windows)
基线 `c1f0c58`（Wave 0 清账时的 HEAD）。

Scope: `internal/bridge/bridge_darwin.go` vs `internal/bridge/bridge_windows.go` (+ `winime*.go`),
and the shared `internal/bridge/{bridge,engines}.go`, `uiapp/`, `main.go`, `pkg/core/`.
Line numbers are for commit `e7c54fe` (after the T1 clipboard-shortcut and T2 Windows IME commits).

Legend: **✅ implemented** · **🟡 partial** (what is missing) · **❌ not implemented** (user-visible effect)

Most product logic (`pkg/core/engine`, `pkg/core/config`, `uiapp`) is platform neutral. The
`HostService` interface (`internal/bridge/bridge.go:19`) is the only OS seam, so parity mostly comes
down to how each bridge feeds that interface.

## Summary

| Feature | Windows | Notes |
|---|---|---|
| Leader key | ✅ | shared `triggerRouter` |
| Double-tap | ✅ | shared `DoubleTapDetector` |
| Capsule / Omnibox tabs | ✅ | engine + uiapp are shared; Ctrl glyphs on Windows |
| Clipboard shortcut (alt+shift+c) | ✅ | router + MyGo fallback accelerator |
| Snippets | 🟡 | no mouse-click buffer reset; no secure-input guard |
| Keymap / remap | ✅ | per-app remaps match by process name |
| Window commands | ✅ | DWM frame compensation; no AX permission gate |
| Open app | 🟡 | ShellExecute only; macOS bundle-id targets do not resolve |
| Clipboard polling / history / pin | 🟡 | text only (same as macOS); excluded list is exact-case process names |
| AI rewrite / streaming | 🟡 | shared copy probe in place (macOS ✅); Windows still has to implement `ProbeSelection` (Ctrl+C, console-safe) |
| Glossary | 🟡 | normalize-selection uses the same probed selection; same Windows step |
| IME | 🟡 | IMM32 compatibility layer; TSF-only IMEs limited; no ToggleInputSource |
| Tray | ✅ | MyGo tray, same items |
| Settings | ✅ | shared uiapp; IME card is Windows-aware |
| Permissions / diagnostics | 🟡 | permissions always "granted"; secure-input row is meaningless |
| URL / title IME rules | 🟡 | title ✅, URL ❌ (no ActiveURL on Windows) |
| Smoke-check | ✅ | platform-neutral |

## Details

### Leader key — ✅ implemented
- Shared detection: `triggerRouter.HandleModifier` `internal/bridge/bridge.go:172`, `DoubleTapDetector` `internal/bridge/engines.go:80`.
- darwin feeds modifiers from `kCGEventFlagsChanged` (`bridge_darwin.go:861`ff); windows from the WH_KEYBOARD_LL hook via `winVKToModifier` + `HandleModifier` (`bridge_windows.go:568-574`).
- Controller handling is shared: `uiapp/controller.go:1467` (`TriggerLeaderKey, TriggerDoubleTap`).

### Double-tap — ✅ implemented
- Same detector/router path as the leader key. Windows filters injected events (`LLKHF_INJECTED` / `SyntheticEventTag`, `bridge_windows.go:534-537`), so Clavis' own SendInput never counts as a tap.
- Difference: darwin resets the detectors on mouse down (`bridge_darwin.go:849`). Windows has no mouse hook, so a click between two taps does not cancel the double-tap (minor).

### Capsule / Omnibox tabs — ✅ implemented
- Tabs are defined in `pkg/core/config/leader_config.go:53-58` and reduced in `pkg/core/engine/capsule.go` (e.g. `:298`, `:1397`); rendering is in `uiapp/capsule_view.go`.
- Platform adaptations: `primaryMod`/`modPrefix`/`altPrefix` (`uiapp/capsule_view.go:1237-1265`), `modKey` (`pkg/core/engine/capsule.go:795`), `adaptActionShortcut` (`uiapp/capsule_preview.go:197`). Windows shows `Ctrl+`/`Alt+` hints.
- Capsule anchor: Windows uses `GetGUIThreadInfo` caret → window → mouse (`bridge_windows.go:796-802`). Apps without a Win32 caret (Chromium, Electron, UWP) fall back to the window or mouse anchor, so the capsule may not sit at the text cursor.
- The clipboard toggle (T1) is shared: router `internal/bridge/bridge.go` (clipboard hotkey), controller `uiapp/controller.go:1497`, MyGo fallback accelerators `main.go:516` (`planFallbackShortcuts`).

### Snippets — 🟡 partial
- Expansion engine is shared (`pkg/core/engine/snippet.go:234`). Injection uses `injectTextAsync` `uiapp/controller.go:1207` → `InjectText`.
- Windows `InjectText` (`bridge_windows.go:990`) sends Backspace ×N, then clipboard + Ctrl+V, then Left ×offset, and restores the clipboard. darwin first tries a direct `AXSelectedText` write (`bridge_darwin.go:1569`).
- Missing on Windows:
  - **No buffer reset on mouse click.** darwin emits `TriggerSnippetReset` on mouse down (`bridge_darwin.go:849-857`). On Windows, if you click to another spot in the same app and keep typing, a stale prefix can still complete a trigger. App switches do reset it (`bridge_windows.go:665-671`).
  - **No secure-input guard.** Windows passes `secureInputActive=false` (`bridge_windows.go:585`), so password fields are not excluded the way macOS `IsSecureEventInputEnabled` (`bridge_darwin.go:903`) excludes them.
  - **IME composition is detected by mode, not by preedit.** Windows treats "IME open + native mode" as composing (`bridge_windows.go:584`), while darwin uses AX marked text (`bridge_darwin.go:909`). In Chinese mode, all letter keys reset the buffer, so snippets only expand in 英 mode or on non-IME layouts.
  - SendInput cannot reach elevated (admin) windows because of UIPI. Injection into an elevated terminal silently does nothing.

### Keymap / remap — ✅ implemented
- Shared `KeymapLayerEngine.HandleKeyEvent` `internal/bridge/engines.go:533`. Windows calls it from the hook (`bridge_windows.go:545-566`) and emits through `sendWinKeyAction` (`:1041`).
- Per-app remaps: Windows passes the process name, and `remapRuleMatchesApp` (`engines.go:727`) also normalises `.exe`/case, so `app_bundle_ids = ["Code.exe"]` works.
- The Settings "Remap Apps" field still says bundle IDs. On Windows, users must type process names (docs/UI wording only).

### Window commands — ✅ implemented
- Shared geometry: `WindowLayoutCalculator.ComputeTargetRect` `engines.go:1467`; display lists come from `winDisplays` `bridge_windows.go:977` and `darwinDisplays` `bridge_darwin.go:1507`.
- Windows `ExecuteWindowCommand` `bridge_windows.go:892` un-maximises, compensates for DWM invisible borders, and calls `SetWindowPos`. darwin needs Accessibility (`bridge_darwin.go:1461`); Windows has no gate, but it cannot move elevated windows (UIPI). That case returns a `SetWindowPos` error.

### Open app — 🟡 partial
- Windows `OpenApp` `bridge_windows.go:1167`: `ShellExecuteW("open")`, falling back to `cmd /C start`. darwin (`bridge_darwin.go:1703`) understands bundle IDs, `.app`, URLs, and app names.
- Missing: leader actions authored on macOS with bundle IDs (default `OpenAppAction("com.apple.Terminal")`, `pkg/core/config/leader_config.go:391`) do nothing on Windows. Users must change them to an exe, path, or `shell:AppsFolder\…` AUMID, and there is no Windows default leader tree.

### Clipboard polling / history / pin — 🟡 partial
- Polling loop is shared: `main.go:775` → `PollClipboardCandidate`; Windows `bridge_windows.go:1410` uses `GetClipboardSequenceNumber` and `CF_UNICODETEXT`.
- Privacy markers: Windows maps `ExcludeClipboardContentFromMonitorProcessing` and `CanIncludeInClipboardHistory=0` (`bridge_windows.go:1448-1468`) to the shared concealed markers (`pkg/core/clipboard/filter.go:15-16`). ✅
- Self-paste suppression: `IgnoreSelfPasteboardChange` + `pbRestorer` (`bridge_windows.go:1006`, `:1017`). ✅
- History UI, pin/delete, fuzzy filter, Alt+1..9 are shared engine/uiapp code (`pkg/core/engine/capsule.go:470-477`). ✅
- Missing or partial:
  - Text only (same as macOS); images and files are not captured.
  - `excluded_bundle_ids` matches `SourceBundleID` exactly (`pkg/core/clipboard/filter.go:76-77`). On Windows that field holds the process name, case-sensitive with `.exe` (`bridge_windows.go:1429`), and the generated defaults (`com.1password.1password`, …) are macOS IDs. Password-manager copies are only filtered through the format markers.
  - The source app is the *foreground* process at poll time, not the clipboard owner (`GetClipboardOwner` is not used).

### AI rewrite / streaming — 🟡 partial
- Streaming client and SSE parser are shared (`uiapp/controller.go:1036`, `:1087`; `pkg/core/ai/sse.go`). ✅
- Missing: Windows `SnapshotContext` (`bridge_windows.go:777`) never fills `SelectedText` (darwin reads `AXSelectedText`, `bridge_darwin.go:1284`). So `resolveRewriteSourceText` (`pkg/core/engine/capsule.go:1152`) falls back to the clipboard. **User-visible:** on Windows you must copy the text before running Rewrite, and "Ask about selection" gets no context. A UIA `TextPattern` or Ctrl+C probe would be needed.
- Result insertion uses `InjectText` (paste), so it shares the snippet UIPI limitation.

**Selection capture, shared since 2026-11-15** (the macOS half of this item):
- `host.ContextSnapshot.SelectionKnown`: a platform that read the selection itself (even an empty one) sets it. When it is false, the Leader, double-tap and Capsule triggers (`uiapp/controller.go`, `resolveSelectionContext`) ask a bridge that implements `bridge.SelectionProber` to copy the selection, with a 90 ms budget, before the panel takes the focus. Other triggers never probe; a bridge without `ProbeSelection` (Windows today) behaves exactly as before.
- `internal/bridge/selection_probe.go`, `CopyProbe`: snapshot the pasteboard → send the copy shortcut → wait for the change → read the text (`PasteboardSnapshotText`: macOS UTF-8, Windows `CF_UNICODETEXT`) → restore every flavor. A line copied because nothing was selected (VS Code's `isFromEmptySelection`) and copied files (`public.file-url`, `CF_HDROP`) are not a selection. A copy that lands after the budget is still undone; the copy and the restore stay out of the clipboard history.
- macOS: `darwinBridge.ProbeSelection` sends ⌘C; it does nothing without Accessibility trust, under secure input, or with Clavis frontmost, and focus on a list, table or button (Finder) counts as a known empty selection. Verified on macOS: probe and controller tests (`-race`), and `CLAVIS_PASTEBOARD_IT=1 go test -run TestDarwinCopyProbe ./internal/bridge/` on the real NSPasteboard (text + a private flavor restored byte for byte). Not verified yet: a real app answering the synthetic ⌘C (the session was locked); it is the same `postTaggedKey` path InjectText uses for ⌘V.
- **Windows step (Windows device):** implement `func (b *windowsBridge) ProbeSelection(ctx context.Context, budget time.Duration) (string, error)` with `CopyProbe{Backend: &windowsPasteboardBackend{bridge: b}, SendCopy: <tagged Ctrl+C via sendWinCombo>, Ignore: b.IgnoreSelfPasteboardChange, LateWatch: 500 * time.Millisecond}`. Skip console windows (conhost, Windows Terminal: Ctrl+C with nothing selected interrupts the program), elevated windows (UIPI drops the input) and password fields; set `SelectionKnown` where UIA `TextPattern` reads the selection.

### Glossary — 🟡 partial
- Engine is shared (`pkg/core/engine/glossary.go:17`); the Glossary tab and search work.
- "Normalize glossary" on the selection (`uiapp/controller.go:1419`) needs `SelectedText`, which is always empty on Windows, so it can only act on clipboard text (see AI rewrite).
- Since 2026-11-15 it acts on the probed selection wherever the platform probes (macOS: tested in `uiapp/selection_context_test.go`, the normalized text replaces the selection instead of the clipboard). On Windows it follows once `ProbeSelection` exists (see AI rewrite).

### IME — 🟡 partial (after T2)
- ✅ Per-app rules: polled every 200 ms (`bridge_windows.go:631`). Rules match by process name (`engines.go` `matchRule` step 4, normalised by `normalizeProcessName`), and the Windows defaults are `WindowsTerminal.exe` and `Code.exe` with mode-only switching (`pkg/core/config/ime_rules_config.go:144`). Title-pattern rules work.
- ✅ 中/英 state: `CurrentInputSource` (`bridge_windows.go:1201`) reports `Mode` from IMC_GETOPENSTATUS and IMC_GETCONVERSIONMODE (`winime_windows.go` `winReadIMEState`). `FormatIMEPillLabel` uses it (`uiapp/controller.go:734`).
- ✅ Pill on manual switches: `imeChangeTracker` (`internal/bridge/winime.go`) fires `onIME` on every (HKL, mode) change.
- ✅ Verified switching with fallbacks and visible errors: `SetIMEMode` `bridge_windows.go:1227` → `reportIMEFailure` `:1387` → `NotifyIMESwitchFailed` (HUD plus Diagnostics "Last IME Error").
- ✅ Leader/Capsule temporary ASCII lock and restore (`bridge_windows.go:1310`, `:1339`).
- 🟡 Limitations:
  - **TSF-only IMEs.** The conversion mode is read and set through the IMM32 compatibility layer (`WM_IME_CONTROL` to `ImmGetDefaultIMEWnd`). Microsoft Pinyin and Japanese IME answer it, but some TSF-only third-party IMEs do not. Then `Mode` is empty, the label falls back to language (`中` for zh layouts), and switching falls back to an HKL change.
  - **Cross-process context.** `ImmGetContext` (second fallback) only works for windows on the calling thread, so for other apps it usually fails and the HKL switch is the real fallback.
  - **Friendly names.** TSF IMEs have no `ImmGetDescription`, so Microsoft Pinyin shows the locale name ("中文(简体，中国)"), not "Microsoft Pinyin".
  - **Per-window layouts.** Windows keeps the HKL per thread, so switching apps can itself change the pill (expected; macOS input sources are global).
  - **Hook latency.** The keyboard hook queries the IME state on every keydown (`bridge_windows.go:584`) with a 100 ms SendMessageTimeout per call. A hung foreground app can push the hook toward the `LowLevelHooksTimeout`.
- ❌ URL-pattern rules: `ActiveURL` is never filled on Windows (darwin: `bridge_darwin.go:1120`, `:1275`). **User-visible:** `url_pattern` rules never match on Windows.
- ❌ `ToggleInputSource` exists only on darwin (`bridge_darwin.go:1973`; not part of `HostService`). There is no Windows equivalent, but Win+Space covers it.
- Manual verification steps are in the T2 commit message (`e7c54fe`).

### Tray — ✅ implemented
- MyGo tray, built once in `main.go:358` (`setupTray`): permissions label, Open at Login (`mygo.App.SetOpenAtLogin`), Open Capsule, Clipboard History (`main.go:396`), Reload, Open config dir, Settings, Quit.
- On Windows the permissions entry always reads "all granted" (see below).

### Settings — ✅ implemented
- Shared uiapp window (`uiapp/settings_view.go`, `uiapp/settings_editors.go`) with tabs at `uiapp/controller.go:35-39`.
- Windows-aware since T2: IME card shows the current source and 中/EN state, readable default-source names, and process-name rule targets (`imeRuleTargetFields`/`imeRuleTargetForApp` in `uiapp/settings_editors.go`). Open-config-dir uses `explorer.exe` (`uiapp/controller.go:2195`).
- 🟡 Some labels and placeholders still say "bundle id" (IME rule target, remap apps, excluded_bundle_ids). That's wording only.

### Permissions / diagnostics — 🟡 partial
- Windows `Permissions` (`bridge_windows.go:369`) always reports everything granted, and `OpenPermissionSettings` returns `ErrUnsupported` (`:386`). That's correct for a non-elevated user session, but:
  - **User-visible:** there is no diagnostic for UIPI. When the foreground app is elevated, the hook gets no keys from it and injection or window moves fail. Clavis doesn't explain why.
  - The Diagnostics "Secure Input" row (`uiapp/settings_view.go:524-535`) always shows `false` on Windows.
- Diagnostics "Active Input Source" now shows a readable name plus 中/EN, and "Last IME Error" when a switch failed (`uiapp/settings_view.go`, `DiagnosticsSnapshot.LastIMEError`).
- Hook health: darwin re-enables a timed-out event tap and counts recoveries (`bridge_darwin.go:826-834`). Windows never re-installs the LL hook when Windows silently removes it after timeouts, and `TapRecoveryCount` stays 0. **User-visible:** after a long system stall, the leader key can stop working until restart.

### Smoke-check — ✅ implemented
- `--smoke-check` (`main.go:84`, `:95-117`) is platform neutral: it generates configs in a temp dir, decodes them, builds the bridge, and prints `os=windows`. It does not start the hook, so it does not validate hook installation or IME access.

## Suggested follow-ups (priority order)
1. Selected-text capture on Windows: implement `ProbeSelection` on the shared `CopyProbe` (console-safe Ctrl+C), then UIA TextPattern for `SelectionKnown`. Unblocks AI rewrite and glossary on the selection.
2. Re-install the LL keyboard hook when it goes silent (watchdog), and report it through `TapRecoveryCount`.
3. Mouse hook (or foreground-caret change) for snippet buffer reset; detect password fields (UIA `IsPassword`) for the secure-input guard.
4. Windows defaults for leader `open_app` targets and `excluded_bundle_ids` (`1Password.exe`, `KeePassXC.exe`), with case-insensitive process matching in the clipboard filter.
5. Browser URL for `url_pattern` rules (UIA address bar), plus a UIPI/elevation hint in Diagnostics.
