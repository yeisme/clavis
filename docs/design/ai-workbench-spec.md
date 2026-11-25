# AI Workbench Specification (5-State Center, Composer, OCR/Translate, Continuation & Telemetry)

> **Status**: Ground Truth (Shipped Nov 23–24, hardened in `CLV-P0-3` `9dff5b0` & `CLV-P0-4` `5cde130`)
>
> **Baseline**: HEAD `79d325b` (2026-11-25)

---

## 1. Five-State AI Interaction Center (`uiapp/ai_center_view.go`)

`renderAIStreamingView` maps real `engine.State` and `aiReview` eligibility into five explicit, non-percentage states (`resolveAIInteractionPhase`):

| Phase | Badge & Tone | Trigger Condition | Process Log Visibility | Primary Actions |
| --- | --- | --- | --- | --- |
| `pending` | `○ Pending` (neutral) | Empty prompt, output, and source text | Hidden | Compose prompt & `Enter` / Send |
| `running` | `● Streaming` (accent) | `!state.AIComplete` with active prompt/source/output | **Expanded** (live char/line count + pulsing caret) | `⌘.` / Stop (`stopActiveAIStream`), `⌘C` Copy |
| `awaiting_human` | `▲ Awaiting approval` (warning) | `state.AIComplete` and diff review eligible (`hasReview`) | **Collapsed** chip (`Process · N/3 steps`, expandable) | **Blocking approval gate**: Replace (`Enter`), Copy (`⌘C`), Toggle Diff (`⌘D`), Revise (`Tab`), Close (`Esc`) |
| `done` | `✓ Done` (success) | `state.AIComplete` without pending replacement gate | **Collapsed** chip (expandable) | Replace (`Enter`), Copy (`⌘C`), Retry (`⌘R`), multi-turn follow-up |
| `failed` | `✕ Error` (danger) | `state.AIError != ""` | **Expanded** (marks failed step + error shake keyframes) | Edit & Retry (`⌘R` / `Enter`) |

- **Truthful 3-Step Log (`aiStepLog`)**: Strictly derived from runtime state (1. Prompt & context capture — selection rune count & turn count; 2. Model stream generation — active model name & received chars/lines; 3. Verify & deliver / Awaiting human approval with target app name). Never fabricates progress percentages, tool calls, or estimated dollar costs.
- **Blocking Approval Gate (`renderAICenterBlockingApprovalGate`)**: When replacing text in an external target app, displays the target app name and `−deleted +inserted` diff counts (`engine.TextDiff`), keeping the composer hidden until the user explicitly clicks **Revise** or presses `Tab`.
- **Top Strip & History**: Renders clickable profile selector pills (`⌘⇧M` cycles profiles) + today's compact token badge (`renderAICenterTopStrip`), collapsible source-text drawer, and deduplicated multi-turn conversation history (`aiDisplayHistory`).

---

## 2. Multiline `@context` & `/command` Input Composer (`uiapp/input_composer.go`)

`renderInputComposer` upgrades follow-up and freeform prompting into an auto-growing 2–5 line `ui.TextArea` (`Enter` submits, `Shift+Enter` inserts newline):

- **Strict 3-Tier Keyboard Priority (`OnTextKey`)**:
  1. **IME Preedit Priority**: When `followUpComposing()` is true (`ObserveInput` / `in.Composing()`), unmodified `Enter`, `Esc`, `Up`, `Down`, and `Tab` are owned exclusively by the IME so candidate selection never sends, navigates menus, or closes Capsule.
  2. **Completion Popup Priority**: When `@` or `/` popup is open (`menuOpen`), `↑`/`↓` cycle up to 4 visible rows, `Tab`/`Enter` accepts the completion, and `Esc` dismisses the active token (`dismissedToken`).
  3. **Composer Shortcuts**: `Backspace` on an empty field removes the last attached chip (`popLastComposerChip`); `↑`/`↓` on single-line text steps through a 30-entry session history (`composerHistoryLimit = 30`, preserving in-progress draft); `Esc` closes Capsule; `Enter` submits.
- **`@context` Mention Chips (`composerMentionCandidates`)**:
  - Triggered by a whitespace-delimited `@...` token (`detectComposerTrigger`).
  - Offers only real runtime context: `@selection` (char count + app), `@clipboard` (preview sanitized via `agentsession.SanitizeDiagnosticText`), `@app` (active app), `@glossary` (configured terms), and up to 3 cached `@agent` sessions (one-line redacted summaries).
  - Accepting attaches a removable `composerChip`; `composePromptWithChips` prepends `[Label] Payload` lines to the submitted prompt without inventing missing fields.
