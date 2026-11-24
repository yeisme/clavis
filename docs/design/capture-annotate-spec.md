# Region Capture & Vector Annotate Specification

> **Status**: Ground Truth (Shipped Nov 23–24, hardened in `CLV-P0-3` `9dff5b0` & `CLV-P0-4` `5cde130`)
>
> **Baseline**: HEAD `79d325b` (2026-11-25)

---

## 1. Scope & Architecture

Clavis provides a zero-CGo screen-capture pipeline paired with a standalone Retina vector annotation window, content-addressed media storage, and one-click Vision OCR / Ask AI handoff:

```text
Global Shortcut / Omnibox (`>截图`) / Clipboard Media Preview ("Annotate")
        │
        ▼
internal/capture.Capturer  ──▶  uiapp.AppController.OpenAnnotateImage / CaptureAndOCR
        │                                      │
        ▼                                      ▼
pkg/core/annotate.Document             uiapp.AnnotateView ("Clavis Annotate" window)
(pure-stdlib shapes + Flatten)         (60fps drag preview + OTF/CJK TextRenderer)
        │                                      │
        ├──▶ CopyAnnotatePNG ──▶ MediaStore + ClipboardStore + ignoreSelfClipboardWrite
        ├──▶ SaveAnnotatePNG ──▶ ~/Pictures/Clavis/clavis-YYYYMMDD-HHMMSS.png
        └──▶ SendAnnotateToAI ──▶ uiapp.StartImageOCR (AI Center Vision flow)
```

---

## 2. Platform Capture Layer (`internal/capture`)

Core contract (`internal/capture/capture.go`):
- `Capturer` interface exposes `CaptureInteractive(ctx) (Result, error)` and `CaptureFull(ctx) (Result, error)`.
- `Result{PNG, Width, Height, CapturedAt, Cancelled}` validates dimensions via `image.DecodeConfig` (`readCapturedPNG`). An empty or uncreated temp file maps to `Result{Cancelled: true}` rather than an error.
- `CommandRunner` (`LookPath`, `Run`) enables deterministic unit tests via `NewCapturerWithRunner`.
- Missing host tooling returns `ErrCaptureUnavailable`.

| Platform | Implementation | Interactive Region | Full Screen | Notes & Current Status |
| --- | --- | --- | --- | --- |
| **macOS** (`capture_darwin.go`) | `/usr/sbin/screencapture` (PATH fallback) | `screencapture -i -r -x <tmp.png>` | `screencapture -r -x <tmp.png>` | **Shipped**. Silent (`-x`), raw resolution (`-r`), `Esc` cleanly returns `Cancelled: true`. Requires Screen Recording permission declared via `NSScreenCaptureUsageDescription` in `mygo.json`. |
| **Linux** (`capture_linux.go`) | Wayland `grim`+`slurp`, X11 `scrot` / `import` | `slurp` geometry → `grim -g <geom>` (falls back to `grim`, `scrot -s`, `import`) | `grim` or `scrot` | **Shipped** (CLI-backed). Dismissing `slurp` or `scrot -s` returns `Cancelled: true`. |
| **Windows** (`capture_windows.go`) | Zero-CGo `user32.dll` + `gdi32.dll` (`GetSystemMetrics`, `BitBlt` with `SRCCOPY\|CAPTUREBLT`, `GetDIBits` BGRA→RGBA PNG encode) | Delegates to `CaptureFull` | Virtual desktop bounds (`SM_XVIRTUALSCREEN`..`SM_CYVIRTUALSCREEN`, primary fallback) | **Full-screen shipped**; interactive region selector overlay is tracked under Phase 4 Windows parity (`CLV-P4-1`). |

---

## 3. Vector Annotation Core (`pkg/core/annotate`)

Pure-stdlib model and rasterizer (`pkg/core/annotate/model.go`, `flatten.go`):
- **Tools (`ToolKind`)**: `ToolRect` (`"rect"`), `ToolArrow` (`"arrow"`), `ToolMosaic` (`"mosaic"`), `ToolText` (`"text"`).
- **Palette (`DefaultPalette()`)**: 5 high-contrast presets — Red (`#FF3B30`), Blue (`#0A84FF`), Green (`#30D158`), Amber (`#FFCC00`), White (`#FFFFFF`).
- **Document & History**: Thread-safe `Document` stores immutable `Base`/`BasePNG`, `Shapes`, `redoStack`, and monotonic `Version` (`NewDocumentFromPNG`, `NewDocumentFromImage`, `AddShape`, `Undo`, `Redo`, `Snapshot`). Adding a shape clears `redoStack`. Defaults: `StrokeWidth = 3`, `MosaicBlock = 14`, `FontSize = 20`.
- **Flattening (`Flatten(doc)`)**: Draws `Base` onto an `image.RGBA` and applies shapes in stack order:
  - `ToolMosaic`: block-averaged pixelation (`applyMosaicRect`, minimum block 4px, default 12–14px).
  - `ToolRect`: alpha-blended pixel outline (`drawRectOutline`).
  - `ToolArrow`: thick round-capped shaft + barycentric filled arrowhead (`drawArrow`, `fillTriangle`).
  - `ToolText`: delegates to pluggable `annotate.TextRenderer`; when `nil` (headless/mobile pure-stdlib mode), falls back to built-in 5×7 bitmap glyphs (`stdFont5x7`) with drop shadow.

---

## 4. Annotator Window, CJK Text Fallback & UX (`uiapp/annotate_view.go`, `main.go`)

