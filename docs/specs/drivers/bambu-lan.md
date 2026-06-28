# Driver Spec: Bambu LAN

## Status

Accepted

## Purpose

Define the Bambu LAN driver slice for printer discovery, TLS refresh, status, camera streaming, camera snapshots, file management, and accepted MQTT/FTPS control operations.

## Scope

Initial target families:

- X1
- P1
- A1
- H2

Initial auth mode:

- LAN access code only.

Initial command support:

- `printer discover`
- `status`
- `printer tls refresh`
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

Out of scope:

- Bambu cloud auth.
- Bambu cloud APIs.
- Authorization bypass.
- Job upload through printer-control APIs.
- Upload-and-start workflows; an integration layer may compose `files upload` and `jobs start`, but the driver keeps them separate.
- BambuTunnelLocal / port `6000` upload or print-start flows.
- File delete, rename, move, and directory creation.
- AMS-aware job start (`use_ams: true`).
- Timelapse control during job start.
- Chamber heater target read-back (M141 is sent; the report omits target).

## Capability Policy

The driver must use capability-gated behavior. It must not assume that every firmware version or model exposes the same status fields.

If the printer or firmware does not support a required status capability, return `unsupported_capability`.

If optional status fields are unavailable, return partial status with warnings.

## Profile Fields

Required:

- `driver: bambu-lan`
- `host`
- `serial` — printer serial number, as shown in the printer's Device Info screen. Stored verbatim (case-sensitive). Used as MQTT topic path component and TLS SNI value.
- `timeout`

Secret (OS keychain only, never in YAML):

- LAN access code: service `polimero`, account `bambu-lan:<name>:access-code`.
- TLS fingerprint: service `polimero`, account `bambu-lan:<name>:tls-fingerprint`. Absent when `insecure: true`.

## Transport Protocol

### Connection

Bambu printers expose an MQTT broker on port `8883` using TLS (MQTT v3.1.1 over TLS).

Connection parameters:

| Parameter | Value |
|---|---|
| Broker | `<host>:8883` |
| Protocol | MQTT v3.1.1 |
| TLS | Required; chain verification skipped (self-signed Bambu CA); leaf cert fingerprinted |
| TLS SNI | Printer serial number (verbatim from profile `serial` field) |
| Username | `bblp` (fixed) |
| Password | LAN access code from secrets bundle |
| Keepalive | 60 seconds |

TLS chain verification is intentionally skipped because Bambu printers present self-signed certificates issued by a Bambu Lab CA that is not present in OS trust stores. The leaf certificate is pinned via TOFU on first connection (ADR 0007); subsequent connections verify the fingerprint against the pinned value.

When `insecure: true`: skip TLS verification entirely; do not set or verify the fingerprint.

### Topics

| Direction | Topic |
|---|---|
| Subscribe (incoming status) | `device/{serial}/report` |
| Publish (outgoing commands) | `device/{serial}/request` |

`{serial}` is the verbatim value from the profile `serial` field. Topic construction must not alter its case.

### Connectivity Check (printer add)

At `printer add` time, the driver performs a full MQTT connection: TLS handshake (capturing the leaf cert fingerprint) followed by MQTT CONNECT with `bblp` and the access code. A CONNACK return code of 0 indicates success. Any non-zero CONNACK return code is an authentication failure.

The driver does not subscribe to topics or publish commands during the connectivity check.

### Status Request Flow

1. Connect to broker (TLS + MQTT auth) and subscribe to `device/{serial}/report`.
2. Publish a `pushall` command to `device/{serial}/request` to request a complete status dump.
3. Wait for a report message containing `print.gcode_state`. Use the command timeout to bound the entire operation.
4. Parse the status payload, map fields to the portable status contract, and disconnect cleanly.

The `pushall` payload:

```json
{
  "pushing": {
    "sequence_id": "1",
    "command": "pushall",
    "version": 1,
    "push_target": 1
  }
}
```

### MQTT Control Commands

Implementation status: experimental for control operations. The driver sends MQTT commands to `device/{serial}/request`, then publishes `pushall` and waits for a matching status/report message on `device/{serial}/report`.

Control command payloads use dynamic decimal `sequence_id` values. The driver must not log raw command payloads or access codes.

