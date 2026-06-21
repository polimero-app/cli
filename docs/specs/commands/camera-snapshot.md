# Command Spec: `camera snapshot`

## Status

Accepted

## Purpose

Capture a single still image from a printer's camera and save it as a JPEG file. Supports both MJPEG (A1 family) and H.264/RTSPS (X1/P1/H2 family) camera protocols.

This is a subcommand of the `camera` top-level action group, which consumes printer profiles managed by `printer`, following ADR 0008.

## Syntax

```text
polimero camera snapshot <name> [--to <path>] [--overwrite] [--timeout <duration>] [--insecure] [--output <format>]
```

## Arguments

- `<name>`: existing printer profile name.

## Flags

- `--to <path>`: optional. Destination file path or directory. When a directory, the file is written inside using the auto-generated name. Default: auto-generated name in the current working directory.
- `--overwrite`: optional. Allows replacing an existing destination file.
- `--timeout <duration>`: optional. Overrides profile/default timeout for the camera connection and frame capture.
- `--insecure`: optional. Skips TLS verification for this invocation regardless of the profile `insecure` setting.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Auto-Generated File Name

When `--to` is not specified or names a directory, the output file name is:

```text
<profile-name>-<timestamp>.jpg
```

Where `<timestamp>` is the local time formatted as `2006-01-02T15-04-05`. Example: `garage-x1c-2026-06-17T23-10-39.jpg`.

## Config Requirements

The command reads the named profile from versioned YAML config under `os.UserConfigDir`.

The profile must include:

- name
- driver
- host
- serial
- timeout or default timeout

## Secret Requirements

The command loads keychain entries using the driver name and profile name:

- Access code: `<driver>:<name>:access-code`
- TLS fingerprint: `<driver>:<name>:tls-fingerprint` (skipped when `--insecure` or `profile.insecure: true`)

The camera endpoint reuses the same TLS fingerprint as the MQTT broker. No additional keychain entry is required.

If the access code is missing or keychain access fails, the command fails with exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with exit code `3`.

If the TLS fingerprint is present but empty or not formatted as `sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

## Behavior

- The command is read-only. It does not send state-changing commands to the printer.
- The command resolves the printer profile, loads secrets, and calls the driver's `CameraSnapshot` operation.
- The driver connects to the camera endpoint, captures a single frame, decodes it if necessary (H.264), encodes it as JPEG, and returns the image bytes.
- The command writes the JPEG data to the destination file.
- The command validates the destination path before opening the camera connection.
- Default timeout is `10s`.
- No retry is performed by default.
- No discovery or scanning is performed.

### Frame Capture by Protocol

**MJPEG (A1/A1 mini family):** The driver connects to port 6000, authenticates, reads a single JPEG frame from the proprietary Bambu frame format, and returns it directly.

**H.264/RTSPS (X1/P1/H2 family):** The driver connects via RTSPS on port 322, waits for a keyframe with codec parameters, begins decoding there, continues feeding later access units until the decoder emits an image, encodes that image as JPEG, and returns the image bytes.

The Bambu LAN implementation uses system FFmpeg libraries (`libavcodec`, `libavutil`, and `libswscale`) via cgo for H.264 frame decoding. This avoids vendoring codec code; packagers are responsible for using maintained FFmpeg packages and verifying their selected FFmpeg license configuration.

### File Write

- If `--to` names an existing directory, the snapshot is written inside that directory using the auto-generated name.
- If `--to` is omitted, the snapshot is written to the current working directory using the auto-generated name.
- If `--to` names a file path, the snapshot is written to that exact path.
- If the destination file exists and `--overwrite` is not set, the command fails with exit code `2`.
- Local writes should use a temporary file in the destination directory and atomically rename it into place where the OS supports atomic rename.
- Partial local files are removed after failed captures when practical.

## Output

Human success example:

```text
Snapshot saved to ./garage-x1c-2026-06-17T23-10-39.jpg (142 KiB).
```

Human success example with `--to`:

```text
Snapshot saved to /home/user/photos/printer.jpg (142 KiB).
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "path": "./garage-x1c-2026-06-17T23-10-39.jpg",
    "sizeBytes": 145408,
    "protocol": "h264"
  },
  "error": null,
  "meta": {
    "command": "camera snapshot",
    "durationMs": 1243
  }
}
```

JSON error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "capability_unsupported",
    "message": "driver \"bambu-lan\" does not support camera snapshot"
  },
  "meta": {
    "command": "camera snapshot"
  }
}
```

