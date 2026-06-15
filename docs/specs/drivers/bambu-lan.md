# Driver Spec: Bambu LAN

## Status

Accepted

## Purpose

Define the first Bambu LAN driver slice for read-only printer status.

## Scope

Initial target families:

- X1
- P1
- A1

Initial auth mode:

- LAN access code only.

Initial command support:

- `printer status`

Out of scope:

- Bambu cloud auth.
- Bambu cloud APIs.
- Authorization bypass.
- Job upload.
- Job start.
- Pause, cancel, movement, heating, or other state-changing commands.

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

### Push Behavior by Family

| Family | Push behavior |
|---|---|
| X1 | Full status object in every push message |
| P1 | Delta only (changed fields only) in autonomous push messages |
| A1 | Delta only (changed fields only) in autonomous push messages |

Always publish `pushall` on connect to obtain a complete status object regardless of family. Do not rely on autonomous push messages for the initial status read.

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
| `print.nozzle_temper` | `temperatures.nozzle.currentCelsius` | Float; °C |
| `print.nozzle_target_temper` | `temperatures.nozzle.targetCelsius` | Float; °C |
| `print.bed_temper` | `temperatures.bed.currentCelsius` | Float; °C |
| `print.bed_target_temper` | `temperatures.bed.targetCelsius` | Float; °C |
| `print.chamber_temper` | `temperatures.chamber.currentCelsius` | Float; °C; no target available |
| `print.mc_percent` | `progress.percent` | Integer 0–100 |
| `print.layer_num` | `progress.currentLayer` | Integer |
| `print.total_layer_num` | `progress.totalLayers` | Integer |
| `print.subtask_name` | `job.name` | Preferred; use if non-empty |
| `print.gcode_file` | `job.name` | Fallback when `subtask_name` is empty |
| `print.mc_print_error_code` | `errors` | Map to error when value is not `"0"` |

`job` is `null` when both `subtask_name` and `gcode_file` are empty or absent.

`temperatures` fields set to `null` as a group when none of the temperature fields are present. Individual temperature sensors (`chamber`) are omitted from the response when their field is absent from the payload rather than set to `null`.

`progress` is `null` when `mc_percent` is absent.

Treat absent fields as unavailable, not as zero values. Implementations may also accept `print.mc_layer_num` as a compatibility fallback for `print.layer_num`, but `print.layer_num` takes precedence when both are present.

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

Real printer tests:

- Optional.
- Build-tagged.
- Never required in default CI.
