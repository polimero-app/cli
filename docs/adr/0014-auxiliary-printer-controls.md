# ADR 0014: Auxiliary Printer Control Commands

## Status

Accepted

## Context

ADR 0012 added the first state-changing command slice: jobs, temperature, and
motion. ADR 0011 also expanded read-only status to expose fan speeds, lighting
state, and speed level when a driver can report them.

Operators still need a small set of routine controls that are commonly exposed
by printer front panels and vendor apps:

- Set user-controllable fan speeds.
- Turn printer lights on or off.
- Adjust the active print speed profile.

These controls are state-changing, but they are narrower than arbitrary G-code
and lower risk than heater or motion commands when bounded correctly. They can
still ruin a print, mask a hardware problem, or create unsafe conditions if the
CLI exposes firmware-managed safety outputs. They therefore need the same
confirmation, timeout, capability, and sanitization discipline as ADR 0012.

## Decision

Polimero will add three top-level command groups, following ADR 0008:

- `polimero fans set <printer> <fan> <percent>`
- `polimero lights set <printer> <light> <state>`
- `polimero speed set <printer> <profile>`

These commands form an auxiliary-control slice. They are not a generic escape
hatch for arbitrary printer commands.

### Capability additions

The driver contract gains independent capability flags:

- `FanControl`
- `LightControl`
- `SpeedControl`

Drivers may implement any subset. Unsupported operations return exit code `5`.

### Fan controls

`fans set` controls only user-controllable fans, such as part cooling,
auxiliary, or chamber circulation fans when the connected model exposes them.

The generic fan contract must not expose firmware-managed safety fans such as
heatbreak, hotend, controller, power-supply, or electronics cooling fans unless
a later accepted ADR and driver spec explicitly proves the operation is safe.

Fan speeds are integer percentages from `0` to `100`; `0` means off.

Portable fan targets use canonical JSON/API keys, while the CLI may accept
friendlier aliases:

| Canonical key | Accepted CLI aliases |
|---|---|
| `partCooling` | `part-cooling`, `part_cooling`, `partCooling` |
| `auxiliary` | `aux`, `auxiliary` |
| `chamber` | `chamber` |

The command layer normalizes these portable aliases before dispatch. Driver
specs may define additional safe keys and aliases for model-specific
user-controllable fans, but drivers must return canonical keys in results.

### Light controls

`lights set` controls simple on/off lighting, such as a chamber light. Brightness
control, color control, animations, and scheduled light behavior are deferred.

The portable light key is `chamber`. The CLI accepts `chamber`,
`chamber-light`, and `chamber_light` as aliases and normalizes them to
`chamber` before dispatch. Driver specs may map that portable key to
brand-specific wire keys, such as `chamber_light`, behind the driver boundary.
The portable command contract defines `on` and `off` as the only accepted
states.

### Speed controls

`speed set` controls the active print speed profile, not motion feedrate and not
global firmware acceleration or jerk settings.

Speed profiles are stable lowercase string tokens documented by each driver
spec, such as `silent`, `standard`, `sport`, or `ludicrous` for drivers that
support those concepts. The command layer does not case-fold speed profile
names. A driver must reject unsupported profiles with exit code `5`.

### Target discovery

This ADR does not add a generic `controls` command. The recommended
discoverability path, when the first implementation needs it, is to add
read-only subcommands in the same top-level groups:

- `polimero fans targets <printer>`
- `polimero lights targets <printer>`
- `polimero speed profiles <printer>`

Those commands require their own command spec updates before implementation.
Until then, driver specs and `status --detailed` are the authoritative sources
for supported observed targets, and unsupported targets fail with exit code `5`.

### Confirmation

All three commands are state-changing and require confirmation by default.

The existing confirmation model applies:

- `--yes` skips the interactive prompt.
- Without `--yes`, a non-interactive session fails immediately.
- In an interactive session, the operator must type `yes`.

### State preconditions

Each command queries status before dispatch:

- `fans set` requires `idle`, `printing`, or `paused`.
- `lights set` requires a reachable printer with a known state. It may run in
  `idle`, `printing`, `paused`, or `error` because lighting can be useful during
  inspection.
- `speed set` requires `printing` or `paused`.

If the current state is `offline` or `unknown`, the command fails before
confirmation. `fans set` and `speed set` also fail when the printer is in
`error`.

If the status precheck itself fails because of auth, secret, transport, timeout,
or parsing failure, the command returns that failure as-is and does not prompt
or dispatch the state-changing operation.

### Confirmed effect

The driver blocks, bounded by the command timeout, until it confirms the
requested effect through a fresh status or protocol acknowledgment. A command
must not report success merely because bytes were sent.

The preferred acknowledgment is a fresh status echo that shows the requested
target and value. An explicit protocol acknowledgment is acceptable only when it
positively identifies the requested target and requested value or state. A
transport publish success, socket write success, HTTP success, or fire-and-forget
library return is not sufficient.

If the driver protocol cannot confirm the requested fan speed, light state, or
speed profile, the driver must not advertise the corresponding capability.

### Driver-neutral scope only

This ADR defines the driver-neutral command contract. A driver must not enable
these capabilities until its accepted driver spec documents the protocol mapping
and model-specific limitations.

For Bambu LAN, this means the Bambu driver spec must be updated with verified
MQTT/G-code mappings before implementation sets any of these capability flags to
`true`.

## Consequences

- `fans`, `lights`, and `speed` join the top-level action command surface.
- The command specs define bounds, preconditions, confirmation behavior, output,
  and non-goals before implementation.
- The driver contract gains auxiliary-control operations.
- `status --detailed` remains the source of observed fan, light, and speed
  state, but it is not required to list every controllable target.
- Malformed fan, light, state, or speed-profile tokens use a stable
  command-layer JSON error code, `invalid_argument`; syntactically valid but
  unsafe bounded values continue to use `unsafe_value`.
- Heatbreak and other firmware-managed safety fan overrides remain out of scope.
- Arbitrary G-code, macro execution, calibration routines, filament or AMS
  commands, power control, and maintenance automation remain out of scope.
- Future extensions such as brightness control, fan curves, speed percentages,
  per-material fan presets, or target discovery commands require their own spec
  update, and may require a new ADR if they expand the control model.
