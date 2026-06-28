# Command Spec: `files`

## Status

Accepted

## Purpose

List and transfer files on a configured printer through driver-neutral file operations.

The `files` command group is a top-level command. It consumes printer profiles created and managed by `printer`.

This command is covered by ADR 0008 for top-level action command placement and ADR 0009 for device file management behavior.

## Syntax

```text
polimero files roots <printer> [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
polimero files list <printer> [<device-path>...] [--recursive] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
polimero files download <printer> <device-path> [--to <local-path>] [--overwrite] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
polimero files upload <printer> <local-path> <device-path> [--overwrite] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<printer>`: existing printer profile name.
- `<device-path>`: logical path on printer storage using a named root, such as `sdcard:/plate.3mf`.
- `<local-path>`: local filesystem path for upload source or download destination.

## Flags

- `--recursive`: optional for `files list`. Recursively lists directory contents, matching `ls -R` behavior.
- `--to <local-path>`: optional for `files download`. Destination file or directory. Default: current working directory.
- `--overwrite`: optional for `files download` and `files upload`. Allows replacing an existing destination file.
- `--timeout <duration>`: optional. Overrides profile/default timeout for each device operation.
- `--insecure`: optional. Skips TLS verification for this invocation regardless of the profile `insecure` setting.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol diagnostics to a new local file. The file must not already exist. Trace output may include FTPS connection phases, TLS fingerprint verification status, operation category, root name, normalized device path, listing entry counts, transfer byte counts, durations, parser warnings, and sanitized FTP status categories. It must not include access codes, FTP passwords, raw FTP command streams, transferred file contents, TLS private material, or unsanitized transport errors.
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

Keychain reads use the same bounded command timeout as the file operation.

If the access code is missing or keychain access fails, the command fails with exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with exit code `3`.

If the TLS fingerprint is present but empty or not formatted as `sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

The command must not prompt for new secrets.

## Device Path Contract

Device paths are logical printer paths, not local filesystem paths or raw protocol paths.

Format:

```text
<root>:/<path>
```

Rules:

- `<root>` must be one of the root names returned by `files roots`.
- Root names must use ASCII letters, digits, `.`, `_`, and `-`, and must start with an ASCII letter or digit.
- The path portion must start with `/`.
- `/` is the only path separator.
- Empty segments and `.` segments are normalized away.
- `..` segments are rejected.
- NUL bytes and ASCII control characters are rejected.
- Device paths longer than 1024 bytes after UTF-8 encoding are rejected.

Examples:

- `sdcard:/`
- `sdcard:/calibration-cube.3mf`
- `sdcard:/models/bracket.3mf`

The command passes normalized root and path values to the driver. The driver maps them to protocol-specific storage paths internally.

## Behavior

Common behavior:

- Default timeout is `10s`.
- No retry is performed by default.
- No discovery or scanning is performed.
- Unsupported driver capabilities fail with exit code `5`.
- Human output sanitizes control characters in device and local paths.
- JSON output uses the stable envelope and normal JSON string escaping.
- File names and paths may appear in output and logs, but secrets must never appear.
- When `--protocol-trace` is set, the trace file is created before connecting to printer storage and closed before command exit. If the trace file cannot be created, the command fails before protocol work with exit code `2`. If trace writing or closing fails after protocol work starts, the command fails with exit code `1` unless an earlier, more specific failure already occurred.

`files roots` behavior:

- Requires the `FileList` capability; returns exit code `5` if the driver does not support `FileList`.
- Lists device storage roots supported by the driver for the named printer.
- Returns exit code `0` when roots are retrieved.
- Returns at least one root for drivers that support file operations.

`files list` behavior:

- Follows `ls` behavior for one or more device paths.
- If no device path is provided and the driver exposes exactly one root, lists that root.
- If no device path is provided and the driver exposes multiple roots, lists the roots using the same table format as `files roots`.
- If a device path names a directory, lists its direct children. An empty directory prints the path header followed by `(empty)` in human output and returns `entries: []` in JSON.
- If a device path names a file, lists that file's metadata.
- If multiple paths are listed in human output, each result is headed by its normalized path.
- `--recursive` recursively lists directory descendants, matching `ls -R` behavior. When applied to a file path, `--recursive` has no effect; the file is listed normally.
- Entries are sorted by name using bytewise ascending order after path normalization.
- Human output includes name, type, size, and modified time.
- JSON output includes all metadata returned by the driver.

`files download` behavior:

- Downloads one regular file from printer storage to the local filesystem.
- Directories are rejected; recursive download is out of scope.
- If `--to` names an existing directory, the downloaded file is written inside that directory using the device file base name.
- If `--to` is omitted, the downloaded file is written to the current working directory using the device file base name.
- If the local destination file exists and `--overwrite` is not set, the command fails with exit code `2`.
- Local writes should use a temporary file in the destination directory and atomically rename it into place where the OS supports atomic rename.
- Partial local files are removed after failed downloads when practical.

`files upload` behavior:

- Uploads one local regular file to printer storage.
- Directories are rejected; recursive upload is out of scope.
- If the device path ends with `/` or names an existing device directory, the uploaded file is written inside that directory using the local file base name.
- If the device path names a file, the uploaded file is written to that exact path.
- If the device destination exists and `--overwrite` is not set, the command fails with exit code `2`.
- Uploading stores the file only. It must not start a print.
- The command must not modify or delete the local source file.

## Data Contracts

Root fields:

- `name`: stable root name, such as `sdcard`.
- `description`: human-readable description.
- `writable`: whether upload is supported for this root.
- `capacityBytes`: total capacity when available, or `null`.
- `freeBytes`: free capacity when available, or `null`.
- `metadata`: driver-specific JSON object. Empty when no additional metadata is available.

File entry fields:

- `name`: entry base name.
- `root`: root name.
- `path`: normalized path within the root.
- `devicePath`: normalized full device path, such as `sdcard:/models/cube.3mf`.
- `type`: one of `directory`, `file`, `unknown`.
- `sizeBytes`: file size in bytes, or `null` when unavailable or not applicable.
- `modifiedAt`: RFC 3339 timestamp in UTC, or `null` when unavailable.
- `metadata`: driver-specific JSON object. Empty when no additional metadata is available.

Transfer result fields:

- `profile`: profile name.
- `driver`: driver name.
- `source`: source path.
- `destination`: destination path.
- `bytesTransferred`: bytes transferred when known, or `null`.
- `warnings`: non-fatal warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

## Output

Human roots example:

```text
Printer: garage-x1c

