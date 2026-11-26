# Clavis Final Gate

Date: 2026-11-27

## Clean source

The gate ran in the detached worktree `/private/tmp/clavis-final-gate.9DHNkV`, created from `origin/main`.

```text
HEAD 7646da4c9113b71f8dffcb7b3408008aa297fe84
git status --short --branch
## HEAD (no branch)
```

The source ref matched `origin/clv/p5-foundation` at the same commit.

## Test and build gate

All commands below ran from the clean worktree with `/opt/homebrew/bin` first in `PATH`.

```text
go test ./...
ok  github.com/yeisme/clavis
ok  github.com/yeisme/clavis/cmd/clavis
ok  github.com/yeisme/clavis/internal/storage
ok  github.com/yeisme/clavis/pkg/core/ai
ok  github.com/yeisme/clavis/uiapp
exit 0

go build ./...
exit 0

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
exit 0
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build ./...
exit 0
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
exit 0

go vet ./...
exit 0
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
exit 0
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go vet ./...
exit 0
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go vet ./...
exit 0
```

## Script and manifest checks

```text
find scripts -type f -name '*.sh' -print0 | xargs -0 -n1 bash -n
shell syntax ok: 6 scripts

manifest validation ok: com.yeisme.clavis, macOS usage descriptions present
scripts/version.sh
0.1.0-dev.511+7646da4c9113
```

The manifest check parsed `mygo.json`, verified the bundle identifier, and verified all four macOS usage descriptions, including `NSScreenCaptureUsageDescription`.

## Package verification

```text
scripts/package.sh
==> version: 0.1.0-dev.511+7646da4c9113
✓ Signed Clavis.app
✓ Created clavis-0.1.0-dev.511+7646da4c9113-arm64.dmg
==> bundled version: Clavis 0.1.0-dev.511+7646da4c9113 (darwin/arm64)
```

The generated artifacts were `build/darwin-arm64/Clavis.app`, the arm64 DMG, the darwin-arm64 ZIP, their SHA-256 files, and `clavis-cli`.

```text
codesign --verify --deep --strict
valid on disk
satisfies its Designated Requirement
Identifier=com.yeisme.clavis
TeamIdentifier=not set
Mach-O 64-bit executable arm64

Clavis.app/...: OK
clavis-cli: OK
clavis-0.1.0-dev.511+7646da4c9113-arm64.dmg: OK
clavis-0.1.0-dev.511+7646da4c9113-darwin-arm64.zip: OK
```

`TeamIdentifier=not set` is expected for this machine because it has no valid Developer ID identity. The package used MyGo's explicit ad-hoc signing path.

## Runtime checks

```text
go run . --smoke-check
Clavis smoke-check ok (leader=right_option, os=darwin, arch=arm64)

HOME=<temporary clean home> go run . --self-check
Clavis self-check (darwin/arm64)
permissions: accessibility=true input-monitoring=true apple-events=true granted=false
permissions: NOT granted — grant Accessibility + Input Monitoring in System Settings
ai key source: none (<temporary clean home>/.config/clavis/ai-keys.toml missing)
self-check ok
```

The self-check was read-only and ran with a temporary home directory. No application was copied to `/Applications`.
