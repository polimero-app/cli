# Command Spec: `printer drivers`

## Status

Accepted

## Purpose

List available printer drivers so users can choose a valid `--driver` value before running `printer add`.

## Syntax

```text
polimero printer drivers [--output <format>]
```

## Flags

- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command must not read or write profile config.

## Secret Requirements

The command must not read or write secrets.

## Output

Drivers are listed in stable alphabetical order by name. Each entry includes a short human-readable description.

Human success example:

```text
DRIVER     DESCRIPTION
bambu-lan  Bambu Lab printers over LAN mode
moonraker  Moonraker-compatible Klipper printers
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "drivers": [
      {
        "name": "bambu-lan",
        "description": "Bambu Lab printers over LAN mode"
      },
      {
        "name": "moonraker",
        "description": "Moonraker-compatible Klipper printers"
      }
    ]
  },
  "error": null,
  "meta": {
    "command": "printer drivers"
  }
}
```

## Exit Codes

- `0`: list produced.
- `2`: invalid output format.

## Error Cases

- `--output` is not `human` or `json`.

## Security Requirements

- Do not read profile config.
- Do not read keychain secrets.
- Do not perform network discovery or printer connectivity checks.

## Test Scenarios

- Lists available drivers, including `bambu-lan` and `moonraker`, in human output.
- Lists drivers in stable alphabetical order.
- Emits stable JSON envelope.
- Rejects invalid output format with exit code `2`.

## Non-goals

- Discovering printers on the network.
- Checking whether a configured printer supports a driver.
- Showing driver capabilities beyond the stable driver name and short description.
