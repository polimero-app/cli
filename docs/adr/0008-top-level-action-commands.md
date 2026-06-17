# ADR 0008: Top-Level Action Commands Operate on Named Printer Profiles

## Status

Accepted

## Context

The `printer` command group owns profile management: adding, removing, listing, and querying printers. As the CLI grows to support camera streaming, job management, and file browsing, a question arises about where these features live in the command tree.

One option is to extend `printer` with further subcommands (`printer camera stream`, `printer jobs list`). A second option is to introduce top-level command groups that operate on named profiles but are not part of profile management.

## Decision

Action commands that operate on a named printer profile are placed at the top level of the CLI, not under `printer`.

- `printer` owns: `add`, `remove`, `list`, `status`, `discover`, `tls refresh`, `drivers`.
- Top-level action groups own operations against a running printer: `camera`, and future groups such as `jobs` and `files`.

Each top-level action command takes a `<name>` positional argument that resolves a printer profile using the same config and secret loading path as `printer status`. The driver contract and secrets bundle are unchanged.

The first top-level action group is `camera`, with the initial subcommand `camera stream`.

## Consequences

- The separation makes clear that `printer` is a management plane and other top-level commands are an operational plane.
- Every new top-level action group requires a command spec before implementation.
- Each top-level group must declare which driver capabilities it requires and return exit code `5` when the resolved driver does not support them.
- Profile resolution, secret loading, and TLS handling are consistent across all top-level commands because they share the same command-layer logic.