- **`/command` Slash Menu (`composerSlashCandidates`)**:
  - Triggered **only** at index 0 or immediately after `\n`/`\r` so paths/URLs like `foo/bar` never open the menu.
  - Merges 6 built-in presets (`/concise`, `/bullets`, `/explain`, `/translate`, `/fix`, `/review`) with configured `prompts.toml` items (capped at 6 matches).
- **Running State**: Swaps the text area for a calm locked status bar keeping attached chips visible alongside an inline `Stop` (`⌘.`) button.

---

## 3. Multimodal Vision OCR & Bilingual Translate

### 3.1 Vision OCR (`uiapp/ocr_translate_view.go`, `pkg/core/ai/multimodal.go`)
- **Image Preparation (`PrepareOCRImage`)**:
  - Validates non-empty raster payload, normalizes MIME (`image/png`, `image/jpeg`, `image/webp`, `image/gif`; `image/jpg` → `image/jpeg`), and decodes bounds.
  - Scales images exceeding `ocrMaxDimension = 2048` px on the longest side via pure-Go `golang.org/x/image/draw.BiLinear` and re-encodes to PNG.
  - Enforces `ai.MaxImageAttachmentBytes = 10 MiB` and `ai.MaxImageAttachments = 8` (`ValidateImageAttachments`).
- **Vision Profile Resolution (`ai.ResolveVisionProfile`)**:
  1. Explicit `cfg.Main.AI.VisionProfileID` (if `Capabilities.Vision == true`).
  2. Active AI profile (if `Capabilities.Vision == true`).
  3. First configured profile with `Capabilities.Vision == true`; otherwise returns `ClientError{Kind: "vision_unsupported"}`.
- **Multi-Protocol Multimodal Encoding**:
  - OpenAI-compatible: `content` array with `{type: "image_url", image_url: {url: "data:<mime>;base64,...", detail}}`.
  - Anthropic Messages: `{type: "image", source: {type: "base64", media_type, data}}`.
  - Gemini `streamGenerateContent`: `{inline_data: {mime_type, data}}`.
- **Inline OCR Bar (`renderOCRTranslateBar`)**: Surfaces active Vision profile name with quick toggles for **Plain Text** (`defaultOCRPrompt(false)`) and **MD Table** (`defaultOCRPrompt(true)`), or a direct **Configure Vision** link to `SettingsTabAI` when no Vision profile exists. Supports both fresh captures (`StartImageOCR`) and stored clipboard images (`StartClipboardImageOCR`).

### 3.2 Directional Bilingual Translate (`pkg/core/engine/translate.go`, `uiapp/ocr_translate_view.go`)
- **Quick Triggers (`ParseTranslateQuery`)**: `tr `, `fy `, `翻译 `, `translate ` (plus colon variants and full-width normalization), falling back to selected text or clipboard preview when no trailing argument is typed.
- **Script Detection (`DetectTranslateDirection`)**: Any Han rune → `TranslateZhToEn` (`"zh->en"`); Latin letter → `TranslateEnToZh` (`"en->zh"`); otherwise `TranslateAutoToZh` (`"auto->zh"`).
- **Technical Preservation (`BuildTranslatePrompt`)**: Sets `SystemPromptOverride` instructing the model to preserve code blocks, inline `` `code` ``, identifiers, paths, URLs, numbers, and Markdown structure verbatim.
- **Source-Hash Direction Reset (`translateDirectionFor`)**: Hashes source text with SHA-256 (`lastSourceHash`); whenever source text changes, any manual `ZH → EN` / `EN → ZH` override automatically resets to `TranslateAuto` (`CLV-P0-3`) so stale manual directions never corrupt subsequent translations.

---

## 4. AI Continuation & Floating Selection Bar

### 4.1 Explicit AI Continuation (`uiapp/ai_continue.go`, `uiapp/snippet_undo.go`)
- **Opt-In & Disclosure**: Triggered only by an explicit user chord (never automatic background polling). Requires `cfg.Main.AI.ContinueEnabled == true`; on first use, shows a one-time preview toast and persists `ContinueDisclosed = true`.
- **Secure Input Gate**: Immediately aborts with a localized HUD warning if `isSecureInputEnabled()` is true.
- **Rune-Safe Context Slicing (`tailBeforeCaret`)**: Slices `[]rune(snap.Value)` up to `SelectionEnd` (clamped safely to `[0, len(runes)]`) and retains at most the trailing `aiContinueContextChars = 200` runes (requiring ≥ 2 runes).
- **Streaming & Parsing**: Uses `aiContinueTimeout = 8s` under `runtimecoord.AIStream`, requests up to `aiContinueOptions = 3` single-sentence continuations (`AIContinuePrompt`), and cleans bullets/numbering/quotes via `ParseAIContinuation`.
- **Caret Insertion & 5-Second Undo**: Injects the top option via `Bridge.InjectText(ctx, best, 0, 0)` and arms `armContinuationUndo(best)` (reusing `snippetUndoWindow = 5 * time.Second` with `typed = ""`, guarded by `SnippetBackspaceUndo` and `undoSafeExpansion` grapheme safety) so an immediate `Backspace` removes only the inserted runes.

