# Camera Stream Design

**Date:** 2026-06-15
**Scope:** ADR 0008 (top-level action commands), `camera stream` command spec, driver contract `CameraStream` extension, Bambu LAN dual-protocol camera support.

---

## 1. Architecture

### Command Hierarchy (ADR 0008)

`printer` is a management plane. `camera` is a new top-level operational plane that operates on named profiles. Future groups (`jobs`, `files`) follow the same pattern.

```
polimero printer ...     # profile management
polimero camera stream   # camera action on a named profile
```

### Protocol Split (Bambu LAN)

Bambu printers expose the camera on two different ports depending on hardware family:

| Port | Format | Families |
|---|---|---|
| 6000 | MJPEG over TLS | A1, A1 mini |
| 322 | H.264 Annex-B over custom TLS | X1, P1S, P2S, H-series, X2D |

The driver auto-detects by probing port 6000 first (2s timeout), falling back to port 322.

### Layer Responsibilities

- **Driver layer**: owns TLS connection, protocol detection, returns `io.ReadCloser` + `CameraFormat`.
- **Command layer**: owns HTTP server, `Content-Type` selection, URL printing, Ctrl+C / timeout handling.

---

## 2. Driver Contract Extension

New capability flag:
```go
CameraStream bool
```

New method:
```go
CameraStream(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (CameraStreamResult, error)
```

New types:
```go
type CameraStreamResult struct {
    Format       CameraFormat
    Stream       io.ReadCloser
    Capabilities Capabilities
}

type CameraFormat string  // "mjpeg" | "h264"
```

The camera endpoint reuses the TLS fingerprint already pinned at `printer add` time — no new keychain entry.

---

## 3. Command Design

```text
polimero camera stream <name> [--port 8080] [--timeout <duration>] [--insecure] [--output human|json]
```

HTTP server binds to `127.0.0.1:<port>`. Serves only `/stream`; all other paths return `404`.

| Format | Content-Type |
|---|---|
| mjpeg | `multipart/x-mixed-replace; boundary=frame` |
| h264 | `video/h264` |

JSON output is emitted once when the server is ready, then the command blocks.

---

## 4. Key Decisions

- **localhost-only binding**: reduces attack surface; no `--bind` flag in v1.
- **Shared TLS fingerprint**: camera endpoint uses the same leaf certificate as the MQTT broker on Bambu printers — reusing the existing keychain entry is correct.
- **No transcoding**: H.264 families are not browser-viewable natively; the spec documents this and directs users to VLC/mpv. Adding a codec dependency is out of scope.
- **Driver-owned stream, command-owned HTTP**: preserves the existing pattern where drivers own protocol work and the command layer owns presentation.
- **Auto-detection over model field**: probing ports avoids adding a `camera_protocol` field to the profile and handles firmware variation naturally.

---

## 5. Files Produced

| File | Description |
|---|---|
| `docs/adr/0008-top-level-action-commands.md` | ADR establishing the management/operational split |
| `docs/specs/commands/camera-stream.md` | Command spec |
| `docs/specs/drivers/driver-contract.md` | Extended with `CameraStream` capability and operation |
