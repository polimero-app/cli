# Command Spec: `status`

## Status

Accepted

## Purpose

Query current status from a configured printer through the driver-neutral status contract.

This is a top-level action command that consumes printer profiles managed by `printer`, following ADR 0008 and ADR 0010.

Extended read-only telemetry is available via `--detailed`, following ADR 0011.

## Syntax

```text
polimero status <name> [--detailed] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<name>`: existing printer profile name.

## Flags

- `--detailed`: optional. Includes extended telemetry (fans, time estimates, speed, Wi-Fi, lights, print metadata, stage, timelapse, g-code position, and brand-specific extensions such as AMS). Without this flag, only summary fields are returned.
- `--timeout <duration>`: optional. Overrides profile/default timeout for this command.
- `--insecure`: optional. Skips TLS verification for this invocation regardless of the profile `insecure` setting.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol diagnostics to a new local file. The file must not already exist. Trace output may include protocol phases, response key inventories, parser fallback decisions, safe scalar summaries, byte counts, and parser warnings. It must not include access codes, TLS private material, raw auth payloads, raw MQTT payloads, or unsanitized protocol errors.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command reads the named profile from versioned YAML config under `os.UserConfigDir`.

The profile must include:

- name
- driver
- host
- serial (used by the driver for TLS SNI and MQTT topic construction)
- timeout or default timeout

## Secret Requirements

The command loads keychain entries using the driver name and profile name from the stored profile:

- Access code: `<driver>:<name>:access-code`
- TLS fingerprint: `<driver>:<name>:tls-fingerprint` (skipped when `--insecure` or `profile.insecure: true`)

Keychain reads use the same bounded command timeout as the status request.

If the access code is missing or keychain access fails, the command fails with exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with exit code `3`.

