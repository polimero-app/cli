# ADR 0009: Device File Management Commands

## Status

Accepted

## Context

Operators often need to inspect and move files stored on a printer, such as print jobs saved on device storage, without going through a slicer UI.

File APIs are security-sensitive. Some printer protocols expose listing, upload, download, delete, and print-start behavior through the same transport or authentication scope. Polimero needs a command contract that allows file listing and transfer while keeping printer-control behavior explicit and separate.

The command must also support automation. Human output should behave like familiar shell file commands, while scripts need the same stable JSON envelope used by other commands.

## Decision

Polimero will add a top-level `files` command group that consumes configured printer profiles managed by `printer`, following ADR 0008.

`printer` remains responsible for profile lifecycle. `files` performs file operations against an existing profile.

Initial subcommands:

- `polimero files roots <printer>`
- `polimero files list <printer> [<device-path>...]`
- `polimero files download <printer> <device-path> [--to <local-path>]`
- `polimero files upload <printer> <local-path> <device-path>`

The command group supports:

- Listing device files and directories with `ls`-style behavior.
- Downloading files from device storage.
- Uploading files to device storage.
- Human-readable default output and `--output json`.

The command group does not include:

- Starting a print from an uploaded or selected file.
- Pausing, resuming, canceling, heating, moving, or otherwise changing printer state.
- Deleting, renaming, moving, or creating directories.
- Editing file contents.

The command layer remains driver-neutral. Device file management is represented by capabilities (`FileList`, `FileDownload`, and `FileUpload`) and driver operations that use portable roots, paths, directory entries, and transfer results. Drivers that do not support a requested file capability return `unsupported_capability`.

Device paths use named roots, such as `sdcard:/models/cube.3mf`, rather than raw protocol paths. The command validates and normalizes device paths before dispatch and rejects traversal components. Drivers map named roots to protocol-specific storage paths internally.

Network and secret handling follow the existing security baseline:

- Existing profile config is used for non-secret printer identity.
- Existing OS keychain entries are used for access code and TLS fingerprint where the driver requires them.
- Calls are bounded by a timeout.
- No retry is performed by default.
- No discovery or scanning is performed.
- Transport security must not be silently downgraded.

The Bambu LAN driver supports the initial file command group through LAN-mode FTP/FTPS as specified in `docs/specs/drivers/bambu-lan.md`.

## Consequences

- File workflows sit at the same CLI level as profile workflows, matching the conceptual model that `printer` manages profiles and other commands consume them.
- Upload and download become explicit file-transfer operations, not hidden side effects of browsing.
- A future implementation can add the command before every driver supports every file capability; unsupported drivers fail with exit code `5`.
- Device paths remain portable across drivers because users address named roots instead of protocol paths.
- Future delete, rename, mkdir, recursive transfer, and print-start commands require separate ADRs and command specs.
