# ADR 0012: Printer Control Commands (Jobs, Temperature, Motion)

## Status

Accepted

## Context

Polimero so far only implements read-only and file-transfer operations: `status`, `camera`, and `files`. ADR 0009 explicitly deferred state-changing behavior — "pause, cancel, movement, heating, or other state-changing commands" — to a future ADR, and the threat model already names "Unsafe Device Control" as a known risk requiring "exact confirmation behavior" and an "explicit non-interactive bypass flag" before any such command ships.

The driver-neutral `Capabilities` struct already declares `JobUpload`, `JobStart`, `JobPause`, `JobCancel`, `TemperatureRead`, `TemperatureWrite`, and `MotionControl`, but none of these have operation contracts or command specs yet.

Operators need to start, pause, resume, and cancel an active print, set heater targets, and perform basic homing/jog motion, without going through a slicer UI. These are the highest-risk commands in the CLI: sending the wrong command can ruin a print, damage hardware, or create a safety hazard (burns, fire risk from an unattended heater).

## Decision

Polimero will add three new top-level command groups, following the ADR 0008 pattern of top-level groups that act on a named printer profile:

- `polimero jobs start <printer> <device-path> [--plate <n>] [--skip-leveling]`
- `polimero jobs pause <printer>`
- `polimero jobs resume <printer>`
- `polimero jobs cancel <printer>`
- `polimero temperature set <printer> [--nozzle <celsius>] [--bed <celsius>] [--chamber <celsius>]`
- `polimero motion home <printer> [--axis x,y,z]`
- `polimero motion jog <printer> [--x <mm>] [--y <mm>] [--z <mm>] [--feedrate <mm/min>]`

`jobs start` only starts a print from a file already present on printer storage, addressed via the existing device-path scheme (ADR 0009). It does not upload a local file; `files upload` followed by `jobs start` are two separate, explicit, auditable actions.

### Capability additions

A new capability flag, `JobResume`, is added to the `Capabilities` struct (driver-contract.md) alongside the existing `JobStart`, `JobPause`, and `JobCancel`, so that pause-capable and resume-capable behavior can be advertised independently if a driver ever supports only one direction.

### Confirmation

Every command in this ADR is state-changing and requires interactive confirmation by default, using the existing `tty.Prompter` pattern already implemented for `printer remove` and `printer tls refresh`: a `--yes` flag skips the prompt; without it, a non-interactic session fails immediately, and an interactive session must type `yes` to proceed.

### State preconditions

Each command validates the printer's current state (via the driver-neutral status operation) before sending anything, and fails with a distinct, non-network error if the precondition is not met:

- `jobs start` requires `idle`.
- `jobs pause` requires `printing`.
- `jobs resume` requires `paused`.
- `jobs cancel` requires `printing` or `paused`.
- `temperature set` and `motion home`/`motion jog` require `idle`.

Precondition checks happen before the confirmation prompt, so an invalid request fails fast without asking the operator to confirm an action that cannot succeed.

### Confirmed effect

Each command waits (bounded by the command timeout) for the driver to confirm the action actually took effect, not merely that it was sent:

- Job actions wait for the expected resulting state (`paused`, `printing`, or `idle`). If the confirmed end-state contradicts the expected transition (e.g. `cancel` results in `error` instead of `idle`), the command fails with `job_action_failed` rather than reporting a false success.
- `temperature set` waits for the printer to acknowledge the new target value(s), not for the temperature to physically reach target.
- `motion home`/`motion jog` report a result state. Drivers that expose a reliable motion-finished signal return `complete`; drivers that can only prove the command was accepted and a fresh status channel is alive return `accepted`. Commands must not render `accepted` as physical completion.

This is the same pattern by which `files upload`/`files download` report bytes transferred, extended to require confirmation of effect or explicit acceptance rather than just transmission.

### Input bounds

The command layer enforces generic safety bounds before any network call, independent of whatever limits printer firmware itself enforces:

- Nozzle target: 0–300°C.
- Bed target: 0–120°C.
- Chamber target: 0–65°C.
- Jog distance: ±10mm per axis per call.

A value outside these bounds fails immediately with `unsafe_value`, before the state precondition check or confirmation prompt.

### Driver-neutral scope only

This ADR and its command specs define the driver-neutral contract only. Mapping these operations onto Bambu LAN's MQTT protocol (publish payloads, completion signals) is deferred to a future update of `docs/specs/drivers/bambu-lan.md`, the same separation ADR 0009 made between the `files` command contract and the Bambu LAN FTP transport details.

## Consequences

- `jobs`, `temperature`, and `motion` join `status`, `camera`, and `files` as top-level action groups; `printer` remains pure profile CRUD.
- The driver contract gains `JobResume`, plus new operations for job control, temperature control, and motion control. Job and temperature operations return confirmed resulting state or targets. Motion operations return either confirmed completion or explicit accepted state, depending on driver protocol support.
- New error codes (`invalid_printer_state`, `job_action_failed`, `unsafe_value`) join the existing driver-contract error mapping table.
- Drivers implement these operations only when their accepted driver spec defines the protocol mapping. Unsupported drivers return exit code `5` (`capability_unsupported`).
- Job start remains decoupled from file upload, preserving ADR 0009's principle that upload must not implicitly start a print.
- Future extensions (absolute motion, extruder jog, additional heater targets, automation-friendly batch sequencing) require their own ADR or spec update.
