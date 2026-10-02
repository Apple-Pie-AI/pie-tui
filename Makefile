VERSION          ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
POSTHOG_API_KEY  ?=
LDFLAGS          := -s -w \
	-X github.com/Apple-Pie-AI/pie-tui/cmd.version=$(VERSION) \
	-X github.com/Apple-Pie-AI/pie-tui/internal/telemetry.APIKey=$(POSTHOG_API_KEY)

.PHONY: build obf build-test build-prod install test vet lint snapshot snapshot-obf demo clean

build: ## build the `pie` binary
	go build -ldflags "$(LDFLAGS)" -o pie .

obf: ## build an obfuscated host-platform binary (Decision 18) — requires garble
	CGO_ENABLED=0 garble -literals -tiny -seed=random build -ldflags "$(LDFLAGS)" -o pie .

build-test: ## non-obfuscated build → dist/test (default darwin/arm64; ARGS='--linux --clean')
	./scripts/build.sh test $(ARGS)

build-prod: ## obfuscated build → dist/prod (default darwin/arm64; ARGS='--linux --clean')
	./scripts/build.sh prod $(ARGS)

install: ## build and install to ~/.local/bin/pie
	go build -ldflags "$(LDFLAGS)" -o $(HOME)/.local/bin/pie .

test: ## run tests
	go test ./...

integration: ## real-claude integration tests (costs tokens; PIE_CLAUDE_BIN pins a CLI version)
	GOTOOLCHAIN=auto go test -tags integration -run Integration -count=1 -v ./internal/runner/

vet: ## go vet
	go vet ./...

snapshot: ## fast local release build, no obfuscation, no publish — requires goreleaser
	PIE_NO_OBFUSCATE=1 goreleaser release --snapshot --clean

snapshot-obf: ## obfuscated local release build (as shipped), no publish — requires goreleaser + garble
	goreleaser release --snapshot --clean

demo: build ## render every demo (desktop + mobile, GIF + MP4) from demo/*.tape — requires vhs
	for t in demo/*.tape; do vhs $$t || exit 1; done

clean:
	rm -rf pie dist