- **CJK Font Fallback (`init()` → `annotate.TextRenderer = renderAnnotateOTFText`)**:
  - `ensureAnnotateFonts()` loads the first installed system TrueType/OpenType font whose glyph table contains `'中'` (`fontSupportsAnnotateCJK`, supporting `.ttc` collections via `opentype.ParseCollection`):
    - macOS: `PingFang.ttc`, `STHeiti Medium.ttc`, `STHeiti Light.ttc`, `Hiragino Sans GB.ttc`, `Arial Unicode.ttf`.
    - Windows: `msyh.ttc`, `msyh.ttf`, `simhei.ttf`, `simsun.ttc`.
    - Linux: `NotoSansCJK-Regular.ttc`, `NotoSansSC-Regular.otf`, `wqy-zenhei.ttc`, `DroidSansFallbackFull.ttf`.
  - Embedded `golang.org/x/image/font/gofont/goregular` serves as `annotateFallbackFont`.
  - `drawAnnotateTextWithFallback` checks each rune's glyph index on the primary font and falls back per rune to `goregular` (with a 1px dark drop shadow at alpha 140), preventing tofu boxes on mixed CJK/Latin/symbol annotations.
- **Window Lifecycle (`main.go` `handleShowAnnotateWindow` / `handleCloseAnnotateWindow`)**:
  - Dedicated window `"Clavis Annotate"` (`920×660`, min `640×460`, `StateKey: "annotate"`), created lazily via `uiapp.FloatingSurface` with `SurfaceElevationFlat`, `DismissExplicit`, and `FocusPreviousApp` on close.
  - Capture calls run under `runtimecoord.Kind("screen_capture")` with a 45s timeout (`CaptureAndAnnotate`, `CaptureAndOCR`).
  - Existing clipboard images can also reopen in the annotator via `OpenAnnotateMedia`.
- **Interactive Canvas & Shortcuts**:
  - Pointer coordinates map between letterboxed fit rect (`computeAnnotateFitRect`, no upscaling > 1×) and source image pixels; drags under 4px are ignored.
  - Live drag feedback renders directly via `ui.Painter` at 60fps; committed shapes trigger version-cached `annotate.Flatten` → `ui.DecodeBitmap` (`refreshAnnotateBitmapLocked`).
  - Inline text bar tracks IME composition (`ObserveInput` / `tin.Composing()`) so unmodified `Enter`/`Esc` during composition never commits or cancels prematurely.
  - Shortcuts: `⌘Z` Undo, `⌘⇧Z` Redo, `⌘C` Copy PNG, `⌘S` Save PNG, `Esc` dismiss text bar or close window.
  - Bottom bar: `Cancel`, `Ask AI` (vision diagnostic prompt), `AI OCR` (empty prompt → default OCR extraction), `Save PNG` (`~/Pictures/Clavis/clavis-YYYYMMDD-HHMMSS.png` or `SaveCapturePath` test hook), and `Copy`.

---

## 5. `MediaStore` Storage, Privacy & Clipboard Dedup

- **Content-Addressed Media Storage (`pkg/core/clipboard/media.go`)**:
  - `MediaStore` persists `<userDataDir>/clipboard-media/<sha256>.<ext>` (`.png`/`.jpg`/`.gif`) and box-downscaled thumbnails under `thumbs/<sha256>.png` (`ThumbMaxEdge = 256`).
  - Decompression-bomb guard (`CheckImage`): rejects payloads `> 40 MiB` (`MaxImageBytes`), dimensions `> 8000 px` (`MaxImageDim`), or total pixels `> 32,000,000` (`MaxImagePixels`) from header metadata before full decode.
  - Atomic `writeFileAtomic` creates directories with `0700`, writes temp files + `os.Rename`, rejects symlinks/directories, and short-circuits identical SHA-256 blobs.
  - Retention: `QuotaEvictions` evicts oldest unpinned `KindImage` entries above `defaultQuotaMiB = 512` (never deleting user files referenced by file/video entries); `GC` sweeps unreferenced 64-hex blobs and `.tmp-*` artifacts inside `MediaStore.Dir`.
- **Self-Write Clipboard Dedup (`uiapp/annotate_view.go`)**:
  - `CopyAnnotatePNG` calls `recordPNGToMediaAndClipboard`, which saves the flattened PNG to `MediaStore`, records a single `"Screenshot WxH"` entry (`SourceAppName: "Clavis Annotate"`) into `ClipboardStore`, and wraps the OS pasteboard write in `ignoreSelfClipboardWrite`.
  - `ignoreSelfClipboardWrite` inspects `bridge.ClipboardSequencer.ClipboardSequence()` before and after writing and signals `IgnoreSelfPasteboardChange` so the clipboard monitor does not re-ingest Clavis's own annotated PNG as a duplicate history item.
- **Clipboard Privacy Filter (`pkg/core/clipboard/filter.go`)**:
  - `PrivacyFilter` (wired in both `main.go` and `uiapp/controller.go`) blocks concealed/transient/auto-generated pasteboard markers (`DefaultConcealedTypeMarkers`, including `org.nspasteboard.ConcealedType`, `com.agilebits.onepassword`, `ExcludeClipboardContentFromMonitorProcessing`, `x-kde-passwordManagerHint`), configured `ExcludedBundleIDs`, and Windows password-manager processes (`DefaultSecretSourceProcesses`) for both text and image payloads (e.g., QR / recovery codes).

---

## 6. Permissions & Packaging Contract (`mygo.json`)

- macOS bundle declares `NSScreenCaptureUsageDescription`:
  `"Clavis needs Screen Recording permission to capture screen regions for annotation, OCR, and AI analysis."`
- Screenshot global trigger is configured via `config.toml` `[trigger].screenshot_shortcut` and registered in both the native keyboard hook (`host.TriggerScreenshotAnnotate`) and MyGo global-shortcut fallback (`main.go`).
