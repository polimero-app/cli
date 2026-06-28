# Command Spec: `jobs`

## Status

Accepted

## Purpose

Start, pause, resume, and cancel a print job on a configured printer through driver-neutral job control operations.

The `jobs` command group is a top-level command. It consumes printer profiles created and managed by `printer`.

This command is covered by ADR 0008 for top-level action command placement and ADR 0012 for printer control commands.

## Syntax

```text
polimero jobs start <printer> <device-path> [--plate <n>] [--skip-leveling] [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
polimero jobs pause <printer> [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
polimero jobs resume <printer> [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
polimero jobs cancel <printer> [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<printer>`: existing printer profile name.
- `<device-path>`: logical path to a file already on printer storage, using a named root, such as `sdcard:/models/cube.3mf` (see `docs/specs/commands/files.md` for the device path contract). The file must already exist on the printer; `jobs start` does not upload it.

## Flags

- `--plate <n>`: optional for `jobs start`. Selects a specific plate/sub-file index within a multi-plate file. Default: driver/printer default (typically the first or only plate).
- `--skip-leveling`: optional for `jobs start`. Skips automatic bed leveling before the print starts. Default: leveling enabled.
- `--yes`: optional. Skips the interactive confirmation prompt. Required in non-interactive sessions (no controlling TTY).
- `--timeout <duration>`: optional. Overrides profile/default timeout for the status check and the job action.
- `--insecure`: optional. Skips TLS verification for this invocation regardless of the profile `insecure` setting.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol diagnostics to a new local file. The file must not already exist. Trace output may include status precheck phases, job action phase names, capability decisions, selected portable states, acknowledgment state, byte counts, durations, parser warnings, and sanitized error categories. It must not include access codes, raw auth payloads, raw MQTT payloads, raw command payloads, TLS private material, or unsanitized protocol errors.
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

Keychain reads use the same bounded command timeout as the job action.

If the access code is missing or keychain access fails, the command fails with exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with exit code `3`.

If the TLS fingerprint is present but empty or not formatted as `sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

The command must not prompt for new secrets.

## Execution Order

Every subcommand follows this sequence:

1. Resolve profile and load secrets.
2. Validate `<device-path>` format for `jobs start` (exit code `2` on malformed input; no network call).
3. If `--protocol-trace` is set, create the trace file before any protocol work.
4. Query current status via the driver-neutral status operation.
5. Check the state precondition for the subcommand (see below). Fail with exit code `2` and error code `invalid_printer_state` if not met.
6. Prompt for confirmation unless `--yes` is set.
7. Dispatch the job action to the driver. The driver blocks (bounded by `--timeout`) until it confirms the resulting state.
8. Close the trace file if one was opened, then render the result.

## State Preconditions

| Subcommand | Required current state |
|---|---|
| `jobs start` | `idle` |
| `jobs pause` | `printing` |
| `jobs resume` | `paused` |
| `jobs cancel` | `printing` or `paused` |

If the current state cannot be determined (e.g. `offline` or `unknown`), the precondition is treated as not met.

## Confirmation

All four subcommands are state-changing and require confirmation by default:

- Without `--yes`, in an interactive session, the command prompts: `<Action description> for <printer>? Type 'yes' to continue: `. Any answer other than `yes` fails with exit code `2`.
- Without `--yes`, in a non-interactive session (no controlling TTY), the command fails immediately with exit code `2`.
- `--yes` skips the prompt entirely.

## Behavior

- Default timeout is `10s`, applied to both the status check and the job action.
- No retry is performed by default.
- `jobs start` only starts a print from a file already on printer storage; it never uploads a local file. Use `files upload` first.
- Each subcommand waits for the driver to confirm the printer reached the expected resulting state before reporting success:
  - `jobs start` and `jobs resume` expect `printing`.
  - `jobs pause` expects `paused`.
  - `jobs cancel` expects `idle`.
- If the confirmed resulting state contradicts the expected transition, the command fails with exit code `1` and error code `job_action_failed`, reporting the unexpected state in `error.details`.
- If no confirming state update arrives before the timeout, the command fails with exit code `4` and error code `timeout`.
- Unsupported driver capabilities fail with exit code `5`.
- If the protocol trace file cannot be created, the command fails before protocol work with exit code `2`. If trace writing or closing fails after protocol work starts, the command fails with exit code `1` unless an earlier, more specific failure already occurred.

## Data Contracts

Job action result fields:

- `profile`: profile name.
- `driver`: driver name.
- `action`: one of `start`, `pause`, `resume`, `cancel`.
- `devicePath`: normalized device path (present only for `start`).
- `plate`: requested plate index, or `null` if not specified (present only for `start`).
- `state`: confirmed resulting portable state.
- `warnings`: non-fatal warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

## Output

Human pause example:

```text
Printer: garage-x1c
Job paused.
```

Human start example:

```text
Printer: garage-x1c
Starting sdcard:/models/cube.3mf (plate 1)...
Job started.
```

Human confirmation prompt (interactive, no `--yes`):

```text
Cancel the active print on garage-x1c? Type 'yes' to continue: yes
Printer: garage-x1c
Job canceled.
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "action": "pause",
    "state": "paused",
    "warnings": [],
    "capabilities": {
      "jobPause": true
    }
  },
  "error": null,
  "meta": {
    "command": "jobs pause",
    "durationMs": 612
  }
}
```

When `--protocol-trace` is enabled and the trace file is opened successfully, JSON `meta` may include `protocolTracePath`. Human output never includes trace contents.

JSON precondition error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "invalid_printer_state",
    "message": "cannot pause: printer is not printing",
    "details": {
      "profile": "garage-x1c",
      "currentState": "idle",
      "requiredState": "printing"
    }
  },
  "meta": {
    "command": "jobs pause"
  }
}
```