#### Job Control

`jobs pause`, `jobs resume`, and `jobs cancel` publish:

```json
{
  "print": {
    "sequence_id": "<decimal>",
    "command": "pause"
  }
}
```

Command names:

| Polimero command | Bambu `print.command` | Expected portable state |
|---|---|---|
| `jobs pause` | `pause` | `paused` |
| `jobs resume` | `resume` | `printing` |
| `jobs cancel` | `stop` | `idle` |

The driver waits for a full status report whose `print.gcode_state` maps to the expected portable state.

#### Job Start From Existing Printer File

`jobs start` remains strictly "file already on printer". It must not upload a local file, probe local paths, or compose an upload/start workflow inside the driver.

For `.3mf` device paths, publish a `project_file` command using a plain `file://` URL accepted by Developer Mode printers:

```json
{
  "print": {
    "sequence_id": "<decimal>",
    "command": "project_file",
    "param": "Metadata/plate_1.gcode",
    "project_id": "0",
    "profile_id": "0",
    "task_id": "0",
    "subtask_id": "0",
    "subtask_name": "cube.3mf",
    "file": "models/cube.3mf",
    "url": "file:///models/cube.3mf",
    "md5": "",
    "bed_type": "auto",
    "bed_leveling": true,
    "flow_cali": false,
    "vibration_cali": false,
    "layer_inspect": false,
    "timelapse": false,
    "use_ams": false,
    "ams_mapping": [],
    "ams_mapping2": [],
    "auto_bed_leveling": 0,
    "nozzle_offset_cali": 0,
    "cfg": "0",
    "extrude_cali_flag": 0
  }
}
```

`--plate <n>` selects `Metadata/plate_<n>.gcode`; when omitted or non-positive, use plate `1`. `--skip-leveling` sets `bed_leveling` to `false`.

For raw `.gcode` device paths, publish `gcode_file` with the normalized printer path in `param`. This path is less Studio-like than `.3mf` `project_file` and remains covered by hardware validation.

The driver waits for `print.gcode_state` in `PRINTING`, `PREPARE`, `RUNNING`, or `SLICING`.

#### Temperature Set

`temperature set` publishes `print.command: "gcode_line"` with one or more heater commands:

| Target | G-code |
|---|---|
| Nozzle | `M104 S<n>` |
| Bed | `M140 S<n>` |
| Chamber | `M141 S<n>` |

The driver waits for a full status report. Nozzle and bed targets must echo the requested target value, including `0` for heater off. Chamber target read-back is not exposed in known Bambu status reports; when a chamber target is requested, the driver reports the sent target only after a fresh full status report is observed.

#### Motion

`motion home` publishes `print.command: "gcode_line"` with `G28` and optional axis letters. `motion jog` publishes `G91`, one `G1` relative move, and `G90`.

Bambu LAN MQTT status does not expose a reliable motion-finished acknowledgment for these G-code lines. The driver therefore returns `MotionResult.State = "accepted"` after the command publish succeeds and a fresh full status report is observed. It must never report this as `complete`.

#### Unsigned Command Rejection

Firmware with command-signature enforcement may reject unsigned `print` commands with `err_code` `84033543` or a reason equivalent to `MQTT Command verification failed`. The driver maps this to an auth/authorization failure with a sanitized message. It must not attempt certificate extraction, signature bypass, cloud credential use, or other authorization bypass behavior.

### Push Behavior by Family

| Family | Push behavior |
|---|---|
| X1 | Full status object in every push message |
| P1 | Delta only (changed fields only) in autonomous push messages |
| A1 | Delta only (changed fields only) in autonomous push messages |
| H2 | Full status object; some fields use different JSON types (see below) |

Always publish `pushall` on connect to obtain a complete status object regardless of family. Do not rely on autonomous push messages for the initial status read.

### H2 Family Payload Differences

The H2 family uses the same MQTT topics and `pushall` command but differs in several field types and locations compared to X1/P1/A1:

| Field | X1/P1/A1 | H2 |
|---|---|---|
| `print.stg` | Integer (stage ID) | Array (stage list) |
| `print.gcode_file_prepare_percent` | Integer | String |
| `print.wifi_signal` | String numeric (e.g. `"-45"`) | String with unit (e.g. `"-69dBm"`) |
| `lights_report` | Top-level sibling of `print` | Nested inside `print` |

