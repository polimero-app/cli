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
- Protocol trace files.

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

- Job, temperature, and motion commands (ADR 0012) may heat, move, print, pause, cancel, or resume unexpectedly.
- Automation may invoke state-changing commands without confirmation.
- A temperature or motion command sent while a print is in progress could ruin the job or crash the toolhead into the part or bed.
- An out-of-range temperature or jog request could cause thermal or mechanical damage if sent unchecked.
- A command may appear to succeed without the printer actually reaching the requested state.

Controls:

- `jobs`, `temperature`, and `motion` commands (ADR 0012) require interactive confirmation by default (`tty.Prompter`); a non-interactive session without an explicit `--yes` flag fails closed.
- Each command validates the printer's current state before dispatch and refuses to run when the precondition is not met (e.g. `temperature set` and `motion home`/`jog` require `idle`; `jobs pause` requires `printing`).
- Temperature and jog-distance values are checked against generic safety bounds before any network call, independent of firmware-side limits.
- Commands wait for driver-confirmed completion (resulting state, acknowledged target, or motion-finished signal) rather than reporting success once a command is merely sent; a contradicting end-state is reported as a failure.
- `jobs start` only starts a print from a file already on printer storage; it cannot be used to implicitly upload and run an arbitrary local file.

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

### Protocol Trace Disclosure

Risks:

- Trace files may aggregate printer host names, serial numbers, job names, file names, paths, parser warnings, and protocol-phase metadata.
- A trace implementation could accidentally capture raw MQTT, FTP, discovery, camera, or authentication payloads.
- A trace path could overwrite an existing local file if opened unsafely.
- Automation might publish trace files to issue trackers without realizing they contain operational metadata.

Controls:

- Protocol tracing is opt-in through `--protocol-trace <file>` on commands that perform printer protocol work.
- Trace files are created as new files only and must not overwrite existing paths.
- POSIX-like implementations create trace files with owner-only read/write permissions (`0600`).
- Trace events contain sanitized summaries, parser decisions, response key inventories, byte counts, and error categories, and may include secret-free protocol payloads (MQTT command and report JSON, discovery records) per ADR 0013; binary payloads such as camera frames and file contents are never included.
- Access codes, passwords, raw auth payloads, TLS private material, transferred file contents, and camera frames are forbidden in trace files.
- Trace contents are never embedded in human output, JSON output, logs, or errors.

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
