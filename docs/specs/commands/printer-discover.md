# Command Spec: `printer discover`

## Status

Accepted

## Purpose

Scan the local network using mDNS/DNS-SD to find printers supported by installed drivers. Reports each discovered printer with its host, serial, model, and friendly name, and indicates which existing profile (if any) already corresponds to each printer.

## Syntax

```text
polimero printer discover [--driver <driver>] [--timeout <duration>] [--output <format>]
```

## Flags

- `--driver <driver>`: optional. Restrict discovery to a single driver. If omitted, all registered drivers that declare `Discovery: true` are queried. Returns exit code `5` when the named driver does not support discovery.
- `--timeout <duration>`: optional. How long to listen for mDNS responses. Default: `5s`. Must parse as a Go duration and be greater than zero.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Behavior

- Opens an mDNS listener on the local network and browses for supported printer service types until the timeout elapses.
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
- `4`: network error (mDNS socket cannot be opened).
- `5`: named driver does not support discovery.

## Error Cases

- Unknown `--driver` value.
- Named driver does not support discovery.
- Invalid `--timeout` format.
- Zero or negative `--timeout`.
- Config directory or file unreadable.
- mDNS socket cannot be opened.

## Security Requirements

- Do not connect to discovered printers.
- Do not capture or log TLS data, access codes, or any secrets.
- Sanitize all network and mDNS errors before display.

## Test Scenarios

- Returns list with serial, model, name, host for each discovered printer.
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
- Discovery protocols other than mDNS/DNS-SD.