## Exit Codes

- `0`: snapshot captured and saved.
- `1`: general failure, including local filesystem I/O errors or frame decode errors.
- `2`: usage, profile, config, path, destination-exists, or validation error.
- `3`: auth or secret error.
- `4`: network or timeout error (camera endpoint unreachable, frame capture timed out).
- `5`: driver does not support `CameraSnapshot`.

## Error Cases

- Missing `<name>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Access code not found in keychain.
- TLS fingerprint not found in keychain (secure profile).
- TLS fingerprint invalid in keychain (secure profile).
- TLS fingerprint mismatch (TOFU violation).
- Camera endpoint unreachable (both protocols failed).
- Frame capture timed out (no decodable image received within timeout for H.264).
- Frame decode failure (corrupted H.264 data).
- JPEG encoding failure.
- Destination file exists without `--overwrite`.
- Destination directory is not writable.
- Destination path is invalid.
- Driver does not support `CameraSnapshot`.

## Security Requirements

- The HTTP server is not used; no network listener is started.
- Do not print or log access codes.
- Do not include camera payloads or TLS material in debug logs unless redacted.
- Sanitize authentication, transport, and camera protocol errors before CLI output.
- Sanitize secret-store backend errors.
- TLS fingerprint pinning follows ADR 0007 and applies to the camera endpoint.
- Local writes must not overwrite files unless `--overwrite` is set.
- The command must not write to any path other than the resolved destination.

## Driver Interface

The driver exposes a new `CameraSnapshot` method gated by the `CameraSnapshot` capability flag:

```go
CameraSnapshot(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (*CameraSnapshotResult, error)
```

Where:

```go
type CameraSnapshotResult struct {
    Data         []byte       // JPEG-encoded image
    Protocol     string       // "mjpeg" or "h264" (source protocol used)
    Capabilities Capabilities
}
```

Contract:

- The driver connects to the camera endpoint, captures a single frame, and returns JPEG bytes.
- `ctx` controls the connection and frame capture timeout.
- For MJPEG: read one frame from the proprietary Bambu frame format (already JPEG).
- For H.264: connect via RTSPS, start decoding at a keyframe with codec parameters, continue until a decoded image is available, and encode as JPEG.
- The driver must use the same TLS fingerprint pinning as for the MQTT and camera stream connections.
- Return `unsupported_capability` when the driver does not support camera snapshot.
- Sanitize transport, TLS, decode, and encode errors before returning.
- Do not log camera image data or secrets.

## Test Scenarios

- Captures JPEG snapshot from mock MJPEG driver (A1-family protocol).
- Captures JPEG snapshot from mock H.264/RTSPS driver (X1-family protocol).
- Saves to current working directory with auto-generated name when `--to` is omitted.
- Saves to specified file path with `--to`.
- Saves inside directory when `--to` names a directory.
- Auto-generated name includes profile name and local timestamp.
- Fails with exit `2` when destination file exists without `--overwrite`.
- Overwrites existing file with `--overwrite`.
- Fails with exit `2` when profile is not found.
- Fails with exit `2` when destination directory is not writable.
- Fails with exit `3` when access code is missing from keychain.
- Fails with exit `3` on TLS fingerprint mismatch (TOFU violation).
- Skips TLS fingerprint check when profile has `insecure: true`.
- Skips TLS fingerprint check when `--insecure` flag is passed.
- Fails with exit `4` when camera endpoint is unreachable.
- Fails with exit `4` when frame capture times out.
- Fails with exit `5` when driver does not support `CameraSnapshot`.
- Uses atomic write with temporary file and rename.
- Cleans up partial files on failure.
- Emits stable JSON envelope when `--output json`.
- JSON `data.sizeBytes` matches actual written file size.
- JSON `data.protocol` reflects the source protocol used.
- Does not leak access code or TLS material in output or logs.

## Non-goals

- Saving as PNG or any format other than JPEG.
- Continuous capture or interval-based series.
- Image post-processing (resize, crop, rotate, overlay).
- Streaming the image to stdout.
- Cloud camera (TUTK/Agora p2p).
- Camera configuration (resolution, exposure, white balance).
- Thumbnail generation.
