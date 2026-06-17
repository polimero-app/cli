# ADR 0005: Configuration and Secrets

## Status

Accepted

## Context

Polimero needs named printer profiles for repeated use. Profiles include non-secret fields such as driver name, host, and timeout. Profiles also need credentials such as a Bambu LAN access code.

The project targets Linux, macOS, and Windows, so config and secret handling must respect platform conventions.

## Decision

Printer profiles are identified by user-defined names. Profile names are case-insensitive. Input is normalized to lowercase before validation and storage. `Garage-X1C` and `garage-x1c` refer to the same profile.

Non-secret configuration:

- Stored as versioned YAML at `polimero/polimero.yaml` under `os.UserConfigDir`.
- For example: `~/.config/polimero/polimero.yaml` on Linux.
- Parsed and written directly with `gopkg.in/yaml.v3` through Polimero's config package.
- Must not contain access codes, tokens, passwords, or other secrets.

Config file structure:

```yaml
version: 1
profiles:
  <name>:
    driver: <driver>
    host: <host>
    timeout: <duration>
    insecure: <bool>
    created: <RFC3339>
    updated: <RFC3339>
```

Secrets:

- Stored in OS keychain facilities.
- macOS uses Keychain.
- Windows uses Credential Manager.
- Linux uses Secret Service where available.
- If OS keychain storage is unavailable, Polimero fails closed.
- Environment-variable secret injection is allowed only in test or integration builds, or explicit test mode.

Keychain naming scheme:

- Service: `polimero`
- Account for the printer access code: `<driver>:<name>:access-code`
- Account for the TLS fingerprint: `<driver>:<name>:tls-fingerprint`

Example accounts for a profile named `garage-x1c` with driver `bambu-lan`:

- `bambu-lan:garage-x1c:access-code`
- `bambu-lan:garage-x1c:tls-fingerprint`

Secret input:

- Interactive terminal users may use a hidden TTY prompt.
- Automation may use `--access-code-file`.
- Secret values must never be accepted through ordinary command-line flags such as `--access-code`.

## Consequences

- Headless systems without keychain support need explicit setup before storing real printer secrets.
- CI must use mocks or test-mode secret injection.
- Shell history and process-list leakage from secret flags is avoided.
- Each profile has up to two keychain entries; rollback must clean up both on partial failure.
