# Command Spec: `camera stream`

## Status

Accepted (not yet implemented)

## Purpose

Start a local HTTP server that proxies the printer's camera feed and print the URL. MJPEG streams (A1 family) are served browser-viewable; H.264 streams (X1/P1 family) are served for media players (VLC, mpv).

## Syntax

```text
polimero camera stream <name> [--port <port>] [--timeout <duration>] [--insecure] [--output <format>]
```

## Arguments

- `<name>`: existing printer profile name.

## Flags

- `--port <port>`: local HTTP server port. Default: `8080`. Must be a valid port number (1–65535). Fails with exit code `2` if the port is already in use.
- `--timeout <duration>`: auto-stop after this duration. Optional. No default (runs until Ctrl+C). Must parse as a Go duration and be greater than zero.
- `--insecure`: skip TLS verification for the camera endpoint for this invocation, regardless of the profile `insecure` setting.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

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

## Behavior

- The command is read-only. It does not send state-changing commands to the printer.
- The command resolves the printer profile, loads secrets, and calls the driver's `CameraStream` operation.
- The driver returns a raw stream and a format descriptor (`mjpeg` or `h264`).
- The command layer starts an HTTP server on `127.0.0.1:<port>` and serves the stream at `/stream`.
- All other HTTP paths return `404`.
- The HTTP server runs until Ctrl+C is received or `--timeout` elapses (exit code `0`).
- If the stream errors after serving has started, the command exits with code `1`.
- Default timeout used for the initial camera connection is the profile or command `--timeout` value.

## Protocol Details (Bambu LAN)

The Bambu LAN driver auto-detects the camera protocol:

1. Attempt TLS connection to port `6000` (MJPEG, A1/A1 mini families). Probe timeout: 2s.
2. If port `6000` is refused or times out, attempt TLS connection to port `322` (H.264 Annex-B, X1/P1/P2/H-series/X2D families).
3. Whichever port connects first determines the `format` in the result.

The driver owns the TLS connection and returns an `io.ReadCloser` over the raw stream. The command layer owns the HTTP server.

## HTTP Serving

| Format | Content-Type |
|---|---|
| `mjpeg` | `multipart/x-mixed-replace; boundary=frame` |
| `h264` | `video/h264` |

The HTTP server binds to `127.0.0.1` only. It is not accessible from other hosts on the network.

## Output

Human output (MJPEG):

```text
Streaming camera from garage-a1m
Format: MJPEG (open in browser)
URL: http://localhost:8080/stream

Press Ctrl+C to stop.
```

Human output (H.264):

```text
Streaming camera from garage-x1c
Format: H.264 (open with VLC or mpv)
URL: http://localhost:8080/stream

Press Ctrl+C to stop.
```

Human output on clean exit:

```text
Stream stopped.
```

JSON output (printed once when server is ready, then blocks):

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "url": "http://localhost:8080/stream",
    "format": "h264",
    "port": 8080
  },
  "error": null,
  "meta": {
    "command": "camera stream"
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
    "message": "driver \"bambu-lan\" does not support camera streaming"
  },
  "meta": {
    "command": "camera stream"
  }
}
```

## Exit Codes

- `0`: clean exit (Ctrl+C or `--timeout` elapsed).
- `1`: stream error after serving started.
- `2`: usage, profile, config, or validation error.
- `3`: auth or secret error.
- `4`: network error (camera endpoint unreachable, both ports failed).
- `5`: driver does not support `CameraStream`.

## Error Cases

- Missing `<name>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Access code not found in keychain.
- TLS fingerprint not found in keychain (secure profile).
- TLS fingerprint mismatch (TOFU violation).
- `--port` out of range or already in use.
- `--timeout` invalid or zero.
- Camera endpoint unreachable (both ports refused or timed out).
- Driver does not support `CameraStream`.

## Security Requirements

- The HTTP server binds to `127.0.0.1` only.
- HTTP `/stream` is the only served path; all others return `404`.
- Do not print or log access codes.
- Do not include camera payloads or TLS material in debug logs unless redacted.
- Sanitize authentication, transport, and camera protocol errors before CLI output.
- Sanitize secret-store backend errors.
- TLS fingerprint pinning follows ADR 0007 and applies to the camera endpoint.

## Test Scenarios

- Starts HTTP server and serves MJPEG stream from mock A1-family driver.
- Starts HTTP server and serves H.264 stream from mock X1-family driver.
- Protocol auto-detection: port 6000 refused → falls back to port 322.
- Fails with exit `2` when profile is not found.
- Fails with exit `2` when `--port` is already in use.
- Fails with exit `2` when `--timeout` is invalid or zero.
- Fails with exit `3` when access code is missing from keychain.
- Fails with exit `3` on TLS fingerprint mismatch (TOFU violation).
- Skips TLS fingerprint check when profile has `insecure: true`.
- Skips TLS fingerprint check when `--insecure` flag is passed.
- Fails with exit `4` when both camera ports are unreachable.
- Fails with exit `5` when driver does not support `CameraStream`.
- `--timeout` auto-stops server after duration (exit `0`).
- HTTP `/stream` serves correct `Content-Type` per format.
- HTTP server returns `404` for all paths other than `/stream`.
- Emits stable JSON envelope when `--output json`.
- Does not leak access code or TLS material in output or logs.

## Non-goals

- Binding the HTTP server to non-localhost addresses.
- Transcoding H.264 to a browser-native format.
- Cloud camera (TUTK/Agora p2p).
- Recording or saving the stream to disk.
- Multiple simultaneous streams from one command invocation.
- Camera snapshot (`camera snapshot`) — separate future command.
