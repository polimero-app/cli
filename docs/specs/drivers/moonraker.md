# Driver Spec: Moonraker

## Status

Accepted

## Purpose

Define the `moonraker` LAN driver for Klipper/Moonraker printers using the
existing driver-neutral command surface.

## Scope

Implemented commands:

- `status`
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
- `fans set`
- `speed set`

Out of scope in this slice:

- `printer discover`
- `printer tls refresh`
- `camera stream`
- `camera snapshot`
- `lights set` (stock Klipper has no portable light command and no way to
  confirm a light state; ADR 0014 forbids advertising an unconfirmable
  capability. A later spec revision may add `SET_LED`-based control.)
- Cloud auth or cloud APIs

## Capability Policy

Capabilities for `moonraker`:

- `Status: true`
- `FileList: true`
- `FileDownload: true`
- `FileUpload: true`
- `JobStart: true`
- `JobPause: true`
- `JobResume: true`
- `JobCancel: true`
- `TemperatureWrite: true`
- `MotionControl: true`
- `FanControl: true`
- `LightControl: false`
- `SpeedControl: true`
- `Discovery: false`
- `TLSRefresh: false`
- `CameraStream: false`
- `CameraSnapshot: false`

## Profile Fields

Required:

- `driver: moonraker`
- `host`
- `timeout`

Optional:

- `serial` (unused by this driver)

Secret (OS keychain only):

- Moonraker API key in `access-code`: service `polimero`, account
  `moonraker:<name>:access-code`

## Transport

HTTP(S) API with bounded request timeouts inherited from `context.Context`.

Base URL rules:

- If `host` includes scheme, use it as provided (`http://` or `https://`).
- Any other scheme is rejected as a usage error (exit code `2`).
- Otherwise default to `http://<host>:7125`.

Transport hardening:

- HTTP redirects are not followed; a redirect response is treated as a failed
  request so the API key is never resent to another host and `https` cannot be
  silently downgraded.
- JSON API responses are capped at 8 MiB; larger responses fail with a
  sanitized error. File downloads stream and are not subject to the cap.

Authentication:

- Send `X-Api-Key: <access-code>` header on requests.

## Operation Mapping

### Status

- Query `printer.objects.query` with `webhooks`, `print_stats`,
  `virtual_sdcard`, `extruder`, and `heater_bed`.
- Map to portable status fields:
  - `state`: from `print_stats.state` and `webhooks.state`
  - `temperatures`: extruder and bed current/target
  - `job.name`: from `print_stats.filename`
  - `progress.percent`: from `virtual_sdcard.progress`

### File operations

- Roots expose one writable root: `gcodes`.
- List uses Moonraker directory listing endpoint for a normalized path under
  `gcodes`.
- Download streams one file per call.
- Upload sends one file per call; no implicit print start.

### Job control

- Start uses Moonraker print-start endpoint with the normalized `gcodes` path.
- Pause/resume/cancel use their corresponding Moonraker endpoints.
- Driver waits (bounded by context) for resulting portable state.

### Temperature set

- Uses Moonraker gcode-script endpoint.
- Maps to:
  - nozzle: `M104 S<n>`
  - bed: `M140 S<n>`
  - chamber: `M141 S<n>`

### Motion

- Home: `G28` with optional axis list.
- Jog: relative move sequence `G91`, `G1 ... F...`, `G90`.
- Returns accepted or complete per the motion contract.

### Auxiliary Control

Fan and speed control send standard Klipper G-code through the gcode-script
endpoint, then confirm the effect by reading the corresponding printer object
back through `/printer/objects/query`, bounded by the command timeout:

- **Fans**: `M106 S<pwm>` for `partCooling` only.
  - Percent conversion: pwm = round(percent × 255 / 100).
  - Acknowledgment: poll the `fan` object until `speed` (0.0–1.0) echoes the
    requested percentage within 1 point; timeout fails with exit code `4`.
  - `auxiliary` and `chamber` fail with exit code `5`: stock Klipper's `M106`
    has no fan index, and portable mapping to `SET_FAN_SPEED` fan names does
    not exist. A later spec revision may add configured fan-name mapping.
- **Speed**: `M220 S<percent>` for speed profiles (silent=20%, standard=100%,
  sport=150%, ludicrous=300%).
  - Acknowledgment: poll the `gcode_move` object until `speed_factor` echoes
    the requested factor; timeout fails with exit code `4`.

Light control is not supported; see Capability Policy.

## Error Mapping

- Auth failures: exit code `3`.
- Network/timeout failures: exit code `4`.
- Unsupported features: exit code `5`.
- Validation/config/path errors: exit code `2`.

Errors must be sanitized and must not include API keys or raw backend payloads.

## Security Requirements

- Never log API key.
- Never store API key in config.
- Use context-bounded HTTP requests.
- Do not perform discovery/network scanning during ordinary commands.
- Keep upload/start as separate operations.

## Protocol Trace

When `--protocol-trace <file>` is enabled by the command layer, the driver
emits sanitized JSON Lines events for Moonraker HTTP operations.

- Driver: `moonraker`
- Transport: `http`
- Phase: `request`
- Operations: `ConnectCheck`, `Status`, `FileList`, `FileDownload`,
  `FileUpload`, `JobStart`, `JobPause`, `JobResume`, `JobCancel`,
  `TemperatureSet`, `MotionHome`, `MotionJog`, `FanSet`, `SpeedSet`

Each event includes safe request metadata (`method`, path, optional HTTP
status), duration, and optional byte counts. Errors are emitted as sanitized
categories (`auth_rejected`, `protocol_error`, `parse_error`, `timeout`,
`cancelled`, `connection_error`).

The driver must not emit access codes, auth headers, unsanitized backend
errors, or raw payloads containing credential material.