ROOT    WRITABLE  FREE       CAPACITY   DESCRIPTION
sdcard  true      12.4 GiB   29.7 GiB   SD card
```

Human list example:

```text
Printer: garage-x1c
Path: sdcard:/

TYPE       SIZE      MODIFIED              NAME
directory  -         -                     models
file       235 KiB   2026-06-15 12:34 UTC  calibration-cube.3mf
```

Human download example:

```text
Downloaded sdcard:/calibration-cube.3mf to ./calibration-cube.3mf (235 KiB).
```

Human upload example:

```text
Uploaded ./bracket.3mf to sdcard:/models/bracket.3mf (1.8 MiB).
```

JSON roots success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "roots": [
      {
        "name": "sdcard",
        "description": "SD card",
        "writable": true,
        "capacityBytes": 31900131328,
        "freeBytes": 13314390016,
        "metadata": {}
      }
    ],
    "warnings": [],
    "capabilities": {
      "fileList": true
    }
  },
  "error": null,
  "meta": {
    "command": "files roots",
    "durationMs": 87
  }
}
```

When `--protocol-trace` is enabled and the trace file is opened successfully, JSON `meta` may include `protocolTracePath`. Human output never includes trace contents.

JSON list success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "paths": [
      {
        "devicePath": "sdcard:/",
        "entries": [
          {
            "name": "models",
            "root": "sdcard",
            "path": "/models",
            "devicePath": "sdcard:/models",
            "type": "directory",
            "sizeBytes": null,
            "modifiedAt": null,
            "metadata": {}
          },
          {
            "name": "calibration-cube.3mf",
            "root": "sdcard",
            "path": "/calibration-cube.3mf",
            "devicePath": "sdcard:/calibration-cube.3mf",
            "type": "file",
            "sizeBytes": 240640,
            "modifiedAt": "2026-06-15T12:34:00Z",
            "metadata": {}
          }
        ]
      }
    ],
    "warnings": [],
    "capabilities": {
      "fileList": true,
      "fileDownload": true,
      "fileUpload": true
    }
  },
  "error": null,
  "meta": {
    "command": "files list",
    "durationMs": 153
  }
}
```

JSON download success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "source": "sdcard:/calibration-cube.3mf",
    "destination": "./calibration-cube.3mf",
    "bytesTransferred": 240640,
    "warnings": [],
    "capabilities": {
      "fileDownload": true
    }
  },
  "error": null,
  "meta": {
    "command": "files download",
    "durationMs": 312
  }
}
```

