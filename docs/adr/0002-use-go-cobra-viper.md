# ADR 0002: Use Go, Cobra, and yaml.v3

## Status

Accepted

## Context

Polimero must be a cross-platform CLI for Linux, macOS, and Windows. It needs stable binary distribution, predictable command parsing, clear help output, configuration loading, and a strong testing story.

## Decision

Polimero will be implemented in Go.

The Go module path is:

```text
github.com/polimero-app/cli
```

The CLI stack is:

- Cobra for commands, flags, help, and command dispatch.
- `gopkg.in/yaml.v3` for config file parsing (see note below).
- `log/slog` (Go standard library) for structured logging throughout all packages.
- Go standard library primitives for context propagation, timeouts, and error wrapping.

All blocking operations, including network calls and secret-store access, must accept `context.Context`.

**Note on config loading:** Viper was initially considered for non-secret configuration loading. It was not adopted because Polimero's config has a single explicit file path, a strict schema version field that must be rejected if wrong, and a `POLIMERO_CONFIG_DIR` env var for test isolation. Viper's global state and implicit env-var merging would complicate version enforcement and test isolation without providing meaningful benefit. Raw `gopkg.in/yaml.v3` with `internal/config.Open()` gives direct control over parsing and error handling.

## Consequences

- Cross-platform binary builds are straightforward.
- Command behavior can be unit tested without shelling out.
- Config loading is explicit and version-gated; schema version changes are enforced at `Open()` time.
- Care is required to keep Cobra command packages thin and driver-neutral.
- `log/slog` adds no third-party dependency and is available on all target platforms.

