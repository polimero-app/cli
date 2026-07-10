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

Out of scope in this slice:

- `printer discover`
- `printer tls refresh`
- `camera stream`
- `camera snapshot`
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
- Otherwise default to `http://<host>:7125`.

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
  `TemperatureSet`, `MotionHome`, `MotionJog`

Each event includes safe request metadata (`method`, path, optional HTTP
status), duration, and optional byte counts. Errors are emitted as sanitized
categories (`auth_rejected`, `protocol_error`, `parse_error`, `timeout`,
`cancelled`, `connection_error`).

The driver must not emit access codes, auth headers, unsanitized backend
errors, or raw payloads containing credential material.
