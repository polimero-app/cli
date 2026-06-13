# Command Spec: `printer list`

## Status

Accepted

## Purpose

List configured printer profiles without revealing secrets.

## Syntax

```text
polimero printer list [--output <format>]
```

## Flags

- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command reads versioned YAML profile config from `os.UserConfigDir`.

If no config file exists, the command succeeds with an empty list.

## Secret Requirements

The command must not read secrets from the keychain.

## Output

Profiles are listed in stable alphabetical order by name.

Human success example:

```text
NAME        DRIVER     HOST        SERIAL           TIMEOUT  INSECURE
garage-x1c  bambu-lan  192.0.2.10  01S09C450100XXX  10s      false
```

Human empty example:

```text
No printer profiles configured.
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profiles": [
      {
        "name": "garage-x1c",
        "driver": "bambu-lan",
        "host": "192.0.2.10",
        "serial": "01S09C450100XXX",
        "timeout": "10s",
        "insecure": false
      }
    ]
  },
  "error": null,
  "meta": {
    "command": "printer list"
  }
}
```

## Exit Codes

- `0`: list produced.
- `1`: general failure.
- `2`: config parse or schema error.

## Error Cases

- Config file exists but is unreadable.
- Config schema version is not `1` (any other value is rejected).
- Config file is malformed.
- One or more profiles fail validation.

## Security Requirements

- Do not read keychain secrets.
- Do not print secret metadata that could reveal credential values.
- Do not include filesystem paths in JSON output unless needed for diagnostics.

## Test Scenarios

- Lists one profile including `serial` and `insecure` fields.
- Lists multiple profiles in stable alphabetical order by name.
- Succeeds with empty list when no config exists.
- Rejects malformed config.
- Rejects unsupported config schema version.
- Does not access secret store.
- Emits stable JSON envelope with `serial` and `insecure` fields per profile.
- Omits `serial` from JSON when not present in profile (drivers that do not require it).

## Non-goals

- Testing live printer connectivity.
- Showing secret presence or secret values.
- Discovering printers.