The driver must tolerate these type variations without failing. JSON unmarshaling must not reject entire messages due to type mismatches in non-essential fields.

## Camera Transport

Implementation status: implemented.

Capabilities:

- `CameraStream: true`
- `CameraSnapshot: true`

Bambu printers expose LAN camera feeds through family-specific endpoints:

| Family | Endpoint | Protocol | Polimero use |
|---|---|---|---|
| A1 / A1 mini | port `6000` | TLS plus Bambu framed MJPEG | `camera stream`, `camera snapshot` |
| X1 / P1 / H2 | port `322` | RTSPS with H.264 video | `camera stream`, `camera snapshot` |

The driver probes RTSPS/H.264 first and falls back to MJPEG when the RTSPS endpoint is unreachable. It must use the same pinned TLS fingerprint behavior as MQTT:

- If `insecure` is false: verify the presented leaf certificate SHA-256 fingerprint matches the pinned fingerprint.
- If `insecure` is true: skip certificate verification.

MJPEG snapshots read a single proprietary Bambu frame and return the JPEG payload directly.

H.264 snapshots wait for a random-access frame, decode the frame, and JPEG-encode it before returning to the command layer. This implementation uses system FFmpeg libraries through cgo:

| Library | Purpose |
|---|---|
| `libavcodec` | H.264 frame decode |
| `libavutil` | FFmpeg frame allocation and image utilities |
| `libswscale` | YUV-to-RGBA conversion before JPEG encoding |

Dependency and audit posture:

- FFmpeg is not vendored into the repository.
- Builds link against system FFmpeg development packages discovered by `pkg-config`.
- Packagers must use maintained FFmpeg packages and verify their selected FFmpeg license configuration.
- Camera payloads and decoded image data must not be logged.

If H.264 frame decoding or JPEG encoding fails, return a sanitized general failure. If the camera endpoint is unreachable or frame capture times out, return a sanitized network or timeout failure.

## File Storage Transport

Implementation status: implemented.

Capabilities:

- `FileList: true`
- `FileDownload: true`
- `FileUpload: true`

Bambu LAN file operations use the printer's LAN-mode FTP service. Bambu's official Developer Mode documentation says enabling Developer Mode opens FTP, and Bambu's network ports documentation lists LAN mode FTP on port `990` with passive data ports `50000` through `50100`.

### FTP Connection

Connection parameters:

| Parameter | Value |
|---|---|
| Host | profile `host` |
| Control port | `990` |
| Protocol | FTP over TLS, implicit TLS |
| Passive data ports | `50000` through `50100` |
| Username | `bblp` |
| Password | LAN access code from secrets bundle |
| Root | `sdcard` maps to FTP server root `/` |

The driver must use encrypted control and data connections. It must not fall back to plaintext FTP.

The driver must enable TLS session reuse (via `ClientSessionCache`) so that the data connection can resume the TLS session established by the control connection. The H2 family enforces this requirement and rejects data connections without session reuse with FTP status 522.

The driver verifies the FTP TLS leaf certificate using the same pinned fingerprint behavior as MQTT:

- If `insecure` is false: skip TLS chain verification and verify the presented leaf certificate SHA-256 fingerprint matches the pinned fingerprint.
- If `insecure` is false and the pinned fingerprint is empty or malformed, fail closed before login.
- If `insecure` is true: skip certificate verification.

The FTP username and password must never be logged. FTP command logs must redact authentication commands and must not include file contents.

### Named Roots

The Bambu LAN driver exposes one named root:

| Root | Protocol path | Writable | Description |
|---|---|---|---|
| `sdcard` | `/` | `true` | SD card |

If the printer reports no usable SD card or rejects access to the FTP root, file operations return a sanitized `device_path_not_found`, `device_storage_rejected`, or `unsupported_capability` error according to the observed condition.

### Path Mapping

Driver-neutral paths use `sdcard:/...`.

Mapping rules:

- `sdcard:/` maps to FTP path `/`.
- `sdcard:/models/cube.3mf` maps to FTP path `/models/cube.3mf`.
- The command layer rejects traversal before dispatch. The driver must still defensively reject `..`, NUL bytes, and control characters.
- The driver must not expose local host filesystem paths.

