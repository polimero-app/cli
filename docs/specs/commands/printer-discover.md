# Command Spec: `printer discover`

## Status

Accepted

## Purpose

Scan the local network using driver-supported local discovery protocols to find printers supported by installed drivers. For Bambu LAN this includes mDNS/DNS-SD, SSDP, and passive UDP broadcast. Reports each discovered printer with its host, serial, model, and friendly name, and indicates which existing profile (if any) already corresponds to each printer.

## Syntax

```text
polimero printer discover [--driver <driver>] [--timeout <duration>] [--output <format>]
```

## Flags

- `--driver <driver>`: optional. Restrict discovery to a single driver. If omitted, all registered drivers that declare `Discovery: true` are queried. Returns exit code `5` when the named driver does not support discovery.
- `--timeout <duration>`: optional. How long to wait for discovery results across the enabled protocols. Default: `5s`. Must parse as a Go duration and be greater than zero.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Behavior

- Runs each selected driver's supported local-network discovery methods until the timeout elapses. For Bambu LAN this is mDNS/DNS-SD browsing, SSDP discovery, and passive UDP broadcast listening.
- May return results from the remaining discovery methods when one protocol fails; returns exit code `4` only when discovery cannot be started for the selected driver(s).
- Cross-references each discovered printer's serial against existing profiles. If a profile's `serial` field matches, the corresponding profile name is included in the output.
- Returns exit code `0` even when zero printers are found.
- If `--driver` is given and the driver does not support discovery, fails with exit code `5`.
- If `--driver` names an unknown driver, fails with exit code `2`.
- Does not connect to printers during discovery. No TLS, MQTT, or access-code use.

## Output

Human success (printers found):

```text
Discovered 2 Bambu printer(s) on the local network (3.1s):

  NAME         SERIAL            MODEL  HOST           CONFIGURED
  My X1C       01S09C450100XXX   X1C    192.0.2.10     garage-x1c
  P1S          01P09A310200XXX   P1S    192.0.2.11     —
```

Human success (none found):

```text
No printers found on the local network (5.0s).
```

JSON success:

```json
{
  "ok": true,
  "data": {
    "printers": [
      {
        "driver": "bambu-lan",
        "host": "192.0.2.10",
        "serial": "01S09C450100XXX",
        "model": "X1C",
        "name": "My X1C",
        "configuredAs": "garage-x1c"
      },
      {
        "driver": "bambu-lan",
        "host": "192.0.2.11",
        "serial": "01P09A310200XXX",
        "model": "P1S",
        "name": "P1S",
        "configuredAs": null
      }
    ],
    "count": 2
  },
  "error": null,
  "meta": {
    "command": "printer discover",
    "durationMs": 3127
  }
}
```

JSON success (none found):

```json
{
  "ok": true,
  "data": {
    "printers": [],
    "count": 0
  },
  "error": null,
  "meta": {
    "command": "printer discover",
    "durationMs": 5001
  }
}
```

JSON error:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "capability_unsupported",
    "message": "driver \"creality-lan\" does not support discovery"
  },
  "meta": {
    "command": "printer discover"
  }
}
```

## Exit Codes

- `0`: scan complete (including zero results).
- `1`: general failure.
- `2`: usage or config error (invalid timeout, unknown driver, config load failure).
- `4`: network error (discovery listeners or probes cannot be started).
- `5`: named driver does not support discovery.

## Error Cases

- Unknown `--driver` value.
- Named driver does not support discovery.
- Invalid `--timeout` format.
- Zero or negative `--timeout`.
- Config directory or file unreadable.
- Discovery listeners or probes cannot be started.

## Security Requirements

- Do not connect to discovered printers.
- Do not capture or log TLS data, access codes, or any secrets.
- Human-readable output must sanitize control characters from unauthenticated discovery fields before writing to a terminal.
- Discovery transport errors must be sanitized before human or JSON output.
- Sanitize all network and discovery errors before display.

## Test Scenarios

- Returns list with serial, model, name, host for each discovered printer.
- Aggregates results from all supported discovery protocols for the selected driver.
- Sets `configuredAs` to profile name when serial matches an existing profile.
- Sets `configuredAs` to null/`—` when serial has no matching profile.
- Returns empty list (exit 0) when no printers found.
- Fails with exit 5 when named driver does not support discovery.
- Fails with exit 2 when unknown driver specified.
- Fails with exit 2 when timeout is invalid or zero.
- Emits stable JSON envelope for success and failure.
- `printers` serializes as `[]` (not `null`) when empty.
- JSON meta includes `durationMs`.

## Non-goals

- Connecting to discovered printers.
- Automatically adding discovered printers as profiles.
- WAN or cloud-based discovery.
- Discovery protocols beyond the driver-documented local-network methods.
