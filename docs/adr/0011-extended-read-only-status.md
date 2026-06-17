# ADR 0011: Extended Read-Only Status

## Status

Accepted

## Context

ADR 0006 established read-only status as the first Bambu LAN driver capability. The initial implementation parses a minimal set of fields from the MQTT pushall report: state, nozzle/bed/chamber temperatures, job name, progress, and errors.

The Bambu MQTT pushall report contains significantly more read-only telemetry that users expect from a printer monitoring tool: AMS (Automatic Material System) slot and filament data, fan speeds, time estimates, speed level, Wi-Fi signal, chamber lighting state, print metadata, stage information, timelapse recording status, and g-code execution position.

Other printer brands expose similar categories of read-only data (fans, time estimates, speed). A single status command that only shows basic temperatures and progress leaves users reaching for vendor apps to see information that is already available in the same network response.

## Decision

The read-only status scope is expanded to include all available telemetry from the pushall report. The data is organized into two tiers:

### Summary tier (default)

The existing status fields remain the default output: state, temperatures, job, progress, errors, and warnings. This is backward-compatible with the current contract.

### Detailed tier (opt-in via `--detailed`)

When the user passes `--detailed`, the status response includes all available extended fields. Both human and JSON output respect this flag; without it, extended fields are omitted.

### Portable vs brand-specific data

Extended fields are split into two categories:

**Portable fields** (universal concepts that any driver may implement):

- Fan speeds (named fans with speed percentage).
- Time estimates (elapsed, remaining, total).
- Speed level or profile name.
- Wi-Fi signal strength.
- Lighting state.
- Print metadata (file name, file size, nozzle diameter, bed/plate type).
- Stage (a more granular sub-state within the print process).
- Timelapse recording status.
- G-code position (Z height, current line, total lines).

**Brand-specific extension block** (hardware unique to one ecosystem):

- AMS data (unit count, per-tray filament type, color, remaining percentage, humidity, temperature).

Brand-specific data lives in a typed `extensions` object keyed by driver name. This keeps the portable contract clean while allowing rich brand-specific telemetry.

### Partial data

Drivers return whatever fields are available. Missing fields are `null` or omitted, not errors. Warnings are emitted for expected-but-missing data (for example, AMS fields absent on a printer with no AMS connected).

### Network behavior

No change. The Bambu driver already receives the full pushall report in a single MQTT message. The `--detailed` flag only affects parsing and output scope, not the network request or timeout.

### Security

All extended fields are read-only telemetry. No state-changing commands are introduced. No new secrets or credentials are required. The same sanitization rules apply: no raw protocol payloads in output, no secret leakage, no unsanitized errors.

## Consequences

- The `status` command spec must be updated to define the detailed contract, the `--detailed` flag, and the portable/extension field schemas.
- The driver contract (`StatusResult`) must be extended with optional structs for fans, time, speed, Wi-Fi, lights, print meta, stage, timelapse, g-code position, and a typed extensions map.
- The Bambu LAN driver spec must define the field mapping for all new Bambu report fields to portable and extension fields.
- The Bambu LAN driver parses more fields from the same pushall response.
- Not all models report all fields. The A1 Mini has no chamber fan or built-in AMS; some firmware versions omit certain fields. The partial-data-with-warnings policy from the current spec remains in effect.
- Future drivers for other brands implement the same portable fields where supported.
- Camera streaming (RTSP) remains a separate command (`camera stream`); timelapse fields only report recording status.
- The `--detailed` flag applies to both human and JSON output formats.