### Listing

The driver should use `MLSD` for directory listings when available. If the printer does not support `MLSD`, the driver may fall back to `LIST` only when the parser is strict and returns partial metadata warnings for fields that cannot be determined.

Listing behavior:

- One FTP listing operation returns either one file entry or one directory's direct children.
- `type` maps FTP directory facts to `directory`, regular files to `file`, and unknown entries to `unknown`.
- `sizeBytes` is populated when the FTP server reports size.
- `modifiedAt` is populated when the FTP server reports modification time.
- Driver-specific facts may be returned under `metadata` in JSON output.

### Download

The driver downloads files using the FTP retrieve operation for one regular file per call.

Rules:

- Directory download is unsupported.
- The driver must stream to the command layer and respect context cancellation.
- Short reads or data-channel errors return sanitized network or driver errors.
- File contents must not be logged.

### Upload

The driver uploads files using the FTP store operation for one regular file per call.

Rules:

- Directory upload is unsupported.
- Upload must only store the file. It must not publish MQTT commands or start a print.
- If overwrite is false, the driver or command layer must check destination existence before storing. If existence cannot be determined safely, fail closed with `device_path_exists` or `driver_internal_error`.
- File contents must not be logged.

### Official Bambu References

- `https://wiki.bambulab.com/en/knowledge-sharing/enable-developer-mode`
- `https://wiki.bambulab.com/en/general/printer-network-ports`
- `https://wiki.bambulab.com/en/knowledge-sharing/access-code-connect`

## State Mapping

Map the Bambu `gcode_state` field to the portable state:

| Bambu `gcode_state` | Portable state | Notes |
|---|---|---|
| `IDLE` | `idle` | Printer ready, no active job |
| `FINISH` | `idle` | Print completed; printer is now idle |
| `PRINTING` | `printing` | Active print in progress |
| `PREPARE` | `printing` | Warming up or preparing before print starts |
| `RUNNING` | `printing` | Alternate state name used on some firmware variants |
| `SLICING` | `printing` | On-printer slicing in progress |
| `PAUSED` | `paused` | Print paused by user |
| `FAILED` | `error` | Print failed |
| _(any other value)_ | `unknown` | Unrecognized state; log verbatim value at debug level |

When the printer is reachable but does not return a parseable `gcode_state`, return `unknown`.

When the printer is unreachable (connection or timeout failure), return `offline`.

## Status Field Mapping

Map Bambu JSON fields (inside the `print` object of the report message) to portable status fields:

| Bambu field | Portable field | Notes |
|---|---|---|
| `print.gcode_state` | `state` | Via state mapping table above |
| `print.nozzle_temper` | `temperatures.nozzle.currentCelsius` | Numeric; °C |
| `print.nozzle_target_temper` | `temperatures.nozzle.targetCelsius` | Numeric; °C |
| `print.bed_temper` | `temperatures.bed.currentCelsius` | Numeric; °C |
| `print.bed_target_temper` | `temperatures.bed.targetCelsius` | Numeric; °C |
| `print.chamber_temper` | `temperatures.chamber.currentCelsius` | Numeric; °C; no target available |
| `print.mc_percent` | `progress.percent` | Numeric integer 0–100 |
| `print.layer_num` | `progress.currentLayer` | Numeric integer |
| `print.total_layer_num` | `progress.totalLayers` | Numeric integer |
| `print.subtask_name` | `job.name` | Preferred; use if non-empty |
| `print.gcode_file` | `job.name` | Fallback when `subtask_name` is empty |
| `print.mc_print_error_code` | `errors` | Map to error when value is not `"0"` |

`job` is `null` when both `subtask_name` and `gcode_file` are empty or absent.

`temperatures` fields set to `null` as a group when none of the temperature fields are present. Individual temperature sensors (`chamber`) are omitted from the response when their field is absent from the payload rather than set to `null`.

`progress` is `null` when `mc_percent` is absent.

Numeric Bambu fields consumed by this status mapping may arrive as JSON numbers or numeric strings. The driver must parse both forms for summary fields and detailed fields, including fan speeds, time estimates, speed/stage values, file size, bed type identifiers, g-code line numbers, HMS codes, printer error code zero values, and AMS IDs, humidity, temperature, remaining percentage, and nozzle temperature range fields.

