# Clavis CI/CD & Release Verification Guide

> Design reference: [`docs/design/cicd-guardrails-2026-11-27.md`](design/cicd-guardrails-2026-11-27.md)

## CI Workflows

| Workflow | Trigger | Required Checks / Purpose |
| --- | --- | --- |
| [`.github/workflows/ci.yml`](../.github/workflows/ci.yml) | `push` to `main`, `pull_request`, manual | `clavis / policy & cross-build-vet`, `clavis / macos-test`, `clavis / macos-race` (incl. `uiapp` & `internal/watcher`), `Test (Linux)`, `Test (Windows)` (`continue-on-error` portability signal), and `clavis / required-checks` |
| [`.github/workflows/nightly-stress.yml`](../.github/workflows/nightly-stress.yml) | Daily 03:00 CST + manual | Full `-race` + `3x` `-race -shuffle=on` on `./uiapp` and `./internal/watcher` |
| [`.github/workflows/release-candidate.yml`](../.github/workflows/release-candidate.yml) | Manual (`ref` + `clavis_version`) | Clean gate + `scripts/package.sh` + DMG mount/clean-install verification + SHA-256 checksum verification + 14-day artifact retention |

## Local Parity Commands (`Makefile`)

- `make hooks`: Configure `core.hooksPath = .githooks` (validates staged whitespace, `gofmt`, shell syntax, `mygo.json`, commit message format, and pre-push `go vet`).
- `make test-fast`: Quick local pre-commit sanity sweep.
- `make ci-race`: Local race detector run covering `./uiapp` and `./internal/watcher`.
- `make ci-pr`: Full local gate before pushing or opening a PR.
- `make setup-signing` & `make install-local`: Maintain a stable local `"Clavis Local Signing"` identity so `/Applications/Clavis.app` upgrades preserve macOS TCC permissions; use `make rollback-local` to revert to the previous backup.

## Troubleshooting Common CI Failures

1. **`digest_mismatch` on Windows**: `.gitattributes` enforces `eol=lf` across text/contract files so SHA-256 digests stay byte-identical across platforms.
2. **Linux SQLite permission failures**: Ensure `TestMain` creates a private `0700` temp base under `$HOME` with deterministic `umask(022)` (see `internal/storage/main_test.go`).
3. **`mygo.json` validation**: `mygo.json` is plain JSON, not an Apple plist — validate with `python3 -m json.tool mygo.json`, and reserve `plutil -lint` for generated `Clavis.app/Contents/Info.plist`.
