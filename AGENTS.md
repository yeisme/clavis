# Clavis Agent Quick-Start (`AGENTS.md`)

1. **Product Scope (1.0)**: macOS daily-driver desktop launcher, clipboard manager, Security Vault, QuickJump, capture/annotate, AI/OCR/translate, Herdr control plane, and opt-in second-IME. Windows (`test-windows` is `continue-on-error` portability signal) and multi-device Sync GUI (1.1, D1 shared-dir encrypted sync) are outside 1.0 delivery claims. See `docs/ROADMAP.md` and `docs/design/scope-1.0.md`.
2. **Everyday Commands (`Makefile`)**:
   - `make test-fast` — whitespace check + `go vet` + fast core/storage/sync/watcher/CLI tests.
   - `make ci-race` — full `-race` including `./uiapp` and `./internal/watcher`.
   - `make ci-pr` — exact local mirror of required GitHub CI checks (3-OS vet/cross-build + full tests + `-race` + isolated `--smoke-check`/`--self-check`).
   - `make setup-signing` / `make install-local` — create local `"Clavis Local Signing"` certificate (keeps macOS TCC permissions across upgrades) and install `/Applications/Clavis.app` with automatic backup.
   - `make hooks` — wire `.githooks/` cleanly.
3. **Engineering Discipline**:
   - Never use `git add -A`; stage explicit file paths to avoid touching concurrent worktree files.
   - Never declare a task `verified` without running post-edit checks (`make ci-pr` or relevant `-race` + cross-OS `vet`).
   - Never leak secrets, raw API keys, or `/Users/<username>` local paths in code, logs, or docs.
   - Stop and escalate if `go test -race ./uiapp ./internal/watcher` reproduces a concurrency flake, or if a required check fails twice.
4. **CI & Release Reference**: See `docs/CI.md` and `docs/design/cicd-guardrails-2026-11-27.md`.
