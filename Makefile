.PHONY: help build release install clean fmt test coverage test-race lint govulncheck ci

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
		out="dist/$(BINARY)-$${goos}-$${goarch}$$ext"; \
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
		pkgconf_bin="$$PKG_CONFIG"; \
		if [ -z "$$pkgconf_bin" ] && [ "$$goos/$$goarch" != "$$host_os/$$host_arch" ]; then \
			case "$$goos/$$goarch" in \
				linux/arm64) pkgconf_bin="$${PKG_CONFIG_LINUX_ARM64:-aarch64-linux-gnu-pkg-config}" ;; \
				windows/amd64) pkgconf_bin="$${PKG_CONFIG_WINDOWS_AMD64:-x86_64-w64-mingw32-pkg-config}" ;; \
				darwin/amd64) pkgconf_bin="$${PKG_CONFIG_DARWIN_AMD64:-pkg-config}" ;; \
				darwin/arm64) pkgconf_bin="$${PKG_CONFIG_DARWIN_ARM64:-pkg-config}" ;; \
			esac; \
		fi; \
		if [ -z "$$pkgconf_bin" ]; then pkgconf_bin="pkg-config"; fi; \
		if [ "$$pkgconf_bin" != "pkg-config" ] && ! command -v "$$pkgconf_bin" >/dev/null 2>&1; then \
			pkgconf_bin="pkg-config"; \
		fi; \
		if [ -n "$$cc_bin" ] && ! command -v "$$cc_bin" >/dev/null 2>&1; then \
			echo "error: C compiler '$$cc_bin' not found for $$target"; \
			echo "hint: install it or set CC/CC_<OS>_<ARCH> before running make release"; \
			exit 1; \
		fi; \
		if ! command -v "$$pkgconf_bin" >/dev/null 2>&1; then \
			echo "error: pkg-config tool '$$pkgconf_bin' not found for $$target"; \
			echo "hint: install it or set PKG_CONFIG/PKG_CONFIG_<OS>_<ARCH> before running make release"; \
			exit 1; \
		fi; \
		if ! "$$pkgconf_bin" --exists libavcodec libavutil libswscale; then \
			echo "error: missing FFmpeg dev packages for $$target (libavcodec/libavutil/libswscale)"; \
			echo "hint: install target FFmpeg headers and .pc files, and set PKG_CONFIG_* if cross-building"; \
			exit 1; \
		fi; \
		if [ -n "$$cc_bin" ] && [ "$$goos/$$goarch" != "$$host_os/$$host_arch" ]; then \
			ff_flags="$$( "$$pkgconf_bin" --cflags --libs libavcodec libavutil libswscale )"; \
			tmp_src="dist/.ffmpeg-check-$${goos}-$${goarch}.c"; \
			tmp_bin="dist/.ffmpeg-check-$${goos}-$${goarch}"; \
			printf '#include <libavcodec/avcodec.h>\nint main(void){return 0;}\n' > "$$tmp_src"; \
			if ! "$$cc_bin" "$$tmp_src" $$ff_flags -o "$$tmp_bin" >/dev/null 2>&1; then \
				echo "error: FFmpeg headers/libs are not usable for $$target with CC=$$cc_bin"; \
				echo "hint: install target-arch FFmpeg dev packages and point PKG_CONFIG_* to their .pc files"; \
				exit 1; \
			fi; \
			rm -f "$$tmp_src" "$$tmp_bin"; \
		fi; \
		if [ -n "$$cc_bin" ]; then \
			GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED=1 CC="$$cc_bin" PKG_CONFIG="$$pkgconf_bin" \
				$(GO) build -ldflags "-X github.com/polimero-app/cli/cmd.Version=$(VERSION)" -o "$$out" .; \
		else \
			GOOS="$$goos" GOARCH="$$goarch" CGO_ENABLED=1 PKG_CONFIG="$$pkgconf_bin" \
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

coverage: ## Run tests with coverage profile (output: coverage.out)
	@packages="$$( $(GO) list ./... )"; \
	if [ -n "$$packages" ]; then \
		$(GO) test -coverprofile=coverage.out $$packages && $(GO) tool cover -func=coverage.out; \
	else \
		echo "no Go packages yet; skipping coverage"; \
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

govulncheck: ## Run govulncheck on all Go packages
	@packages="$$( $(GO) list ./... )"; \
	if [ -n "$$packages" ]; then \
		$(GO) run golang.org/x/vuln/cmd/govulncheck@latest $$packages; \
	else \
		echo "no Go packages yet; skipping govulncheck"; \
	fi

ci: fmt test test-race lint ## Run full CI suite (fmt + test + test-race + lint)