JSON action-failed error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "job_action_failed",
    "message": "cancel did not result in the expected state",
    "details": {
      "profile": "garage-x1c",
      "action": "cancel",
      "expectedState": "idle",
      "observedState": "error"
    }
  },
  "meta": {
    "command": "jobs cancel"
  }
}
```

## Exit Codes

- `0`: job action completed and confirmed.
- `1`: general failure, including `job_action_failed` or trace write/close failure after protocol work starts.
- `2`: usage, profile, config, device-path, precondition, confirmation error, or invalid/uncreatable protocol trace path before protocol work starts.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability.

## Error Cases

- Missing `<printer>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Missing `<device-path>` for `jobs start`.
- Invalid device path format for `jobs start`.
- Device path does not exist on printer storage.
- State precondition not met for the requested action.
- Confirmation declined.
- Non-interactive session without `--yes`.
- Protocol trace path already exists or cannot be created.
- Access code not found in keychain.
- TLS fingerprint not found in keychain (secure profile).
- TLS fingerprint invalid in keychain (secure profile).
- TLS fingerprint mismatch (TOFU violation).
- Authentication failed.
- Connection failed.
- Timeout waiting for confirmed state.
- Driver does not support the requested job action.
- Confirmed resulting state contradicts the expected transition.

## Security Requirements

- Do not print or log access codes.
- Never bypass the state precondition check or the confirmation prompt, regardless of `--output` format.
- `--yes` only skips the interactive prompt; it does not skip the state precondition check.
- `jobs start` must not upload a file; it only starts a print from an existing device path.
- Sanitize authentication, transport, and secret-store errors.
- Sanitize protocol parser errors and do not expose raw protocol payloads.
- Protocol trace output must contain sanitized job-control summaries only. It must not include access codes, raw auth payloads, raw MQTT payloads, raw command payloads, TLS private material, or unsanitized protocol errors.
- Do not perform discovery or scanning.

## Test Scenarios

- Starts a job from a valid device path when idle; confirms resulting state `printing`.
- Starts a job with `--plate` and `--skip-leveling` set.
- Pauses a job when printing; confirms resulting state `paused`.
- Resumes a job when paused; confirms resulting state `printing`.
- Cancels a job when printing; confirms resulting state `idle`.
- Cancels a job when paused; confirms resulting state `idle`.
- Fails with `invalid_printer_state` when the precondition is not met for each subcommand.
- Fails with `invalid_printer_state` when current state is `offline` or `unknown`.
- Fails with exit code `2` when confirmation is declined.
- Fails with exit code `2` in a non-interactive session without `--yes`.
- Skips confirmation when `--yes` is set.
- Fails with `job_action_failed` when the confirmed end-state contradicts the expected transition.
- Fails with `timeout` when no confirming state update arrives in time.
- Fails when profile is missing.
- Fails when access code is missing from keychain.
- Fails when TLS fingerprint is missing for a secure profile.
- Fails with exit code `3` on TLS fingerprint mismatch.
- Skips TLS fingerprint check when profile has `insecure: true`.
- Skips TLS fingerprint check when `--insecure` flag is passed.
- Fails with unsupported capability for a driver that does not support the requested action.
- Uses command timeout override for the status check and the job action.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Fails before protocol work when the protocol trace file cannot be created.
- Does not leak access code, raw auth payloads, raw MQTT payloads, raw command payloads, or TLS material in protocol trace output.
- Emits stable JSON envelope.
- Does not leak secrets in output or logs.

## Non-goals

- Uploading a local file as part of starting a job; use `files upload` first.
- Deleting, renaming, or browsing device files; see `files`.
- Adjusting temperature or motion as part of job control; see `temperature` and `motion`.
- Queuing multiple jobs or job history.
- Discovering printers.
- Showing Bambu cloud job state.
