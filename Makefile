SHELL := /usr/bin/env bash
.DEFAULT_GOAL := help

.PHONY: help hooks test-fast ci-pr ci-race bench-clipboard release-verify setup-signing install-local rollback-local

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

hooks: ## Install repository .githooks (refuses to overwrite non-empty custom hooksPath)
	@cur="$$(git config --local --get core.hooksPath || true)"; \
	if [ -n "$$cur" ] && [ "$$cur" != ".githooks" ]; then \
		echo "error: custom core.hooksPath already set to '$$cur'; refusing to overwrite" >&2; \
		exit 1; \
	fi; \
	chmod +x .githooks/*; \
	git config --local core.hooksPath .githooks; \
	echo "✓ Installed git hooks (.githooks)"

test-fast: ## Fast local check (<90s target): diff whitespace, go vet, core/pkg tests
	git diff --check
	go vet ./...
	go test -count=1 ./pkg/... ./internal/secrets/... ./internal/storage/... ./internal/sync/... ./internal/watcher/... ./internal/diag/... ./internal/filesearch/... ./cmd/clavis/... .

ci-race: ## Run race detector across uiapp, watcher, core, internal, CLI & root
	go test -count=1 -race \
		./uiapp \
		./internal/watcher \
		./internal/secrets/... \
		./internal/storage/... \
		./internal/sync/... \
		./internal/daemon/... \
		./internal/diag/... \
		./internal/filesearch/... \
		./pkg/core/... \
		./cmd/clavis/... \
		.

ci-pr: ## Full PR gate matching GitHub Actions (tests, race, 3-OS vet/build, scripts, manifest)
	git diff --check
	find scripts .githooks -type f \( -name '*.sh' -o -path '.githooks/*' \) -print0 | xargs -0 -n1 bash -n
	python3 -m json.tool mygo.json > /dev/null
	go vet ./...
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go vet ./...
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go vet ./...
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/bridge
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o /dev/null ./internal/capture
	go test -count=1 ./...
	$(MAKE) ci-race
	h="$$(mktemp -d)"; gopath="$$(go env GOPATH)"; gomod="$$(go env GOMODCACHE)"; gocache="$$(go env GOCACHE)"; trap 'rm -rf "$$h"' EXIT; \
	HOME="$$h" GOPATH="$$gopath" GOMODCACHE="$$gomod" GOCACHE="$$gocache" go run . --smoke-check && \
	HOME="$$h" GOPATH="$$gopath" GOMODCACHE="$$gomod" GOCACHE="$$gocache" go run . --self-check && \
	HOME="$$h" GOPATH="$$gopath" GOMODCACHE="$$gomod" GOCACHE="$$gocache" go run ./cmd/clavis sync plan

bench-clipboard: ## Run 120Hz clipboard switch benchmarks
	go test -run '^$$' -bench BenchmarkClipboardSwitch -benchmem -count=3 ./uiapp/

release-verify: ## Build macOS package and verify signature, plist, version & smoke-check
	./scripts/package.sh
	plutil -lint build/darwin-arm64/Clavis.app/Contents/Info.plist
	codesign --verify --deep --strict --verbose=2 build/darwin-arm64/Clavis.app
	h="$$(mktemp -d)"; trap 'rm -rf "$$h"' EXIT; HOME="$$h" ./build/darwin-arm64/Clavis.app/Contents/MacOS/Clavis --smoke-check

setup-signing: ## Create/verify 'Clavis Local Signing' cert so /Applications upgrades keep TCC permissions
	./scripts/setup-local-signing.sh

install-local: ## Build, stably sign, backup, swap into /Applications/Clavis.app and launch
	./scripts/install-local.sh

rollback-local: ## Roll back /Applications/Clavis.app to the latest local backup
	./scripts/install-local.sh --rollback
