.PHONY: test test-race lint ci

GO ?= go

test:
	@packages="$$( $(GO) list ./... )"; \
	if [ -n "$$packages" ]; then \
		$(GO) test $$packages; \
	else \
		echo "no Go packages yet; skipping tests"; \
	fi

test-race:
	@packages="$$( $(GO) list ./... )"; \
	if [ -n "$$packages" ]; then \
		$(GO) test -race $$packages; \
	else \
		echo "no Go packages yet; skipping race tests"; \
	fi

lint:
	@if [ -z "$$( $(GO) list ./... 2>/dev/null )" ]; then \
		echo "no Go packages yet; skipping lint"; \
	elif command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed; skipping lint"; \
	fi

ci: test test-race lint
