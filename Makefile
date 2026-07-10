.PHONY: help build release install clean fmt test test-race lint ci

GO      ?= go
BINARY  := polimero
VERSION ?= dev
RELEASE_TARGETS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

help: ## Show available targets
	@printf "\033[37mUsage:\033[0m\n"
	@printf "  \033[37mmake [target]\033[0m\n\n"
	@printf "\033[34mAvailable targets:\033[0m\n"
	@grep -E '^[0-9a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[0;36m%-12s\033[m %s\n", $$1, $$2}'
	@printf "\n"

build: ## Build the binary (output: ./polimero)
	$(GO) build -o $(BINARY) .

release: ## Build release binaries into ./dist
	@rm -rf dist && mkdir -p dist
	@set -e; \
	host_os="$$(uname -s | tr '[:upper:]' '[:lower:]')"; \
	case "$$host_os" in \
		mingw*|msys*|cygwin*) host_os="windows" ;; \
		darwin*) host_os="darwin" ;; \
		linux*) host_os="linux" ;; \
	esac; \
	host_arch="$$(uname -m)"; \
	case "$$host_arch" in \
		x86_64) host_arch="amd64" ;; \
		aarch64|arm64) host_arch="arm64" ;; \
	esac; \
	for target in $(RELEASE_TARGETS); do \
		goos="$${target%/*}"; \
		goarch="$${target#*/}"; \
		ext=""; \
		if [ "$$goos" = "windows" ]; then ext=".exe"; fi; \
		out="dist/$(BINARY)_$${goos}_$${goarch}$$ext"; \
		echo "building $$target -> $$out"; \
		cc_bin="$$CC"; \
		if [ -z "$$cc_bin" ] && [ "$$goos/$$goarch" != "$$host_os/$$host_arch" ]; then \
			case "$$goos/$$goarch" in \
				linux/arm64) cc_bin="$${CC_LINUX_ARM64:-aarch64-linux-gnu-gcc}" ;; \
				windows/amd64) cc_bin="$${CC_WINDOWS_AMD64:-x86_64-w64-mingw32-gcc}" ;; \
				darwin/amd64) cc_bin="$${CC_DARWIN_AMD64:-}" ;; \
				darwin/arm64) cc_bin="$${CC_DARWIN_ARM64:-}" ;; \
			esac; \
		fi; \
		if [ "$$goos" = "darwin" ] && [ "$$host_os" != "darwin" ] && [ -z "$$cc_bin" ]; then \
			echo "error: $$target cgo build requires macOS runner or CC_DARWIN_* toolchain override"; \
			exit 1; \
		fi; \
		if [ -n "$$cc_bin" ] && ! command -v "$$cc_bin" >/dev/null 2>&1; then \
			echo "error: C compiler '$$cc_bin' not found for $$target"; \
			echo "hint: install it or set CC/CC_<OS>_<ARCH> before running make release"; \
			exit 1; \
		fi; \
		if [ -n "$$cc_bin" ]; then \
			GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED=1 CC="$$cc_bin" \
				$(GO) build -ldflags "-X github.com/polimero-app/cli/cmd.Version=$(VERSION)" -o "$$out" .; \
		else \
			GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED=1 \
				$(GO) build -ldflags "-X github.com/polimero-app/cli/cmd.Version=$(VERSION)" -o "$$out" .; \
		fi; \
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
