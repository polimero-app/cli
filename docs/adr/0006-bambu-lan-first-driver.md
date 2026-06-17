# ADR 0006: Bambu LAN First Driver

## Status

Accepted

## Context

Polimero needs a first real driver to validate the driver architecture and command contracts. Bambu printers are common, security-sensitive network devices with local LAN/developer-mode behavior that has changed publicly over time.

## Decision

The first real driver target is Bambu LAN.

Initial scope:

- X1, P1, and A1 families.
- LAN access code authentication only.
- Read-only status query first.
- TLS Trust On First Use per ADR 0007.
- Capability-gated behavior rather than hardcoded firmware assumptions.
- No Bambu cloud credentials.
- No Bambu cloud APIs.
- No bypass of authorization controls.

Initial command support:

- `printer add` (mandatory TLS connection, TOFU fingerprint)
- `status`
- `printer tls refresh`

Later Bambu LAN file listing, download, and upload behavior is covered by ADR 0009 and the Bambu LAN driver spec. File upload stores files only and does not authorize print start.

Implementation may use official documentation, user-owned device observation, and compatible public OSS references with attribution and license review.

Transport certificate handling is specified in ADR 0007. There must be no silent insecure TLS fallback.

## Consequences

- Bambu cloud behavior is intentionally out of scope.
- Some printer or firmware versions may return unsupported-capability errors.
- Protocol research must be reviewed for legal, license, and security compatibility.
