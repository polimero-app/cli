# Design: Promote `status` to a Top-Level Command

**Date:** 2026-06-17
**Scope:** Move `printer status` to a top-level `status` command. Documentation (ADR + command spec) and code (package layout, shared helper extraction).

---

## 1. Motivation

ADR 0008 split the CLI into a `printer` management plane (`add`, `remove`, `list`, `status`, `discover`, `tls-refresh`, `drivers`) and a top-level operational plane for commands that act on a running printer (`camera`, future `jobs`/`files`). `status` was left under `printer`, but it queries the live device over the network, not the local profile store — by ADR 0008's own test ("operations against a running printer" → top-level), it was mis-filed, most likely because it was the first command ever implemented, before the plane split existed.

Reconsideration was prompted by three compounding factors: ergonomics (`polimero status <name>` vs `polimero printer status <name>` for the single most-used command), consistency (camera/jobs/files will all be top-level "verb/group + name" against a live printer, leaving `status` as the odd one out), and usage/discoverability friction.

No release has shipped (no git tags, no changelog), so this is a clean rename with no back-compat alias and no deprecation period.

## 2. Decision

`status` moves to the top level. `printer` becomes purely profile CRUD. Documented as a new ADR that supersedes ADR 0008's placement of `status`.

## 3. Documentation changes

- **New ADR 0010** ("Promote `status` to a Top-Level Command"): restates the plane split with `status` moved to the operational plane. `printer` owns `add`, `remove`, `list`, `discover`, `tls-refresh`, `drivers`. Top level owns `status`, `camera`, and future `jobs`/`files`, all resolving a profile via `<name>`.
- **ADR 0008**: `Status` field changes from `Accepted` to `Superseded`, with a one-line pointer to ADR 0010. Content otherwise left as the historical record — not rewritten.
- **Command spec**: `docs/specs/commands/printer-status.md` renamed to `docs/specs/commands/status.md`. Changes are limited to: title, the `Syntax` line (`polimero status <name> ...`), and `meta.command` in the JSON examples (`"printer status"` → `"status"`). All behavioral sections (config, secrets, behavior, data contract, exit codes, security, test scenarios, non-goals) are unchanged.

## 4. Code changes

**Shared helper extraction.** `validateProfileName` and its backing regex currently live in `cmd/printer/add.go`, shared (within-package) by `add`/`remove`/`status`/`tls-refresh`. Once `status` leaves the `printer` package, it needs this validation too, and future `camera`/`jobs`/`files` packages will hit the same need. Lift it into `internal/config` as `config.ValidateProfileName(name string) error` — config-domain logic, alongside the existing `Profile`/`GetProfile`. All four existing call sites in `cmd/printer` switch to the exported version.

**New `cmd/status` package.** `cmd/printer/status.go` and `cmd/printer/status_test.go` move to `cmd/status/status.go` and `cmd/status/status_test.go`. Since `status` has no subcommands, the package exposes `Command()` directly (no `printer.go`-style aggregator needed). Rename to drop the `Status` stutter now that the package name disambiguates: `StatusDeps` → `Deps`, `StatusCommandWithDeps` → `CommandWithDeps`, `statusCommand()` → folded into the exported `Command()`. The `meta.command` value written by the command's JSON success/error paths changes from `"printer status"` to `"status"`.

**Wiring.** `cmd/root.go` imports `cmd/status` and adds `status.Command()` alongside `printer.Command()`. `cmd/printer/printer.go` drops the `statusCommand()` registration line.

**Test fixtures.** `status_test.go`'s move means it loses access to `cmd/printer`'s package-level `test_constants_test.go` (`testFingerprint`, `alternateFingerprint`). Duplicate the two constants into a `cmd/status` test file — trivial, not worth a shared test-support package for two strings.

## 5. File map

| File | Action |
|---|---|
| `docs/adr/0010-status-top-level-command.md` | Create |
| `docs/adr/0008-top-level-action-commands.md` | Modify — `Status: Superseded`, pointer to 0010 |
| `docs/specs/commands/printer-status.md` | Rename to `docs/specs/commands/status.md`, update syntax/title/`meta.command` |
| `internal/config/config.go` | Modify — add `ValidateProfileName` (moved from `cmd/printer/add.go`) |
| `cmd/printer/add.go` | Modify — remove `validateProfileName`/`profileNameRE`, call `config.ValidateProfileName` |
| `cmd/printer/remove.go`, `cmd/printer/tls_refresh.go` | Modify — call `config.ValidateProfileName` instead of the package-local helper |
| `cmd/printer/status.go`, `cmd/printer/status_test.go` | Delete (moved) |
| `cmd/status/status.go` | Create — moved from `cmd/printer/status.go`, package renamed, `Status` stutter dropped, `meta.command` updated |
| `cmd/status/status_test.go` | Create — moved from `cmd/printer/status_test.go`, adjusted imports/names, local copy of fingerprint test constants |
| `cmd/printer/printer.go` | Modify — drop `statusCommand()` registration |
| `cmd/root.go` | Modify — wire `status.Command()` |

## 6. Testing strategy

Existing status command tests move and continue to pass under the new package/import paths — no behavioral test changes, since the command's logic is unchanged. `internal/config` gets new direct tests for `ValidateProfileName` (likely already covered indirectly via `add`/`remove`/`tls-refresh`/`status` tests, but worth a focused unit test now that it's a public package function). No new root-wiring test is strictly required beyond what `cmd/status`'s own command tests already exercise.

## 7. Non-goals

- No `printer status` alias or deprecation period.
- No implementation of `camera`, `jobs`, or `files` — this only clears the package-layout precedent for them.
- No change to the status command's behavior, flags, output format, or exit codes — only its location in the command tree and its package.
