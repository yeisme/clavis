# `internal/plugins`

**Status: Experimental / Frozen (Decision D2)**

## Scope

`internal/plugins` provides a provider-neutral foundation for scanning local `plugin.toml` manifests, supervising JSON-RPC plugin subprocesses lazily, and dispatching declared capabilities (`candidates.lookup`, `style.render`, `action.run`).

Per **Decision D2** (`docs/ROADMAP.md`), Clavis remains focused on its core "second input method (Inline Hook)" architecture. This package is kept compiling and tested as a dormant internal foundation and is **not wired into the desktop runtime** or open for new extension surface.

## Security Model & Current Guarantees

- **Fail-closed action gating**: `ActionGate` defaults to `DenyAllGate{}` whenever a `nil` gate is passed to `Supervisor.RunAction`, `Supervisor.NewAutomationActionRunner`, or `actionProvider.Invoke`. Callers must inject an explicit policy gate (or `AllowAllGate{}` in trusted tests).
- **Allow-listed style output**: `Supervisor.RenderStyle` filters response payloads to fields explicitly declared in `manifest.style_fields`.
- **Declarative-only permissions**: `manifest.permissions` (`filesystem`, `network`) are metadata declarations only; this package does **not** enforce OS-level sandboxing.

## Security Prerequisites Before Any Future Desktop Activation

Before enabling `internal/plugins` in any production desktop build, all of the following gates must be satisfied:

1. **Mandatory Policy `ActionGate` & User Consent**: Wire a policy-backed `ActionGate` with explicit per-permission user approval and audit logging (never `AllowAllGate{}` in production).
2. **Manifest & Binary Provenance**: Require signature/checksum verification for `plugin.toml` and plugin entrypoints before launching executables.
3. **Process & Environment Isolation**: Replace the default `execLauncher` with a hardened launcher providing sanitized minimal environments (no parent LLM/cloud API key inheritance), command allowlisting, and bounded redacted stderr diagnostics (matching `internal/mcp.ExecProcessLauncher`).
4. **OS-Level Sandboxing**: Enforce declared `permissions.filesystem` and `permissions.network` at the OS boundary (e.g., macOS Seatbelt / App Sandbox, Windows AppContainer/Job Objects, Linux seccomp/namespaces).
5. **Resource Caps & Operational Controls**: Bound child CPU, memory, file descriptors, and expose clear enable/disable/crash diagnostics in the desktop UI.
