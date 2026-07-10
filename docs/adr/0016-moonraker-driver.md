# ADR 0016: Moonraker Driver

## Status

Accepted

## Context

Polimero already has a stable, driver-neutral command surface and one production
driver (`bambu-lan`). Many Klipper-based printers (including Sonic Pad and
Nebula deployments) expose Moonraker over LAN HTTP APIs and can map to the same
portable operations.

The CLI needs a second concrete driver that reuses existing commands instead of
adding new command families.

## Decision

Add a `moonraker` driver that implements the existing command contracts for:

- `status`
- `jobs start|pause|resume|cancel`
- `temperature set`
- `motion home|jog`
- `files roots|list|download|upload`

The driver name is exactly `moonraker`.

`printer add` supports `--driver moonraker` and stores the Moonraker API key in
the existing keychain access-code slot (`moonraker:<name>:access-code`).

Moonraker transport is HTTP(S). TLS fingerprint pinning and `printer tls refresh`
remain unsupported for this driver.

## Consequences

- No new top-level commands are introduced.
- Command behavior stays driver-neutral; capability checks gate unsupported
  operations.
- `moonraker` profiles do not require `serial`.
- `moonraker` does not expose discovery or camera features in this slice.
- Existing security rules still apply: keychain-only secrets, bounded timeouts,
  sanitized errors, no secret logging, and no protocol auth payload leakage.
