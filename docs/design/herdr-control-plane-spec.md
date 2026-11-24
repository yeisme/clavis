# Herdr Control Plane & Agent Session Specification

> **Status**: Ground Truth (Shipped Nov 23–24, hardened in `CLV-P0-2` `0468f29` & `CLV-P1-3` `da85969`)
>
> **Baseline**: HEAD `79d325b` (2026-11-25)

---

## 1. Architecture & Boundary Split

Clavis separates **passive multi-provider discovery** from **gated Herdr control actions** so browsing agent sessions never mutates terminal state or spawns processes without explicit user intent:

| Layer | Packages / Files | Authority |
| --- | --- | --- |
| **Read-Only Discovery** | `pkg/core/agentsession/{model,dsh,codex,claude,herdr_source,aggregate,sanitize}.go`, `internal/agentsession/codex_sqlite.go` | Passive local inspection (JSON, JSONL, `mode=ro` SQLite, `herdr agent/workspace list`, `herdr agent read`) with ANSI stripping and secret/path redaction. |
| **Gated Control Plane** | `pkg/core/agentsession/{herdr_control,focus}.go`, `uiapp/{agent_manage_controls,agent_sessions}.go` | Explicit user-triggered workspace/pane focus, rename, allowlisted CLI launch, two-step confirmed prompt delivery, and idle completion watch. |
| **Terminal Fallback** | `pkg/core/terminal/resolver.go`, `uiapp/agent_sessions.go` | Deterministic configured-profile resolution (`ghostty` → explicit `StateDegraded` fallback to Apple `Terminal`) when `herdr` is absent. |

---

## 2. Multi-Source Discovery & Codex SQLite Adapter

`agentsession.Discover(ctx, opts)` queries up to 4 sources concurrently, each capped by `ProviderTimeout` (default & max **500ms**). Source errors are returned as `Diagnostic` entries rather than discarding healthy providers:

1. **DSH** (`dsh.go`): `~/.dsh/storages/session_projcache/sessions/*.json` and `~/.dsh/threads/pi/*.jsonl`.
2. **Codex SQLite** (`internal/agentsession/codex_sqlite.go` → `pkg/core/agentsession/codex.go`):
   - `NewCodexSQLiteReader(path)` explicitly **forbids `immutable=1`** (`codex: immutable=1 is forbidden; use mode=ro`) so SQLite `-wal` frames remain visible and readings never lag behind checkpoints.
   - Opens `~/.codex/state_5.sqlite` via `file:...?mode=ro` with `sqlite.OpenOptions{ReadOnly: true, BusyTimeout: 100ms}` (falls back to path with `ReadOnly: true` only when URI filenames are disabled; never opens writable).
   - Dynamically inspects `sqlite_master` and `PRAGMA table_info(threads)` to adapt across schema revisions (`id`/`thread_id`/`conversation_id`, `updated_at`/`last_activity_at`, `cwd`/`workspace`/`working_directory`, `awaiting_human`, `pending_review`, etc.) with a hard limit of 500 rows.
3. **Claude Code** (`claude.go`): `~/.claude/projects` JSONL logs.
4. **Live Herdr Daemon** (`herdr_source.go`): enabled when `opts.HerdrExecutor != nil` (`uiapp` passes `currentHerdrExecutor()`). Calls `herdr agent list` and `herdr workspace list` (800ms timeout each), maps `agent_status` (`working`/`running`/`active`/`busy` → `StatusRunning`; `blocked`/`awaiting_human`/`needs_confirmation`/`approval` → `StatusWaiting` + `PendingReview`/`AwaitingHuman`; `idle`/`waiting`/`ready` → `StatusWaiting`; `done`/`stopped`/`completed`/`exited` → `StatusStopped`; otherwise `StatusUnknown`), sets `HerdrManaged: true`, and syncs live workspaces & panes into Omnibox `#` QuickJump (`syncAgentQuickJumpTargets`).

---

## 3. Terminal Output Sanitization (`sanitize.go` & `ReadOutput`)

