# Command Spec: `motion`

## Status

Accepted

## Purpose

Home and jog printer axes on a configured printer through a driver-neutral motion control operation.

The `motion` command group is a top-level command. It consumes printer profiles created and managed by `printer`.

This command is covered by ADR 0008 for top-level action command placement and ADR 0012 for printer control commands.

## Syntax

```text
polimero motion home <printer> [--axis <list>] [--yes] [--timeout <duration>] [--insecure] [--output <format>]
polimero motion jog <printer> [--x <mm>] [--y <mm>] [--z <mm>] [--feedrate <mm/min>] [--yes] [--timeout <duration>] [--insecure] [--output <format>]
```

## Arguments

- `<printer>`: existing printer profile name.

## Flags

- `--axis <list>`: optional for `motion home`. Comma-separated subset of `x,y,z` to home. Default: all three axes.
- `--x <mm>`: optional for `motion jog`. Relative move on the X axis. Range: `-10`–`10`.
- `--y <mm>`: optional for `motion jog`. Relative move on the Y axis. Range: `-10`–`10`.
- `--z <mm>`: optional for `motion jog`. Relative move on the Z axis. Range: `-10`–`10`.
- At least one of `--x`, `--y`, `--z` is required for `motion jog`.
- `--feedrate <mm/min>`: optional for `motion jog`. Move speed. Default: `1500`.
- `--yes`: optional. Skips the interactive confirmation prompt. Required in non-interactive sessions (no controlling TTY).
- `--timeout <duration>`: optional. Overrides profile/default timeout for the status check and the motion operation.
- `--insecure`: optional. Skips TLS verification for this invocation regardless of the profile `insecure` setting.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command reads the named profile from versioned YAML config under `os.UserConfigDir`.

The profile must include:

- name
- driver
- host
- serial when required by the driver
- timeout or default timeout

The command does not write profile config.

## Secret Requirements

The command loads keychain entries using the driver name and profile name from the stored profile:

- Access code: `<driver>:<name>:access-code`
- TLS fingerprint: `<driver>:<name>:tls-fingerprint` (skipped when `--insecure` or `profile.insecure: true`)

Keychain reads use the same bounded command timeout as the motion operation.

If the access code is missing or keychain access fails, the command fails with exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with exit code `3`.

If the TLS fingerprint is present but empty or not formatted as `sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

The command must not prompt for new secrets.

## Execution Order

1. Resolve profile and load secrets.
2. Validate input: `--axis` values are a subset of `x,y,z` (home); at least one of `--x`/`--y`/`--z` is given and each is within ±10mm (jog). Exit code `2`, error code `unsafe_value` for out-of-range jog distances, before any network call.
3. Query current status via the driver-neutral status operation.
4. Check the state precondition: current state must be `idle`. Fail with exit code `2` and error code `invalid_printer_state` otherwise.
5. Prompt for confirmation unless `--yes` is set.
6. Dispatch the motion command to the driver. The driver blocks (bounded by `--timeout`) until it confirms the motion finished.
7. Render the result.

## Safety Bounds

Enforced by the command layer before any network call, independent of whatever soft limits the printer firmware itself enforces:

- Jog distance: `-10`mm to `10`mm per axis per call.

A value outside this bound fails with exit code `2` and error code `unsafe_value`, listing the offending axis and value in `error.details`.

## Confirmation

Homing and jogging are state-changing and physically move the toolhead or bed; both require confirmation by default:

- Without `--yes`, in an interactive session, the command prompts with a summary of the requested motion: `Home <axes> on <printer>?` or `Jog <deltas> on <printer>? Type 'yes' to continue: `. Any answer other than `yes` fails with exit code `2`.
- Without `--yes`, in a non-interactive session (no controlling TTY), the command fails immediately with exit code `2`.
- `--yes` skips the prompt entirely.

## Behavior

- Default timeout is `10s`, applied to both the status check and the motion operation.
- No retry is performed by default.
- `motion home` with no `--axis` given homes all three axes in one call.
- `motion jog` moves relative to the printer's current position; it does not accept absolute coordinates.
- `motion jog` covers X/Y/Z only; the extruder (E) axis is out of scope for this command.
- The command waits for the driver to confirm the requested motion (homing or jog) has physically finished before reporting success.
- If no motion-finished confirmation arrives before the timeout, the command fails with exit code `4` and error code `timeout`.
- Unsupported driver capabilities fail with exit code `5`.

## Data Contracts

Motion result fields:

- `profile`: profile name.
- `driver`: driver name.
- `action`: `home` or `jog`.
- `axes`: array of homed axes, e.g. `["x","y","z"]` (present only for `home`).
- `delta`: object with `xMillimeters`, `yMillimeters`, `zMillimeters`, `feedrateMmPerMin` (present only for `jog`). Axis fields are `null` when not requested.
- `warnings`: non-fatal warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

## Output

Human home example:

```text
Printer: garage-x1c
Homing x, y, z...
Homing complete.
```

Human jog example:

```text
Printer: garage-x1c
Jogging x+5.0mm at 1500mm/min...
Jog complete.
```

Human confirmation prompt (interactive, no `--yes`):

```text
Home x, y, z on garage-x1c? Type 'yes' to continue: yes
Printer: garage-x1c
Homing x, y, z...
Homing complete.
```

JSON home success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "action": "home",
    "axes": ["x", "y", "z"],
    "warnings": [],
    "capabilities": {
      "motionControl": true
    }
  },
  "error": null,
  "meta": {
    "command": "motion home",
    "durationMs": 4200
  }
}
```