If the TLS fingerprint is present but empty or not formatted as `sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

## Behavior

- The command is read-only.
- The command dispatches through the driver-neutral status interface.
- Default timeout is `10s`.
- No retry is performed by default.
- Partial status is allowed when optional fields cannot be retrieved.
- Partial status must include warnings.
- Unsupported driver capabilities fail with exit code `5`.
- `--detailed` does not change network behavior. The same pushall request and timeout apply. The flag controls parsing and output scope only.
- Both human and JSON output respect `--detailed`: without it, only summary fields are returned.
- When `--protocol-trace` is set, the trace file is created before connecting to the printer and closed before command exit. If the trace file cannot be created, the command fails before protocol work with exit code `2`. If trace writing or closing fails after protocol work starts, the command fails with exit code `1` unless an earlier, more specific failure already occurred.

## Status Data Contract

### Summary fields (always returned)

- `profile`: profile name.
- `driver`: driver name.
- `state`: one of `unknown`, `offline`, `idle`, `printing`, `paused`, `error`. `offline` means the connection attempt failed or timed out (printer is unreachable). `unknown` means the driver connected but could not determine a clear state from the response.
- `temperatures`: available nozzle, bed, chamber, and target temperatures. `null` if unavailable.
- `job`: active job summary when available. `null` if no active job or unavailable.
- `progress`: percentage and layer counts when available. `null` if unavailable.
- `errors`: active printer errors. Always an array; empty when no active errors. Each element is an object with `code` (string) and `message` (string).
- `warnings`: partial-data or non-fatal retrieval warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

### Extended portable fields (returned with `--detailed`)

All extended fields are `null` or omitted when unavailable. Missing extended data does not produce an error; a warning is added when a field is expected but absent for the connected hardware.

- `fans`: named fan speeds. Object with string keys (fan name) and integer values (speed percentage 0-100). `null` if unavailable. Example keys: `partCooling`, `heatbreak`, `auxiliary`, `chamber`.
- `timeEstimates`: time information for the current job. `null` if unavailable or no job active.
  - `elapsedSeconds`: integer. Time since print started.
  - `remainingSeconds`: integer or `null`. Estimated time remaining.
  - `totalSeconds`: integer or `null`. Estimated total print time.
- `speedLevel`: current speed profile or level. String value. Driver-defined values (for example `"silent"`, `"standard"`, `"sport"`, `"ludicrous"` for Bambu). `null` if unavailable.
- `wifi`: network signal information. `null` if unavailable.
  - `signalDbm`: integer. Wi-Fi signal strength in dBm.
- `lights`: lighting state. Object with string keys (light name) and string values (`"on"`, `"off"`, or a brightness level). `null` if unavailable. Example keys: `chamber`.
- `printMeta`: metadata about the current print file. `null` if unavailable or no job active.
  - `fileName`: string. Full file path or name.
  - `fileSize`: integer or `null`. File size in bytes.
  - `nozzleDiameter`: float or `null`. Nozzle diameter in mm.
  - `bedType`: string or `null`. Plate type (for example `"textured_pei"`, `"cool_plate"`).
- `stage`: current operational sub-stage within the print state. String. More granular than `state` (for example `"heating"`, `"printing"`, `"cooling"`, `"leveling"`). `null` if unavailable.
- `timelapse`: timelapse recording status. `null` if unavailable.
  - `recording`: boolean. Whether timelapse recording is active.
  - `progress`: integer or `null`. Recording progress percentage.
  - `ready`: boolean or `null`. Whether a timelapse file is ready for retrieval.
- `gcodePosition`: g-code execution position. `null` if unavailable.
  - `zMm`: float. Current Z height in millimeters.
  - `currentLine`: integer. Current g-code line number.
  - `totalLines`: integer. Total g-code lines.

### Brand-specific extensions (returned with `--detailed`)

- `extensions`: object keyed by driver name. Contains hardware-specific data that has no portable equivalent. `null` if the driver has no extensions or `--detailed` is not set.

For the `bambu-lan` driver:

- `extensions.bambu-lan.ams`: AMS (Automatic Material System) data. `null` if no AMS connected or data unavailable.
  - `units`: array of AMS unit objects.
    - `id`: integer. Unit index (0-based).
    - `humidity`: integer or `null`. Humidity percentage inside the unit.
    - `temperature`: float or `null`. Temperature in Celsius inside the unit.
    - `trays`: array of tray objects.
      - `slot`: integer. Tray slot index (0-based).
      - `filamentType`: string or `null`. Material type (for example `"PLA"`, `"PETG"`, `"ABS"`).
      - `color`: string or `null`. Hex color code (for example `"FF0000"`).
      - `remainingPercent`: integer or `null`. Estimated remaining filament percentage.
      - `nozzleTempMin`: integer or `null`. Minimum nozzle temperature for this filament in Celsius.
      - `nozzleTempMax`: integer or `null`. Maximum nozzle temperature for this filament in Celsius.

Other drivers define their own extension keys as needed.

## Output

### Summary output (default)

Human success example:

```text
Printer: garage-x1c
State: printing
Progress: 42%
Nozzle: 215.0 C / 220.0 C
Bed: 60.0 C / 60.0 C
Job: bracket.3mf
```

Human output with active printer errors:

```text
Printer: garage-x1c
State: error
Errors:
- hms:00000001:00000002 hardware error
```

Human partial-data example:

```text
Printer: garage-x1c
State: printing
Progress: 42%
Warnings:
- chamber temperature unavailable
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "state": "printing",
    "temperatures": {
      "nozzle": {
        "currentCelsius": 215.0,
        "targetCelsius": 220.0
      },
      "bed": {
        "currentCelsius": 60.0,
        "targetCelsius": 60.0
      }
    },
    "job": {
      "name": "bracket.3mf"
    },
    "progress": {
      "percent": 42
    },
    "errors": [],
    "warnings": [],
    "capabilities": {
      "status": true
    }
  },
  "error": null,
  "meta": {
    "command": "status",
    "durationMs": 148
  }
}
```

When `--protocol-trace` is enabled and the trace file is opened successfully, JSON `meta` may include `protocolTracePath`. Human output never includes trace contents.

Human output sanitizes printer-supplied strings (job names, bed types, light names and modes, filament types and colors, error and warning text) by replacing C0/C1 terminal control characters with U+FFFD. JSON output relies on normal JSON string escaping.

### Detailed output (`--detailed`)

Human detailed example:

```text
Printer: garage-x1c
State: printing
Stage: printing
Progress: 42% (layer 84 / 200)
Speed: standard
Time: 1h 12m elapsed, 1h 40m remaining
Nozzle: 215.0 C / 220.0 C
Bed: 60.0 C / 60.0 C
Chamber: 38.0 C
Fans:
  Part cooling: 100%
  Heatbreak: 70%
  Auxiliary: 50%
  Chamber: 30%
Wi-Fi: -45 dBm
Lights:
  Chamber: on
Job: bracket.3mf (14.2 MB, 0.4mm nozzle, textured_pei)
G-code: Z 12.40 mm, line 48201 / 112400
Timelapse: recording (35%)
AMS:
  Unit 0 (humidity: 25%, temp: 28.0 C):
    Slot 0: PLA red (85%)
    Slot 1: PLA white (62%)
    Slot 2: PETG black (40%)
    Slot 3: (empty)
