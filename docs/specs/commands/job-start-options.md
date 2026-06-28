# Command Spec Draft: `jobs start` advanced options

## Status

Draft

## Purpose

Define future user-facing options for AMS selection, timelapse recording, and calibration choices during `jobs start`.

This draft does not authorize implementation. The accepted `jobs start` contract remains limited to a file already on printer storage, optional plate selection, and optional leveling skip.

## Proposed Syntax

```text
polimero jobs start <printer> <device-path> [--plate <n>] [--skip-leveling] [--use-ams] [--ams-mapping <mapping>] [--timelapse] [--flow-calibration] [--vibration-calibration] [--layer-inspection] [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Proposed Flags

- `--use-ams`: request AMS filament routing for the print.
- `--ams-mapping <mapping>`: map slicer filament indexes to physical AMS/external slots. Proposed portable format is comma-separated `filament=source` pairs, where `source` is `ams<unit>:<slot>` or `external`, for example `0=ams0:2,1=external`.
- `--timelapse`: request print timelapse recording when supported.
- `--flow-calibration`: request flow/pressure-advance calibration when supported by the model and file.
- `--vibration-calibration`: request vibration calibration when supported.
- `--layer-inspection`: request first-layer inspection when supported.

## Capability Detection

The command layer must resolve model capabilities before publishing MQTT:

- AMS options require an AMS-capable status payload and at least one usable AMS or virtual/external tray mapping source.
- Timelapse requires a status or capability field indicating timelapse support.
- Flow calibration, vibration calibration, and layer inspection require model/firmware support signals.
- Multi-nozzle mappings require a model capability indicating multiple toolheads or nozzle-rack support.

If a requested option is unsupported or cannot be confirmed, fail closed with `capability_unsupported` before publishing the start command.

## Bambu LAN Mapping Notes

Future BambuLAN `project_file` payload fields should map as follows when capability checks pass:

- `--use-ams` -> `use_ams: true`.
- `--ams-mapping` -> `ams_mapping` and `ams_mapping2`, preserving Bambu's legacy and multi-AMS shapes.
- `--timelapse` -> `timelapse: true`; internal timelapse storage, if ever exposed, maps to the `cfg` bitmask and requires its own accepted spec update.
- `--flow-calibration` -> `flow_cali: true` plus `extrude_cali_flag` when the model requires that mirror flag.
- `--vibration-calibration` -> `vibration_cali: true`.
- `--layer-inspection` -> `layer_inspect: true`.

The driver must still keep upload and start separate. These options only affect the MQTT start payload for a file already on printer storage.