JSON jog success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "action": "jog",
    "delta": {
      "xMillimeters": 5.0,
      "yMillimeters": null,
      "zMillimeters": null,
      "feedrateMmPerMin": 1500
    },
    "warnings": [],
    "capabilities": {
      "motionControl": true
    }
  },
  "error": null,
  "meta": {
    "command": "motion jog",
    "durationMs": 540
  }
}
```

JSON bounds error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "unsafe_value",
    "message": "jog distance out of range",
    "details": {
      "axis": "z",
      "value": 25.0,
      "minimum": -10.0,
      "maximum": 10.0
    }
  },
  "meta": {
    "command": "motion jog"
  }
}
```

JSON precondition error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "invalid_printer_state",
    "message": "cannot move while printing",
    "details": {
      "profile": "garage-x1c",
      "currentState": "printing",
      "requiredState": "idle"
    }
  },
  "meta": {
    "command": "motion jog"
  }
}
```

## Exit Codes

- `0`: motion completed and confirmed.
- `1`: general failure.
- `2`: usage, profile, config, bounds, precondition, or confirmation error.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability.

## Error Cases

- Missing `<printer>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Invalid `--axis` value for `motion home`.
- No `--x`/`--y`/`--z` given for `motion jog`.
- Jog distance outside the ±10mm bound.
- State precondition not met (printer not idle).
- Confirmation declined.
- Non-interactive session without `--yes`.
- Access code not found in keychain.
- TLS fingerprint not found in keychain (secure profile).
- TLS fingerprint invalid in keychain (secure profile).
- TLS fingerprint mismatch (TOFU violation).
- Authentication failed.
- Connection failed.
- Timeout waiting for motion-finished confirmation.
- Driver does not support motion control.

## Security Requirements

- Do not print or log access codes.
- Never bypass the safety bounds check, the state precondition check, or the confirmation prompt, regardless of `--output` format.
- `--yes` only skips the interactive prompt; it does not skip the bounds or precondition checks.
- Sanitize authentication, transport, and secret-store errors.
- Sanitize protocol parser errors and do not expose raw protocol payloads.
- Do not perform discovery or scanning.

## Test Scenarios

- Homes all axes when `--axis` is omitted.
- Homes a subset of axes when `--axis` is given.
- Jogs a single axis; confirms motion-finished result.
- Jogs multiple axes in one call.
- Uses the default feedrate when `--feedrate` is omitted.
- Uses an explicit feedrate when given.
- Fails with `unsafe_value` for a jog distance above 10 or below -10 on any axis.
- Fails with exit code `2` when no `--x`/`--y`/`--z` is given for `motion jog`.
- Fails with exit code `2` for an invalid `--axis` value.
- Fails with `invalid_printer_state` when current state is not `idle`.
- Fails with exit code `2` when confirmation is declined.
- Fails with exit code `2` in a non-interactive session without `--yes`.
- Skips confirmation when `--yes` is set.
- Fails with `timeout` when no motion-finished confirmation arrives in time.
- Fails when profile is missing.
- Fails when access code is missing from keychain.
- Fails when TLS fingerprint is missing for a secure profile.
- Fails with exit code `3` on TLS fingerprint mismatch.
- Skips TLS fingerprint check when profile has `insecure: true`.
- Skips TLS fingerprint check when `--insecure` flag is passed.
- Fails with unsupported capability for a driver that does not support motion control.
- Uses command timeout override for the status check and the motion operation.
- Emits stable JSON envelope.
- Does not leak secrets in output or logs.

## Non-goals

- Absolute-coordinate moves.
- Extruder (E axis) moves.
- Motion while printing or paused.
- Disabling/enabling stepper motors independently of motion.
- Discovering printers.
