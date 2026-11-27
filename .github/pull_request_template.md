## Summary
- Task ID (optional for manual PRs, required for agent branches): `T-clavis-...`
- Allowed / touched paths:
- What changed and why:

## Verification Evidence
- [ ] `make test-fast` / `go test -count=1 ./...`
- [ ] `make ci-race` (incl. `./uiapp` & `./internal/watcher`)
- [ ] `make ci-pr` (three-platform `vet` + cross-build + scripts/manifest checks)
- [ ] Real macOS hardware check (if UI/bridge changed)

## Notes / Risks
- Any remaining known issues or follow-ups:
