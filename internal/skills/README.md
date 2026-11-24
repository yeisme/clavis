# `internal/skills`

**Status: Experimental / Frozen (Decision D2)**

## Scope

`internal/skills` provides filesystem discovery, TOML parsing, and structural validation for declarative `skill.toml` manifests and their relative `actions` / `workflows` file references. It intentionally performs **no execution** and grants **no permissions**.

Per **Decision D2** (`docs/ROADMAP.md`), Clavis remains focused on its core "second input method (Inline Hook)" architecture. This package is kept compiling and tested as a dormant internal foundation and is **not wired into the desktop runtime** or open for new host extension surface.

## Security Prerequisites Before Any Future Desktop Activation

Before loading or executing skill bundles in any production desktop build, all of the following gates must be satisfied:

1. **Explicit Opt-In & Provenance Verification**: Require explicit user enablement and cryptographic integrity/signature verification for skill bundles before activation.
2. **Strict Path Containment**: Enforce canonical path resolution and symlink-escape checks (`filepath.EvalSymlinks` within the skill root directory) before reading any referenced `actions` or `workflows` files.
3. **Fail-Closed Execution & Approval Gating**: Route all skill-triggered actions and workflows through a fail-closed permission gate with explicit user approval for filesystem, network, or external tool side effects.
4. **Dependency Pinning & Cycle Checks**: Implement deterministic dependency resolution, version pinning, and cycle/supply-chain validation before loading transitive skill dependencies.
5. **Context & Secret Isolation**: Prevent untrusted skill templates or workflows from accessing parent process credentials, raw clipboard history, or privileged IME/input-hook state.
