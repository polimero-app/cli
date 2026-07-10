# Polimero CLI Plan

## Summary

Polimero is a greenfield command line interface for interacting with 3D printers through brand-specific drivers behind a common command surface. The project continues to grow incrementally, one command at a time, with ADR and spec driven development authorizing new behavior before code lands.

The implementation stack is Go with Cobra for CLI structure and `gopkg.in/yaml.v3` for non-secret configuration. Security is the highest-priority quality attribute. The first real printer integration is Bambu LAN for X1, P1, A1, and H2 families, using LAN access code authentication only.

## Project Decisions

- CLI binary name: `polimero`.
- Go module path: `github.com/polimero-app/cli`.
- License: `AGPL-3.0-only`.
- Stack: Go, Cobra, yaml.v3.
- Supported operating systems: Linux, macOS, and Windows.
- Development model: ADR and command spec required before implementation.
- First driver: Bambu LAN.
- First read command: `polimero status <name>`.
- First profile commands: `printer add`, `printer list`, and `printer remove`.
- Implemented command set: `printer add`, `printer list`, `printer remove`, `printer drivers`, `printer discover`, `printer tls refresh`, `status`, `camera stream`, `camera snapshot`, `files roots`, `files list`, `files download`, `files upload`, `jobs start`, `jobs pause`, `jobs resume`, `jobs cancel`, `temperature set`, `motion home`, `motion jog`.
- Implemented drivers: `bambu-lan`, `moonraker`.
- Config format: versioned YAML at `polimero/polimero.yaml` under `os.UserConfigDir`; profiles stored as a map keyed by name.
- Secret storage: OS keychain first; fail closed if unavailable.
- Keychain naming scheme: service `polimero`; accounts `<driver>:<name>:access-code` and `<driver>:<name>:tls-fingerprint`.
- TLS policy: Trust On First Use (TOFU) per ADR 0007; `--insecure` flag available on `printer add`, `status`, and `printer tls refresh`; fingerprint stored in OS keychain.
- Output: human-readable default plus stable JSON envelope through `--output json`; `--output` is a global persistent flag on the root command.
- `durationMs` in JSON meta: network commands only.
- Discovery: explicit opt-in only; implemented as `printer discover`.

## Planned Future Drivers

The following drivers are planned but not part of the first implementation slice. Each requires its own ADR and driver spec before implementation begins.

### Creality LAN

Target families: K1, K1 Max, K1C, K2 Plus.

Transport: HTTP-based LAN API (Creality Print protocol). No TLS in typical LAN mode; transport security requirements will be defined in the driver spec.

## Repository Foundation

The initial repository should contain:

- `README.md`
- `LICENSE`
- `.gitignore`
- `go.mod`
- `Makefile`
- `.github/workflows/ci.yml`
- `docs/adr/`
- `docs/specs/commands/`
- `docs/specs/drivers/`
- `docs/security/`

Printer-control commands (jobs, temperature, motion) are authorized by ADR 0012 and their respective command specs. Auxiliary controls (fans, lights, speed) are authorized by ADR 0014 and their respective command specs.

## ADR Workflow

ADR status values are:

- `Proposed`
- `Accepted`
- `Superseded`

Every ADR must include:

- Context
- Decision
- Consequences
- Status

Every command implementation must reference either a dedicated accepted ADR or an accepted ADR that already covers the decision.

## Command Spec Workflow

Each command spec must include:

- Purpose
- Syntax
- Arguments and flags
- Configuration requirements
- Secret requirements
- Output contract
- Exit codes
- Error cases
- Security requirements
- Test scenarios
- Non-goals

Specs must be precise enough for tests to be written before command implementation.

## Security Baseline

- Never log secrets.
- Never store secrets in config files.
- Sanitize all user-facing errors.
- Use bounded network timeouts.
- Use no retries by default for read-only network commands.
- Fail closed if the OS keychain is unavailable.
- Do not perform automatic network discovery during ordinary commands.
- Require explicit confirmation behavior in specs for future state-changing commands.
- Do not support cloud credentials in the first Bambu LAN slice.

## Testing Baseline

Default CI must not require physical printer hardware.

Planned test layers:

- Unit tests for command parsing, config, validation, output, errors, and driver dispatch.
- Contract tests shared by all drivers.
- Mock transport and protocol fixture tests for Bambu LAN.
- Opt-in build-tagged hardware integration tests.
- Race detector for network and concurrency paths.
- Cross-platform CI on Linux, macOS, and Windows.

Expected local targets:

- `make test`
- `make test-race`
- `make lint`
- `make ci`

## Documentation Set

Initial ADRs:

- `docs/adr/0001-use-adr-and-spec-driven-development.md`
- `docs/adr/0002-use-go-cobra-viper.md`
- `docs/adr/0003-driver-adapter-architecture.md`
- `docs/adr/0004-security-baseline.md`
- `docs/adr/0005-configuration-and-secrets.md`
- `docs/adr/0006-bambu-lan-first-driver.md`
- `docs/adr/0007-tls-trust-on-first-use.md`
- `docs/adr/0008-top-level-action-commands.md`
- `docs/adr/0009-device-file-management-commands.md`
- `docs/adr/0010-promote-status-to-top-level.md`
- `docs/adr/0011-extended-read-only-status.md`
- `docs/adr/0012-printer-control-commands.md`
- `docs/adr/0013-protocol-trace-diagnostics.md`
- `docs/adr/0014-auxiliary-printer-controls.md`
- `docs/adr/0016-moonraker-driver.md`

Command specs:

- `docs/specs/commands/printer-add.md`
- `docs/specs/commands/printer-drivers.md`
- `docs/specs/commands/printer-discover.md`
- `docs/specs/commands/printer-list.md`
- `docs/specs/commands/printer-remove.md`
- `docs/specs/commands/printer-tls-refresh.md`
- `docs/specs/commands/printer-status.md` (superseded by `status.md`)
- `docs/specs/commands/status.md`
- `docs/specs/commands/camera-stream.md`
- `docs/specs/commands/camera-snapshot.md`
- `docs/specs/commands/files.md`
- `docs/specs/commands/jobs.md`
- `docs/specs/commands/temperature.md`
- `docs/specs/commands/motion.md`
- `docs/specs/commands/fans.md`
- `docs/specs/commands/lights.md`
- `docs/specs/commands/speed.md`

Driver and security docs:

- `docs/specs/drivers/driver-contract.md`
- `docs/specs/drivers/bambu-lan.md`
- `docs/specs/drivers/moonraker.md`
- `docs/security/threat-model.md`
- `docs/security/secret-handling.md`