- `HerdrClient.ReadOutput(ctx, target, lines)` executes `herdr agent read <target> --source recent-unwrapped --lines <N> --format text` (1200ms timeout; `lines` defaults to 60, capped at 200; UI fetches 80 lines asynchronously via `EnsureAgentOutput` with a 3s freshness cache and in-flight deduplication).
- All terminal text passes through `SanitizeMultilineOutput(raw, maxLines, maxBytes)` (default 180 lines / 20 KiB):
  - Strips OSC (`ansiOSC`), CSI (`ansiCSI`), and single-char (`ansiSingle`) ANSI escape sequences.
  - Redacts credentials matching `secretRE` (`bearer`, `sk-...`, `gh[pous]_...`, `github_pat_...`, `xox[bp]-...`, `api_key`/`secret_key`/`access_token`/`password`/`token` assignments) → `[redacted]`.
  - Redacts absolute filesystem paths (`pathRE`) → `[path]`.
  - Truncates on valid UTF-8 rune boundaries and closes unbalanced triple-backtick code fences (` ``` `) for safe Markdown rendering.
- One-line summaries and diagnostics pass through `SanitizeDiagnosticText` (strips code blocks, secrets, absolute paths, collapses whitespace, caps at 120 runes).

---

## 4. Gated Control Plane Boundaries (`herdr_control.go` & `agent_manage_controls.go`)

Every write operation on `HerdrController` enforces explicit safety invariants:

### 4.1 Per-Session UI State Isolation
- `agentManageUIState` stores per-pane state in `sessions map[string]*agentSessionControlState`, keyed by `agentSessionControlKey(session)` (prefers `PaneID`, falls back to `ID`) with bounded LRU eviction (`maxAgentSessionControlStates = 64`).
- `renameOpen`, `renameDraft`, `promptOpen`, `promptDraft`, `confirmArmed`, and `statusMsg` are strictly scoped to one session key. Switching sessions, clicking a preset, or editing the prompt text clears `confirmArmed = false`.

### 4.2 Two-Step Confirmation & Live Target Re-Verification (`SendPrompt`)
1. UI requires clicking **Prepare Send** (`confirmArmed = true`) before revealing **Confirm Send** (`SendHerdrPrompt(target, draft, true)`).
2. `HerdrController.SendPrompt` rejects `req.Confirmed == false` with `code = "confirmation_required"`, `Retryable = false`.
3. Rejects empty/NUL payloads, unsafe target identifiers, and secret-bearing prompts before spawning any command.
4. Re-queries live `herdr agent list` immediately before dispatch and verifies `target` matches a live `PaneID`, `Name`, or `AgentSession.Value` (`target_not_found` if vanished).
5. Executes `herdr agent send <target> --message <msg>` (2500ms timeout), disarms `confirmArmed`, and calls `ForceRefreshAgentOutput(target)` on success.

### 4.3 Unified Secret Screening (`containsSendSecret`)
- Combines `sanitize.go`'s `secretRE` with `sendSecretPattern` covering:
  - Provider token prefixes: `sk-`, `gh[pous]_`, `github_pat_`, `glpat-`, `xox[bp]-`, AWS `AKIA...`.
  - Quoted credential assignments (`bearer`, `api_key`, `secret_key`, `access_token`, `password`, `passwd`, `token`).
  - PEM private-key headers (`-----BEGIN ... PRIVATE KEY-----`).
- Matches abort delivery with `code = "secret_blocked"`, `State = StateFailed`, `Retryable = false`.

### 4.4 Option-Injection & Control-Character Rejection
- `isUnsafeCLIValue(v)` rejects any value starting with `-` or containing `\x00`.
- `isUnsafeCLIIdentifier(v)` additionally rejects `\r` and `\n`.
- Applied across `FocusPane` (`paneID`), `FocusWorkspace` (`workspaceID`), `RenameAgent` (`target`, `cleanName`, max 40 runes), `SendPrompt` (`target`, plus NUL check on `msg`), `StartAgent` (`workspace`, `name`), and `WaitState` (`target`, `state`).

### 4.5 6-CLI Start Allowlist (`StartAgent`)
- `allowedHerdrAgentCLIs` permits **only**: `claude`, `codex`, `pi`, `agy`, `gemini`, `opencode`.
- Any other CLI value is rejected with `code = "disallowed_agent_cli"`. Dispatches `herdr agent start --agent <cli> [--workspace <ws>] [--name <name>]` (3s timeout).

### 4.6 `StateUnknown` Non-Retryable Contract
- Every control operation returns `host.OperationResult{State, Code, Message, ObservedAt, Retryable}`:
  - `SendPrompt`: any execution error or timeout after `herdr agent send` begins returns `StateUnknown` (`send_outcome_unknown`, `Retryable: false`) because delivery to the terminal cannot be proven absent — automatic retries are strictly prohibited.
  - `FocusPane`, `FocusWorkspace`, `RenameAgent`, `WaitState`: `context.DeadlineExceeded` maps to `StateUnknown` (`Retryable: false`).
  - UI refresh checks `agentRefreshRetryable()` (`result.Retryable && result.State != host.StateUnknown`) and prompts manual refresh on unknown outcomes.
- `WatchHerdrAgentCompletion` runs `herdr agent wait <target> --state idle --timeout 45` under `runtimecoord.AgentWait` (60s outer cap, deduplicated per target in `watching map[string]bool`) and notifies via HUD + refresh upon completion.

---

## 5. Session Focus & Terminal Resolver Fallback

1. Selecting an Agent session copies `session.ID` to clipboard and invokes `focusAgentSession`:
   - Probes `HerdrFocuser.Available(ctx)` (checks both `LookPath("herdr")` and valid `herdr agent list` JSON — never assumes binary presence equals working daemon).
   - If `session.PaneID != ""`, calls `PaneFocuser.FocusPane` directly (`herdr agent focus <paneID>`).
   - Otherwise calls `HerdrFocuser.Focus(ctx, session.ID)`, which uses **exact** `sessionFileMatchesID` matching (`<id>` or `<prefix>_<id>` after stripping `.jsonl`). Substring matching is prohibited; 0 matches returns `StateUnknown` (`session_not_found`) and >1 matches returns `StateUnknown` (`session_ambiguous`).
2. When `herdr` is unavailable, `focusAgentSession` triggers `openTerminalForAgentFallback`:
   - Resolves `cfg.Main.Terminal` through `pkg/core/terminal.NewResolver` (`ResolveTerminal`).
   - Checks configured `Default` profile (default `"ghostty"`) via app bundle directories (`/Applications`, `~/Applications`, etc.) or `PATH`.
   - If unavailable, explicitly degrades **only** to configured `"terminal"` / `"apple-terminal"` (`StateDegraded`, `code: "terminal_fallback"`, `open -a Terminal <dir>`) — never silently picking an unrelated third terminal. If neither exists, returns `StateFailed` (`terminal_unavailable`).
   - If process execution fails but a `BundleID` is known, falls back to `bridge.OpenApp(bundleID)`.
