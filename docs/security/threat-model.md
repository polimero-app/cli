# Threat Model

## Scope

This threat model covers the Polimero CLI, local configuration, local secret storage, printer network communication, driver adapters, logs, and command output.

Cloud service integrations are out of scope for the first implementation.

## Assets

- Printer credentials, including Bambu LAN access codes.
- Printer network addresses.
- Printer operational state.
- Job names and metadata.
- Device file names, paths, sizes, and timestamps.
- Device file contents transferred by explicit upload or download commands.
- User trust in command output.
- Local config integrity.
- Local logs.

## Trust Boundaries

- User shell to Polimero process.
- Polimero process to config file.
- Polimero process to OS keychain.
- Polimero process to local filesystem.
- Polimero process to local network printer.
- Driver-neutral command layer to brand-specific driver.
- CLI output to humans and automation.

## Primary Threats

### Secret Exposure

Risks:

- Secret in shell history.
- Secret in process arguments.
- Secret in YAML config.
- Secret in logs.
- Secret in JSON output.
- Secret in errors.

Controls:

- No `--access-code` flag.
- Hidden TTY prompt or strict `--access-code-file`.
- OS keychain storage.
- Redacted structured logging.
- Sanitized error contract.

### Unsafe Device Control

Risks:

- Future commands may heat, move, print, pause, or cancel unexpectedly.
- Automation may invoke state-changing commands without confirmation.

Controls:

- First slice is read-only.
- Future dangerous commands require exact confirmation behavior in specs.
- Non-interactive bypass flags must be explicit.

### Network Misuse

Risks:

- Accidental network scanning.
- Connecting to the wrong device.
- Hanging network calls.
- Insecure transport downgrade.

Controls:

- Named profiles.
- Explicit discovery only.
- Default 10 second timeout.
- No retry by default.
- No silent insecure TLS fallback.

### Driver Boundary Violations

Risks:

- Brand-specific assumptions leak into generic command behavior.
- Unsupported device features are treated as generic failures.

Controls:

- Driver-neutral contract.
- Capability metadata.
- Typed unsupported-capability errors.
- Contract tests.

### Device File Metadata Exposure

Risks:

- Device filenames may reveal customer names, model names, or production details.
- Malicious or malformed device filenames may inject terminal control sequences.
- Recursive listing may expose more metadata than intended when explicitly requested.

Controls:

- File listing follows explicit `files list` arguments.
- Human output sanitizes control characters from file names and paths.
- File names and paths are not secrets and may appear in output and logs.
- File contents must not be logged.
- Delete, rename, directory creation, and print-start behavior require separate ADRs and specs.

### Device File Transfer Misuse

Risks:

- Uploading an unintended file to printer storage.
- Downloading a device file over an existing local file.
- Treating upload as implicit authorization to start printing.
- Logging transferred file contents or credentials.

Controls:

- Upload reads only the local source path requested by the user.
- Download does not overwrite local files unless `--overwrite` is set.
- Upload stores files only and must not start prints.
- Access codes and transferred file contents are never logged.

### Supply Chain Risk

Risks:

- Incompatible OSS code or protocol snippets.
- Vulnerable dependencies.
- Unmaintained packages.

Controls:

- Dependency allowlist and audit policy.
- License compatibility check.
- Attribution for OSS protocol references.
- Vulnerability scanning in CI.

## Non-goals For First Slice

- Cloud account security.
- Multi-user RBAC.
- Remote service API hardening.
- Fleet management.
- Printer firmware security assessment.