### 4.2 Floating Selection Bar (`uiapp/selection_bar_view.go`)
- Non-activating (`ShowInactive`) floating action strip (`selectionBarHeight = 34`, width computed dynamically via `PreferredSelectionBarWidth` using CJK 1.0em / Latin 0.62em metrics, clamped `[120, 560]`).
- Wires configured actions (`optimize_prompt`, `explain`, `translate`, `polish`, `copy`) to mouse click, digit shortcuts `1`–`5`, and `Esc` dismissal via `SelectionBarViewWithController`.

---

## 5. Security & Privacy Guardrails (`uiapp/controller.go`, `pkg/core/ai/context.go`)

1. **OS Secure Input Blocking (`isSecureInputEnabled`)**:
   - Queries `secureInputBridge.IsSecureInputEnabled()` (macOS Carbon `IsSecureEventInputEnabled`) or `Permissions.Details["secureInput"]`.
   - When active, blocks both `startAIContinue` and any `startAIStream` request carrying sensitive ambient context (`SourceText`, `SelectedText`, `ClipboardPreview`, `SurroundingText`, or active transform).
2. **Auto-Attached Secret Scrubbing (`scrubAutoAttachedSecrets`)**:
   - Runs automatically on `SelectedText`, `ClipboardPreview`, and `SurroundingText` before constructing `ai.CompletionRequest`.
   - Redacts PEM private-key blocks/headers (`aiPEMPrivateKeyBlockRe`, `aiPEMPrivateKeyHeaderRe`) and secret tokens (`sk-...`, `sk_live_...`, `sk_test_...`, `ghp_...`, `github_pat_...`, `xox[bp]-...`) with `[REDACTED]`, **unless** the user explicitly typed that exact token in `eff.Prompt`.
3. **Context Policy & MCP Redaction (`pkg/core/ai/context.go`)**:
   - `ApplyContextPolicy` allowlists ambient fields with per-field rune limits; `sanitizeMCPResources` parses JSON keys (`authorization`, `cookie`, `apikey`, `accesstoken`, `token`, `secret`, `password`, `passwd`) and applies regex redaction capped at 32 resources / 16,384 total runes.

---

## 6. Usage Telemetry (`pkg/core/ai/observability.go`, `internal/storage/learning_writer.go`, `uiapp/ai_usage_view.go`)

- **Pricing-Independent Accounting**: `ParseProviderUsage` / `mergeProviderUsage` normalizes token counts across OpenAI (`prompt_tokens`/`completion_tokens`/`prompt_tokens_details.cached_tokens`), Anthropic (`input_tokens`/`output_tokens`/`cache_read_input_tokens`), and Gemini (`usageMetadata.promptTokenCount`/`candidatesTokenCount`/`cachedContentTokenCount`), tracking `Reported bool` so omitted usage is never guessed as zero.
- **Zero-Blocking Persistence**: `main.go` wires `LearningWriter` as `AIUsageRecorder` on `AppController` / `AIClient`. `LearningWriter.RecordAIUsage` enqueues `UsageRecord` in memory and flushes asynchronously in batches (`750ms` interval or `256` ops) to the SQLite `ai_usage` table (`at_ns`, `profile`, `provider`, `model`, `action`, `input_tokens`, `output_tokens`, `cached_tokens`, `reported`, `latency_ms`) — never blocking streaming or UI frames.
- **UI Surfacing**: `ensureAIUsageRefresh` reads 7-day aggregates asynchronously for three surfaces: the Stats Hub (`⌘7`) AI card, a real 7-day stacked column chart of input + output tokens per day (`renderAIUsageHubChart`, built on MyGo's `ui/chart`: round token ticks, hairline grid, today in the accent, hover/keyboard selection with a tooltip of date · input · output · calls); the AI tab's compact 7-bar usage band (`renderAIUsageBand`); and the AI Center top strip (`Today <tokens>`). Tokens are the only spend measure: no price is stored, so no currency figure is ever shown or estimated. Prompt contents and credentials are never stored in telemetry (`Fingerprint` hashes only when diagnostics are needed).
