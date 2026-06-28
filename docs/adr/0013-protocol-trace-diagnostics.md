# ADR 0013: Protocol Trace Diagnostics

## Status

Accepted

## Context

Polimero's driver boundary intentionally hides brand-specific protocol details from the stable command surface. That protects users from raw MQTT, FTP, camera, TLS, and discovery payloads leaking into ordinary output, but it makes protocol debugging difficult when a printer family or firmware version reports data in a slightly different shape.

The Bambu H2 family already exposed this problem: a status command could connect successfully and parse most fields while silently missing a chamber temperature because the field name or JSON shape differed from earlier families. Similar issues can occur in file listings, camera protocol selection, discovery records, TLS pinning, and future control-command acknowledgments.

Existing logging is not enough for this use case. Debug logs must remain redacted and should not become raw protocol captures. Users and maintainers need an explicit, opt-in diagnostic artifact that records protocol phases, safe metadata, parser decisions, and parser warnings without exposing secrets or raw transferred content.

## Decision

Polimero will add a `--protocol-trace <file>` flag to commands that perform printer protocol work:

- `printer add`
- `printer discover`
- `printer tls refresh`
- `status`
- `camera stream`
- `camera snapshot`
- `files roots`
- `files list`
- `files download`
- `files upload`
- `jobs start`
- `jobs pause`
- `jobs resume`
- `jobs cancel`
- `temperature set`
- `motion home`
- `motion jog`

The flag writes sanitized protocol diagnostics as JSON Lines to a new local file. Each line is one event object. The trace file is a local diagnostic artifact, not part of the command's normal human or JSON data contract.

Trace events may include:

- Command, driver, operation, phase, and timestamp metadata.
- Transport names and sanitized endpoint identifiers.
- Byte counts, duration values, selected protocol names, and capability decisions.
- Response key inventories and safe scalar summaries needed to debug parser behavior.
- Parser warnings, type mismatches, fallback decisions, and unsupported-field decisions.
- Sanitized error categories.

Trace events must not include:

- Access codes, passwords, tokens, private keys, or TLS private material.
- Raw authentication payloads.
- Raw MQTT payloads, raw FTP command streams, raw discovery packets, raw camera frames, raw transferred file contents, or unredacted protocol payloads.
- Unsanitized backend, parser, TLS, FTP, MQTT, RTSP, or secret-store error text.

The command layer owns trace file lifecycle:

- The file is created before any protocol work starts.
- The path must name a new file; an existing file is not overwritten.
- On POSIX-like systems, the file is created owner-readable and owner-writable only (`0600`).
- If the file cannot be created, the command fails before protocol work with exit code `2`.
- If writing or closing the trace fails after protocol work has started, the command fails with exit code `1` unless an earlier, more specific command failure already occurred.
- When `--output json` is used and the trace file is successfully opened, `meta.protocolTracePath` may contain the user-provided trace path. The trace contents are never embedded in human or JSON output.

The command layer will make the trace sink available to drivers through `context.Context`. Driver method signatures are not changed for this feature. Drivers must treat an absent trace sink as a no-op.

Commands that do not perform printer protocol work are outside this ADR. If `--protocol-trace` is passed to a command outside this ADR's scope, that command fails as an unsupported flag or usage/config error rather than creating an empty diagnostic file.

## Consequences

- Command specs for the in-scope commands define the new flag, trace lifecycle, JSON metadata, error behavior, and redaction requirements.
- The driver contract gains a protocol-trace diagnostics section without changing driver operation signatures.
- The Bambu LAN driver spec defines trace-safe event categories for MQTT status/control, FTPS file operations, camera probing/capture, TLS refresh, and discovery.
- Security documentation explicitly treats protocol trace files as sensitive local diagnostics with stricter creation and redaction rules than ordinary output.
- Default CI must use mock transports and fixtures to verify trace redaction and parser-warning events.
- Real-printer trace collection remains opt-in and user-owned. Trace files may contain printer network addresses, printer serial numbers, file names, job names, and operational metadata even though they must not contain secrets or raw payloads.
- Full packet capture, secret-bearing traces, cloud protocol tracing, and automatic trace upload are out of scope.