```

JSON detailed example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "state": "printing",
    "temperatures": {
      "nozzle": {
        "currentCelsius": 215.0,
        "targetCelsius": 220.0
      },
      "bed": {
        "currentCelsius": 60.0,
        "targetCelsius": 60.0
      },
      "chamber": {
        "currentCelsius": 38.0
      }
    },
    "job": {
      "name": "bracket.3mf"
    },
    "progress": {
      "percent": 42,
      "currentLayer": 84,
      "totalLayers": 200
    },
    "fans": {
      "partCooling": 100,
      "heatbreak": 70,
      "auxiliary": 50,
      "chamber": 30
    },
    "timeEstimates": {
      "elapsedSeconds": 4320,
      "remainingSeconds": 6000,
      "totalSeconds": 10320
    },
    "speedLevel": "standard",
    "wifi": {
      "signalDbm": -45
    },
    "lights": {
      "chamber": "on"
    },
    "printMeta": {
      "fileName": "bracket.3mf",
      "fileSize": 14893261,
      "nozzleDiameter": 0.4,
      "bedType": "textured_pei"
    },
    "stage": "printing",
    "timelapse": {
      "recording": true,
      "progress": 35,
      "ready": false
    },
    "gcodePosition": {
      "zMm": 12.4,
      "currentLine": 48201,
      "totalLines": 112400
    },
    "extensions": {
      "bambu-lan": {
        "ams": {
          "units": [
            {
              "id": 0,
              "humidity": 25,
              "temperature": 28.0,
              "trays": [
                {
                  "slot": 0,
                  "filamentType": "PLA",
                  "color": "FF0000",
                  "remainingPercent": 85,
                  "nozzleTempMin": 190,
                  "nozzleTempMax": 230
                },
                {
                  "slot": 1,
                  "filamentType": "PLA",
                  "color": "FFFFFF",
                  "remainingPercent": 62,
                  "nozzleTempMin": 190,
                  "nozzleTempMax": 230
                },
                {
                  "slot": 2,
                  "filamentType": "PETG",
                  "color": "000000",
                  "remainingPercent": 40,
                  "nozzleTempMin": 220,
                  "nozzleTempMax": 260
                },
                {
                  "slot": 3,
                  "filamentType": null,
                  "color": null,
                  "remainingPercent": null,
                  "nozzleTempMin": null,
                  "nozzleTempMax": null
                }
              ]
            }
          ]
        }
      }
    },
    "errors": [],
    "warnings": [],
    "capabilities": {
      "status": true
    }
  },
  "error": null,
  "meta": {
    "command": "status",
    "durationMs": 148
  }
}
```

JSON timeout example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "timeout",
    "message": "status request timed out",
    "details": {
      "profile": "garage-x1c",
      "timeout": "10s"
    }
  },
  "meta": {
    "command": "status"
  }
}
```

## Exit Codes

- `0`: status retrieved, including partial status with warnings.
- `1`: general failure, including trace write or close failure after protocol work starts.
- `2`: usage, profile, config, validation error, or invalid/uncreatable protocol trace path before protocol work starts.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability.

## Error Cases

- Missing `<name>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Access code not found in keychain.
- TLS fingerprint not found in keychain (secure profile).
- TLS fingerprint invalid in keychain (secure profile).
- TLS fingerprint mismatch (TOFU violation).
- Authentication failed.
- Connection failed.
- Timeout.
- Driver does not support status.
- Driver returns malformed status.

## Security Requirements

- Do not print or log access codes.
- Do not include protocol payloads in debug logs unless redacted.
- Protocol trace output must contain sanitized event summaries only. It must not include access codes, raw auth payloads, raw MQTT payloads, TLS private material, or unsanitized parser/transport errors.
- Do not perform discovery or scanning.
- Do not send state-changing commands.
- Sanitize authentication and transport errors.
- Sanitize secret-store backend errors.

## Test Scenarios

### Summary mode

- Returns full status for a mock driver.
- Returns partial status with warnings.
- Fails when profile is missing.
- Fails when access code is missing from keychain.
- Fails when TLS fingerprint is missing for a secure profile.
- Fails with exit code `3` on TLS fingerprint mismatch (TOFU violation).
- Skips TLS fingerprint check when profile has `insecure: true`.
- Skips TLS fingerprint check when `--insecure` flag is passed.
- Fails with auth error.
- Fails with timeout.
- Fails with unsupported capability.
- Uses command timeout override.
- Emits stable JSON envelope.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Fails before connecting when the protocol trace file cannot be created.
- Does not leak access codes, raw auth payloads, raw MQTT payloads, or TLS material in protocol trace output.
- Does not leak secrets in output or logs.
- Sanitizes terminal control characters in printer-supplied strings in human output.
- Does not include extended fields without `--detailed`.

### Detailed mode

- Returns all available extended portable fields with `--detailed`.
- Returns brand-specific extensions under `extensions.<driver>` with `--detailed`.
- Omits `extensions` key without `--detailed`.
- Returns partial extended data with warnings when some fields are unavailable.
- Returns `null` for AMS when no AMS is connected (not an error).
- Fan speeds are integer percentages 0-100.
- Time estimates use seconds as the unit.
- Speed level is a string value from the driver.
- Wi-Fi signal is in dBm (negative integer).
- Lights values are strings (`"on"`, `"off"`, or brightness level).
- G-code position includes Z height, current line, and total lines.
- Timelapse reports recording state, not camera stream access.
- JSON detailed output includes all fields in the documented schema.
- Human detailed output formats extended fields readably.

## Non-goals

- Starting, pausing, canceling, or uploading jobs.
- Discovering printers.
- Showing Bambu cloud state.
- Retrying transient failures.
- Camera stream access (separate `camera stream` command).
- Controlling temperatures, fans, speed, or any other printer state.
- Polling or continuous monitoring (single snapshot per invocation).