JSON upload success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "source": "./bracket.3mf",
    "destination": "sdcard:/models/bracket.3mf",
    "bytesTransferred": 1887436,
    "warnings": [],
    "capabilities": {
      "fileUpload": true
    }
  },
  "error": null,
  "meta": {
    "command": "files upload",
    "durationMs": 843
  }
}
```

JSON error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "device_path_not_found",
    "message": "device path not found",
    "details": {
      "profile": "garage-x1c",
      "path": "sdcard:/missing.3mf"
    }
  },
  "meta": {
    "command": "files download"
  }
}
```

## Exit Codes

- `0`: operation completed.
- `1`: general failure, including local filesystem I/O errors or trace write/close failure after protocol work starts.
- `2`: usage, profile, config, path, destination-exists, validation error, or invalid/uncreatable protocol trace path before protocol work starts.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability.

## Error Cases

- Missing `<printer>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Unknown root name.
- Invalid device path format.
- Device path does not exist.
- Device path is not a directory for directory-only operations.
- Device path is not a file for file-only operations.
- Local source path does not exist.
- Local source path is not a regular file.
- Local destination exists without `--overwrite`.
- Local destination directory is not writable.
- Protocol trace path already exists or cannot be created.
- Access code not found in keychain.
- TLS fingerprint not found in keychain (secure profile).
- TLS fingerprint invalid in keychain (secure profile).
- TLS fingerprint mismatch (TOFU violation).
- Authentication failed.
- Connection failed.
- Timeout.
- Driver does not support the requested file capability.
- Driver returns malformed file metadata.
- Upload rejected by printer storage.
- Download stream terminated before completion.

## Security Requirements

- Do not print or log access codes.
- Do not store or cache file contents in profile config.
- Do not store or cache file listings in profile config.
- Do not perform discovery or scanning.
- Do not start prints after upload.
- Do not delete, rename, move, or create directories.
- Sanitize authentication, transport, path, and secret-store errors.
- Sanitize protocol parser errors and do not expose raw protocol payloads.
- Protocol trace output must contain sanitized file-operation summaries only. It must not include access codes, FTP passwords, raw auth payloads, raw FTP command streams, transferred file contents, TLS private material, or unsanitized transport errors.
- Sanitize device and local paths before human terminal output.
- Do not silently downgrade transport security.
- Local download destinations must not overwrite files unless `--overwrite` is set.
- Upload must read only the local source path requested by the user.

## Test Scenarios

- Lists roots for a mock driver.
- Lists one directory for a mock driver in human output.
- Lists one directory for a mock driver in JSON output.
- Lists one file path directly.
- Lists multiple paths with headings in human output.
- Recursively lists directory descendants when `--recursive` is set.
- Sorts entries by stable name ordering.
- Returns empty `entries: []` for an empty directory.
- Emits all driver-provided metadata in JSON.
- Normalizes repeated separators and `.` path segments.
- Rejects unknown roots.
- Rejects relative device paths.
- Rejects paths containing `..`.
- Rejects control characters in paths.
- Downloads a file to the current directory by default.
- Downloads a file to a destination directory.
- Rejects download overwrite without `--overwrite`.
- Uploads a local regular file to a device directory.
- Uploads a local regular file to an explicit device file path.
- Rejects upload overwrite without `--overwrite`.
- Rejects directory upload and directory download.
- Fails when profile is missing.
- Fails when access code is missing from keychain.
- Fails when TLS fingerprint is missing for a secure profile.
- Fails with exit code `3` on TLS fingerprint mismatch.
- Skips TLS fingerprint check when profile has `insecure: true`.
- Skips TLS fingerprint check when `--insecure` flag is passed.
- Fails with timeout.
- Fails with unsupported capability.
- Uses command timeout override for keychain reads and file operations.
- Emits stable JSON envelope.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Fails before connecting when the protocol trace file cannot be created.
- Does not leak access code, raw auth payloads, raw FTP command streams, transferred file contents, or TLS material in protocol trace output.
- Does not leak secrets in output or logs.
- Upload does not start a print.

## Non-goals

- Deleting, renaming, or moving files.
- Creating directories.
- Recursive upload or download.
- Starting a print from an uploaded or selected file.
- Previewing thumbnails or interpreting file contents.
- Discovering printers.
- Showing Bambu cloud files.
