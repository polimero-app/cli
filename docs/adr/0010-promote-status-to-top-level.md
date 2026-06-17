# ADR 0010: Promote Status to Top-Level Command

## Status

Accepted

## Context

ADR 0008 established that action commands operating on a running printer belong at the top level of the CLI, separate from the `printer` management group. Commands like `camera stream` and `files list` follow this pattern: they resolve a named printer profile and perform an operational task against the running device.

`printer status` is semantically an operational command. It connects to a running printer, queries live state, and returns telemetry. It does not create, modify, or delete profile configuration. Its placement under `printer` was a historical artifact of the initial implementation, where it was the first command that connected to a device.

As the command surface grows, keeping `status` under `printer` while `camera` and `files` sit at the top level creates an inconsistency: users must remember which operational commands live under `printer` and which do not.

## Decision

`printer status` is promoted to `polimero status <name>`. The syntax, flags, behavior, and exit codes remain unchanged except:

- The command path becomes `status` instead of `printer status`.
- The JSON envelope `meta.command` field becomes `"status"`.
- The `printer` subcommand `status` is removed entirely (no deprecation alias).

The `printer` group retains only profile management commands: `add`, `remove`, `list`, `discover`, `tls refresh`, and `drivers`.

This supersedes the ownership list in ADR 0008, which previously included `status` under `printer`.

## Consequences

- The CLI command tree now has a clean separation: `printer` is the management plane, and top-level commands (`status`, `camera`, `files`) are the operational plane.
- Existing scripts or documentation referencing `polimero printer status` will break. Since the CLI is pre-1.0 and has no published stability guarantee, this is acceptable.
- The shared profile resolution and secret loading logic used by `status` must be accessible outside the `cmd/printer` package, enabling future top-level action commands to reuse it.
- The `printer-status.md` command spec is superseded by `status.md`.
