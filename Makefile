.PHONY: help build build-release install clean fmt test test-race lint ci

GO      ?= go
BINARY  := polimero
VERSION ?= dev
RELEASE_TARGETS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the binary (output: ./polimero)
	$(GO) build -o $(BINARY) .

build-release: ## Build release binaries into ./dist
	@rm -rf dist && mkdir -p dist
	@set -e; \
	for target in $(RELEASE_TARGETS); do \
		goos="$${target%/*}"; \
		goarch="$${target#*/}"; \
		ext=""; \
		if [ "$$goos" = "windows" ]; then ext=".exe"; fi; \
		out="dist/$(BINARY)_$${goos}_$${goarch}$$ext"; \
		echo "building $$target -> $$out"; \
		GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED=1 \
			$(GO) build -ldflags "-X github.com/polimero-app/cli/cmd.Version=$(VERSION)" -o "$$out" .; \
	done

install: ## Install the binary to GOPATH/bin
	$(GO) install .

clean: ## Remove the built binary
	rm -f $(BINARY)

fmt: ## Check gofmt formatting (fails on unformatted files)
	@unformatted="$$( gofmt -l . )"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: the following files are not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

test: ## Run tests
	@packages="$$( $(GO) list ./... )"; \
	if [ -n "$$packages" ]; then \
		$(GO) test $$packages; \
	else \
		echo "no Go packages yet; skipping tests"; \
	fi

test-race: ## Run tests with race detector
	@packages="$$( $(GO) list ./... )"; \
	if [ -n "$$packages" ]; then \
		$(GO) test -race $$packages; \
	else \
		echo "no Go packages yet; skipping race tests"; \
	fi

lint: ## Run golangci-lint (skipped if not installed)
	@if [ -z "$$( $(GO) list ./... 2>/dev/null )" ]; then \
		echo "no Go packages yet; skipping lint"; \
	elif command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed; skipping lint"; \
	fi

ci: fmt test test-race lint ## Run full CI suite (fmt + test + test-race + lint)
