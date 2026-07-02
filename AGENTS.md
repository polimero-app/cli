# AGENTS.md

Instructions for agents working in this repository.

## Project Context

Polimero is a greenfield Go CLI for interacting with 3D printers through brand-specific drivers behind a stable command surface.

Current phase: printer-management, network read, and control command slices are implemented (`printer add`, `printer list`, `printer remove`, `printer drivers`, `printer discover`, `printer tls refresh`, `status`, `camera stream`, `camera snapshot`, `files roots`, `files list`, `files download`, `files upload`, `jobs start`, `jobs pause`, `jobs resume`, `jobs cancel`, `temperature set`, `motion home`, `motion jog`), plus the `--trace` protocol diagnostics flag. Auxiliary control specs (fans, lights, speed) are accepted but not yet implemented. Keep using accepted ADRs and specs to authorize new behavior before implementation.

Primary references:

- `PLAN.md`
- `docs/adr/`
- `docs/specs/commands/`
- `docs/specs/drivers/`
- `docs/security/`

## Core Rules

- Treat security as the highest-priority quality attribute.
- Preserve the ADR/spec-driven workflow.
- Keep documentation up to date with behavior changes in the same change set.
- Do not implement a command unless its command spec exists and is accepted.
- Do not add architecture-significant behavior unless an accepted ADR covers it.
- Keep command behavior driver-neutral unless a spec explicitly defines a brand-specific extension.
- Keep Bambu-specific details behind the Bambu driver boundary.
- Do not introduce cloud credentials, cloud APIs, or authorization bypass behavior.

## Stack And Repository Decisions

- Language: Go.
- Module path: `github.com/polimero-app/cli`.
- CLI stack: Cobra.
- License: AGPL-3.0-only.
- Config format: versioned YAML loaded with `gopkg.in/yaml.v3` under `os.UserConfigDir`.
- Secret storage: OS keychain first.
- First driver: Bambu LAN.
- First read command: `polimero status <name>`.

## Security Requirements

- Never log secrets.
- Never store secrets in YAML config.
- Never expose secrets in human output, JSON output, errors, tests, or fixtures.
- Do not add command-line secret flags such as `--access-code`.
- Use hidden TTY prompts or strict `--access-code-file` handling for secret input.
- Fail closed if OS keychain storage is unavailable.
- Use bounded timeouts for network work.
- Do not retry read-only network commands by default.
- Do not perform discovery or network scanning during ordinary commands.
- Do not silently downgrade transport security.
- Sanitize all user-facing errors.
- Redact debug logs; debug mode is not permission to print protocol secrets or auth payloads.

## Command Contract Rules

All user-facing commands must support:

- Human-readable default output.
- `--output json`.
- Stable JSON envelope:

```json
{
  "ok": true,
  "data": {},
  "error": null,
  "meta": {}
}
```

Error JSON must use:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "error_code",
    "message": "sanitized message",
    "details": {}
  },
  "meta": {}
}
```

Stable exit codes:

- `0`: success
- `1`: general failure
- `2`: usage/config error
- `3`: auth/secret error
- `4`: network/timeout error
- `5`: unsupported capability

## Driver Rules

- CLI packages depend on driver-neutral interfaces.
- Drivers expose capabilities.
- Unsupported behavior returns an unsupported-capability error.
- All blocking driver operations accept `context.Context`.
- Drivers must not read config files directly.
- Drivers must not persist secrets.
- Drivers must not return raw protocol payloads in errors.
- Hardware integration tests must be opt-in and build-tagged.

## Bambu LAN Rules

Initial Bambu scope:

- X1, P1, A1, and H2 families.
- LAN access code only.
- Implemented commands: discovery, TLS refresh, read-only status, camera streaming, camera snapshot, file management (roots, list, download, upload), job control (start, pause, resume, cancel), temperature targets, and motion (home, jog).
- Capability-gated behavior.

Out of scope unless a later accepted ADR/spec says otherwise:

- Bambu cloud auth.
- Bambu cloud APIs.
- Combined upload-and-start in a single command (`jobs start` only starts files already on printer storage; ADR 0012).
- Authorization bypass.

Protocol research may use official docs, user-owned device observations, and compatible public OSS references with attribution and license review.

## Testing Expectations

Before implementation changes are considered complete:

- Run `make ci`.
- Run relevant narrower checks while iterating, such as `make test`, `make test-race`, and `make lint`.
- Do not commit code before tests and lint pass, unless the commit message and handoff explicitly document why verification could not be completed.
- Add or update unit tests for command parsing, config, validation, output, errors, secret-store abstraction, and driver dispatch.
- Add contract tests for driver behavior.
- Use mock transports and fixtures for default CI.
- Keep real-printer tests behind explicit integration tags.
- Run race tests for code touching network or concurrency.

Some changes may be documentation-only. The Makefile also handles repository states with no Go packages by skipping Go package checks.

## Editing Guidance

- Keep changes scoped to the requested ADR, spec, command, or driver area.
- Do not refactor unrelated docs or future implementation plans while working on one command.
- Prefer precise contracts over vague implementation notes.
- When implementation behavior changes, update affected ADRs, command specs, driver specs, security docs, README content, and examples in the same logical change.
- When adding dependencies later, document purpose, license compatibility, maintenance status, and vulnerability/audit posture.
- Use ASCII unless a file already requires non-ASCII content.

## Commit Guidance

- Use atomic commits: each commit should represent one coherent logical change.
- Do not mix unrelated refactors, formatting, documentation updates, and feature work unless they are required for the same logical change.
- Keep ADR/spec changes in the same commit as the implementation they authorize when they are part of one behavior change.
- Commit messages should clearly identify the affected command, driver, ADR, spec, or security area.
- Before committing code, run the relevant verification targets and include any known skipped checks or environment limitations in the handoff.