For H-series compatibility, if `print.chamber_temper` is absent, the driver may use an alternate current chamber temperature field such as `print.chamber_temp`, `print.chamber_temperature`, a nested `print.chamber.temp`/`temperature` value, or an equivalent top-level chamber temperature field. Target, fan, light, speed, state, and mode fields must not be used as current chamber temperature.

If a consumed field is present with an unsupported JSON shape, return partial status with a sanitized `status_field_type_mismatch` warning instead of silently dropping the field.

Treat absent fields as unavailable, not as zero values. Implementations may also accept `print.mc_layer_num` as a compatibility fallback for `print.layer_num`, but `print.layer_num` takes precedence when both are present.

## Protocol Trace Diagnostics

When a protocol trace sink is present in `context.Context`, the Bambu LAN driver emits sanitized events for protocol phases and parser decisions.

Allowed trace data:

- MQTT status and control phases: connect, TLS fingerprint verification result, subscribe topic template, publish command name, report byte count, response key inventory, selected `gcode_state`, parser fallback decisions, parser warnings, action acknowledgment state, and sanitized error categories.
- FTPS file phases: control/data connection phases, TLS fingerprint verification result, command category (`list`, `download`, `upload`), root name, normalized driver-neutral device path, transfer byte counts, listing entry counts, parser warnings, and sanitized FTP status categories.
- Camera phases: probed endpoint kind (`mjpeg` or `h264`), selected protocol, TLS fingerprint verification result, frame/stream byte counts, decode phase names, timeout categories, and sanitized codec or camera protocol errors.
- Discovery phases: enabled protocols, listener/probe start results, result counts, deduplication decisions, and sanitized record key inventories.
- TLS refresh phases: connection attempt, SNI presence, captured fingerprint format status, and sanitized TLS error categories.

Forbidden trace data:

- LAN access code, FTP password, MQTT password, raw MQTT CONNECT payloads, raw MQTT report payloads, raw outgoing MQTT command payloads, raw FTP command streams, raw FTP file contents, raw camera frames, decoded images, TLS private material, private keys, certificates beyond fingerprint metadata, and unsanitized transport or parser errors.

Trace output may include printer serial numbers, configured host names or addresses, file names, job names, and operational metadata because those values already appear in command output or profile data. It must still sanitize terminal control characters and JSON strings normally.

### Error Code Mapping

When `mc_print_error_code` is present and its value is not `"0"`, include one entry in the `errors` array:

```json
{
  "code": "printer_error",
  "message": "printer error: <mc_print_error_code value>"
}
```

Do not attempt to decode individual Bambu error codes into human descriptions.

## Transport Security

Transport certificate handling follows ADR 0007 (TLS Trust On First Use).

The driver receives the profile's `insecure` flag and the pinned TLS fingerprint (if present) through the secrets bundle. It must not silently fall back to insecure TLS.

Behavior:

- If `insecure` is false: skip TLS chain verification; verify the presented leaf certificate's SHA-256 fingerprint matches the pinned value. A mismatch is an authentication failure (exit code `3`).
- If `insecure` is false and the pinned fingerprint is empty or malformed, fail closed before connecting.
- If `insecure` is true: skip all certificate verification.

If secure transport cannot be established, return a sanitized connection or authentication error. Do not expose raw MQTT, TLS, or JSON parser errors in command output.

## mDNS Discovery

Capability: `Discovery: true`.

Bambu printers advertise their presence on the local network via mDNS/DNS-SD.

Service type: `_bambu._tcp` (local domain).

TXT record mapping:

| TXT key | `DiscoveredPrinter` field |
|---|---|
| `sn` | `Serial` |
| `dev_model_name` | `Model` |
| `dev_name` | `Name` |

Host is taken from the A record (IPv4 preferred, IPv6 fallback). Port is taken from the SRV record (typically 8883). Entries with no resolvable IP address are skipped silently.

The `Driver` field is always `"bambu-lan"`.

Discovery does not perform TLS handshakes, MQTT connections, or secret reads.

## SSDP Discovery

Bambu printers announce themselves via SSDP (Simple Service Discovery Protocol) on the local network.

