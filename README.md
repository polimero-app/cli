# Polimero CLI

Polimero is a command line interface for interacting with 3D printers through brand-specific drivers behind a common command surface.

The project is intentionally ADR and spec driven. Initial commands are implemented, and new behavior should continue to land incrementally, one command at a time, after the relevant ADRs and command specs are accepted.

## Current Status

This repository contains the ADR/spec foundation plus the first implemented printer-management and read-command slices.

Implemented commands currently include `printer add`, `printer list`, `printer remove`, `printer drivers`, `printer discover`, `printer status`, and `printer tls refresh`.

## Key Decisions

- Language: Go
- Module: `github.com/polimero-app/cli`
- CLI stack: Cobra
- License: AGPL-3.0-only
- First driver: Bambu LAN
- First read command: `polimero printer status <name>`
- Secret storage: OS keychain first
- Config storage: versioned YAML under `os.UserConfigDir`

## Global Flags

- `--output <format>`: output format. Values: `human`, `json`. Default: `human`.
- `--verbose`, `-v`: show detailed progress output in human mode. Verbose lines are suppressed when `--output json` is used.

## Documentation

- [PLAN.md](PLAN.md)
- [ADRs](docs/adr)
- [Command specs](docs/specs/commands)
- [Driver specs](docs/specs/drivers)
- [Security docs](docs/security)

## Development

Expected verification targets:

```sh
make test
make test-race
make lint
make ci
```

`make ci` runs tests, race tests, and linting when the relevant tools are available.