**Device type (ST/NT):** `urn:bambulab-com:device:3dprinter:1`

**Discovery method:** Send a UDP M-SEARCH to multicast address `239.255.255.250:1900` with `MX: 3`. The printer replies with an HTTP/1.1 200 response carrying custom headers.

Custom Bambu headers in the response:

| Header | `DiscoveredPrinter` field |
|---|---|
| `DevModel.bambu.com` | `Model` |
| `DevName.bambu.com` | `Name` |
| `USN` (parsed) | `Serial` — extracted from `uuid:SERIAL::urn:...` format |

Host is extracted from the `LOCATION` header (`http://IP/`). If parsing fails, the UDP source IP is used. Port is always `8883` (MQTT).

## UDP Broadcast Discovery

Bambu printers periodically broadcast JSON status packets on UDP port 2021 to the local network.

**Listen address:** `0.0.0.0:2021` (passive — no probe packet sent)

JSON fields in the broadcast payload:

| JSON field | `DiscoveredPrinter` field |
|---|---|
| `sn` | `Serial` |
| `dev_name` | `Name` |
| `dev_product_name` | `Model` |
| `ip` | `Host` (fallback: UDP source IP) |

Port is always `8883` (MQTT).

**Note:** Printers broadcast every 20–30 seconds. With the default 5s scan window, UDP is unreliable as a standalone protocol; it supplements mDNS and SSDP.

## Multi-Protocol Fan-Out and Deduplication

`Discover()` runs mDNS, SSDP, and UDP concurrently. Results are deduplicated by serial number (key `serial:<SN>`). If serial is empty, the host IP is used as the deduplication key (`host:<IP>`). First arrival wins. If all three protocols fail to start, `Discover()` returns exit code 4. If only some protocols fail, results from the remaining protocols are returned.

## External Sources

Allowed protocol research sources:

- Official Bambu documentation.
- User-owned device observations.
- Public OSS sources with compatible licenses and attribution.

Disallowed:

- Cloud credential bypass.
- Identity spoofing against Bambu cloud services.
- Copying incompatible licensed code.
- Embedding private keys, certificates, tokens, or captured secrets.

## Error Mapping

Expected mappings:

| Condition | Error code |
|---|---|
| Bad or missing access code (CONNACK non-zero) | `authentication_failed` |
| Secret not found in keychain | `secret_not_found` |
| TLS fingerprint mismatch | `authentication_failed` |
| Unreachable host or TLS failure | `connection_failed` |
| Context deadline exceeded | `timeout` |
| Unsupported status behavior | `unsupported_capability` |
| Protocol parse failure | `driver_internal_error` (sanitized message) |

## Tests

Required before implementation is considered complete:

- Mock transport: full status from X1-style payload.
- Mock transport: delta-only payload (missing optional fields); partial status with warnings.
- Mock transport: auth failure (CONNACK non-zero).
- Mock transport: TLS fingerprint mismatch.
- Mock transport: timeout waiting for report message.
- Mock transport: unsupported capability.
- State mapping: all known `gcode_state` values produce the correct portable state.
- State mapping: unknown `gcode_state` value produces `unknown`.
- Field mapping: `subtask_name` preferred over `gcode_file`; falls back correctly.
- Field mapping: `mc_print_error_code` non-"0" produces an error entry.
- Field mapping: `mc_print_error_code` "0" produces empty errors array.
- Fixture parsing for representative X1/P1/A1 status payloads when legally and safely available.
- Redaction tests for access code and sensitive payloads.
- File roots: exposes `sdcard` as writable when FTP root is available.
- File list: parses MLSD directory entries with name, type, size, and modified time.
- File list: falls back to strict LIST parsing with warnings when MLSD is unavailable.
- File download: retrieves one regular file and reports bytes transferred.
- File upload: stores one regular file and does not publish MQTT commands.
- File upload: rejects overwrite when overwrite is false and destination exists.
- File operations: authenticate with `bblp` and LAN access code without logging credentials.
- File operations: verify FTP TLS fingerprint unless profile or invocation is insecure.
- File operations: reject traversal and malformed paths defensively in the driver.

Real printer tests:

- Optional.
- Build-tagged.
- Never required in default CI.
