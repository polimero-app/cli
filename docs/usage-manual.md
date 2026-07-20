# Polimero CLI Usage Manual

This manual documents the current Polimero CLI command surface, including all implemented features, command flags, sample usage, and sample output.

---

## 1. What Polimero does

Polimero is a CLI to manage and control 3D printers through driver-specific backends behind a stable command interface.

Implemented drivers:

- `bambu-lan` — Bambu Lab printers over LAN mode
- `moonraker` — Moonraker-compatible Klipper printers

Implemented feature areas:

- Printer profile management
- Printer discovery
- TLS fingerprint pinning refresh
- Read-only printer status
- Camera stream and snapshot
- Printer file operations (roots, list, download, upload)
- Job control (start, pause, resume, cancel)
- Temperature control (set targets)
- Motion control (home, jog)
- Protocol trace diagnostics (`--protocol-trace`)

Accepted but not yet implemented command areas:

- `fans`
- `lights`
- `speed`

---

## 2. Installation and first run

If you are running from source:

```bash
go run . --help
```

Binary usage:

```bash
polimero --help
```

Version:

```bash
polimero --version
```

Sample output:

```text
polimero version dev
```

---

## 3. Global CLI behavior

### 3.1 Root command

```bash
polimero [command]
```

### 3.2 Available top-level commands

- `printer`
- `status`
- `camera`
- `files`
- `jobs`
- `temperature`
- `motion`
- `fans`
- `lights`
- `speed`
- `completion`

### 3.3 Global flags

- `--output <human|json>` (default: `human`)
- `-v, --verbose`
- `--version`

### 3.4 Output modes

#### Human output

Default operator-focused text output.

#### JSON output

All commands that support JSON use a stable envelope:

```json
{
  "ok": true,
  "data": {},
  "error": null,
  "meta": {
    "command": "status"
  }
}
```

Error shape:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "config_error",
    "message": "profile name is required",
    "details": {}
  },
  "meta": {
    "command": "status"
  }
}
```

`meta.durationMs` appears on network operations.  
`meta.protocolTracePath` appears when `--protocol-trace` is used with JSON output.

### 3.5 Exit codes

- `0` success
- `1` general failure
- `2` usage/config error
- `3` authentication/secret error
- `4` network/timeout error
- `5` unsupported capability

---

## 4. Configuration and secrets

### 4.1 Config file location

Default location:

- Linux: `${XDG_CONFIG_HOME:-~/.config}/polimero/polimero.yaml`

Override with:

```bash
POLIMERO_CONFIG_DIR=/custom/path
```

### 4.2 Config content

Non-secret profile fields are stored in YAML. Secrets are **not** stored in YAML.

Example:

```yaml
version: 1
profiles:
  office-x1:
    driver: bambu-lan
    host: 192.168.1.40
    serial: 01S00A123456789
    timeout: 10s
    insecure: false
    created: 2026-07-11T01:10:00Z
    updated: 2026-07-11T01:10:00Z
```

### 4.3 Secrets

Stored in OS keychain under service `polimero`:

- `<driver>:<profile>:access-code`
- `<driver>:<profile>:tls-fingerprint` (when TLS pinning is in use)

---

## 5. Protocol tracing

Many network commands support:

```bash
--protocol-trace /path/to/trace.jsonl
```

Trace file behavior:

- Created with restrictive permissions
- Must not already exist
- JSON Lines format (one event per line)

Typical event fields include:

- `timestamp`
- `command`
- `driver`
- `operation`
- `phase`
- `transport`
- `endpoint`
- `byteCount`
- `durationMs`
- `protocol`
- `payload` (sanitized, no secrets)
- `warning`
- `errorCategory`

---

## 6. Printer profile management (`printer`)

## 6.1 `printer drivers`

List registered drivers.

```bash
polimero printer drivers
```

Sample human output:

```text
DRIVER      DESCRIPTION
bambu-lan   Bambu Lab printers over LAN mode
moonraker   Moonraker-compatible Klipper printers
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "drivers": [
      { "name": "bambu-lan", "description": "Bambu Lab printers over LAN mode" },
      { "name": "moonraker", "description": "Moonraker-compatible Klipper printers" }
    ]
  },
  "error": null,
  "meta": { "command": "printer drivers" }
}
```

## 6.2 `printer add <name>`

Add a printer profile and store secrets in keychain.

```bash
polimero printer add office-x1 \
  --driver bambu-lan \
  --host 192.168.1.40 \
  --serial 01S00A123456789 \
  --timeout 10s \
  --access-code-file ~/.config/polimero/office-x1.access
```

Flags:

- `--driver` (required)
- `--host` (required)
- `--serial` (driver-dependent)
- `--timeout` (default `10s`)
- `--insecure` (skips TLS verification + connect check)
- `--access-code-file` (required in non-interactive sessions)
- `--protocol-trace`

Sample human output:

```text
Printer profile added: office-x1
Driver: bambu-lan
Host: 192.168.1.40
Serial: 01S00A123456789
TLS: sha256:0f4f6a8c4f0dd6d6a1c0c6fd3f8e9d72d52d4d2ff9c49be0f6bcb07f18db7a1d
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": {
      "name": "office-x1",
      "driver": "bambu-lan",
      "host": "192.168.1.40",
      "serial": "01S00A123456789",
      "timeout": "10s",
      "insecure": false,
      "tlsFingerprint": "sha256:0f4f6a8c4f0dd6d6a1c0c6fd3f8e9d72d52d4d2ff9c49be0f6bcb07f18db7a1d"
    }
  },
  "error": null,
  "meta": { "command": "printer add" }
}
```

## 6.3 `printer list`

```bash
polimero printer list
```

Sample human output:

```text
NAME       DRIVER      HOST          SERIAL            TIMEOUT  INSECURE
office-x1  bambu-lan   192.168.1.40  01S00A123456789  10s      false
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profiles": [
      {
        "name": "office-x1",
        "driver": "bambu-lan",
        "host": "192.168.1.40",
        "serial": "01S00A123456789",
        "timeout": "10s",
        "insecure": false
      }
    ]
  },
  "error": null,
  "meta": { "command": "printer list" }
}
```

## 6.4 `printer remove <name>`

Removes profile and associated keychain secrets.

```bash
polimero printer remove office-x1 --yes
```

Sample human output:

```text
Printer profile removed: office-x1
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "removed": {
      "name": "office-x1",
      "accessCodeRemoved": true,
      "tlsFingerprintRemoved": true
    },
    "warnings": []
  },
  "error": null,
  "meta": { "command": "printer remove" }
}
```

## 6.5 `printer discover`

Scan local network (driver capability gated).

```bash
polimero printer discover --timeout 5s
polimero printer discover --driver bambu-lan --timeout 8s
```

Sample human output:

```text
Discovered 1 printer(s) on the local network (4.9s):

  NAME                 SERIAL               MODEL    HOST               CONFIGURED
  Bambu X1C            01S00A123456789      X1C      192.168.1.40       office-x1
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "printers": [
      {
        "driver": "bambu-lan",
        "host": "192.168.1.40",
        "serial": "01S00A123456789",
        "model": "X1C",
        "name": "Bambu X1C",
        "configuredAs": "office-x1"
      }
    ],
    "count": 1
  },
  "error": null,
  "meta": {
    "command": "printer discover",
    "durationMs": 4921
  }
}
```

## 6.6 `printer tls refresh <name>`

Re-pin TLS cert fingerprint or set profile insecure mode.

```bash
polimero printer tls refresh office-x1 --yes
polimero printer tls refresh office-x1 --insecure --yes
```

Sample human output (re-pin):

```text
TLS certificate re-pinned: office-x1
Fingerprint: sha256:df98fcb95c09f89b250e87fd8be0c85da71624c64c4ef5ca8b96b8a380f4f8fe
```

Sample human output (`--insecure`):

```text
TLS certificate verification disabled: office-x1
Warning: TLS verification is disabled for this profile.
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "fingerprint": "sha256:df98fcb95c09f89b250e87fd8be0c85da71624c64c4ef5ca8b96b8a380f4f8fe",
    "insecure": false
  },
  "error": null,
  "meta": {
    "command": "printer tls refresh",
    "durationMs": 331
  }
}
```

---

## 7. Status (`status <name>`)

Shows current printer state and telemetry.

```bash
polimero status office-x1
polimero status office-x1 --detailed
```

Flags:

- `--detailed`
- `--timeout`
- `--insecure`
- `--protocol-trace`

Sample human output:

```text
Printer: office-x1
State: printing
Progress: 42%
Nozzle: 216.4 C / 220.0 C
Bed: 59.8 C / 60.0 C
Job: benchy.3mf
```

Sample detailed human output:

```text
Printer: office-x1
State: printing
Stage: outer wall
Progress: 42% (layer 67 / 159)
Speed: standard
Time: 1h 12m elapsed, 1h 39m remaining
Nozzle: 216.4 C / 220.0 C
Bed: 59.8 C / 60.0 C
Fans:
  Part cooling: 80%
  Auxiliary: 40%
Wi-Fi: -54 dBm
Job: benchy.3mf (12.4 MB, 0.4mm nozzle)
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "state": "printing",
    "temperatures": {
      "nozzle": { "currentCelsius": 216.4, "targetCelsius": 220.0 },
      "bed": { "currentCelsius": 59.8, "targetCelsius": 60.0 }
    },
    "job": { "name": "benchy.3mf" },
    "progress": { "percent": 42, "currentLayer": 67, "totalLayers": 159 },
    "errors": [],
    "warnings": [],
    "capabilities": { "status": true, "fileList": true, "jobStart": true }
  },
  "error": null,
  "meta": {
    "command": "status",
    "durationMs": 227
  }
}
```

---

## 8. Camera operations (`camera`)

## 8.1 `camera stream <name>`

Starts a local HTTP stream proxy on loopback (`127.0.0.1`).

```bash
polimero camera stream office-x1
polimero camera stream office-x1 --port 8090 --timeout 30m
```

Sample human output:

```text
Streaming camera from office-x1
Format: MJPEG (open in browser)
URL: http://127.0.0.1:8080/stream

Press Ctrl+C to stop.
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "url": "http://127.0.0.1:8080/stream",
    "format": "mjpeg",
    "port": 8080
  },
  "error": null,
  "meta": { "command": "camera stream" }
}
```

## 8.2 `camera snapshot <name>`

Capture one still frame and save as JPEG.

```bash
polimero camera snapshot office-x1
polimero camera snapshot office-x1 --to ./snaps --overwrite
```

Sample human output:

```text
Snapshot saved to ./office-x1-2026-07-11T02-00-01.jpg (284.6 KiB).
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "path": "./office-x1-2026-07-11T02-00-01.jpg",
    "sizeBytes": 291423,
    "protocol": "rtsp"
  },
  "error": null,
  "meta": {
    "command": "camera snapshot",
    "durationMs": 462
  }
}
```

---

## 9. File operations (`files`)

Device path format:

```text
<root>:/path/inside/root
```

Example: `sdcard:/models/cube.3mf`

## 9.1 `files roots <printer>`

```bash
polimero files roots office-x1
```

Sample human output:

```text
Printer: office-x1

ROOT     WRITABLE  FREE       CAPACITY   DESCRIPTION
sdcard   true      7.4 GiB    8.0 GiB    SD Card
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "roots": [
      {
        "name": "sdcard",
        "description": "SD Card",
        "writable": true,
        "capacityBytes": 8589934592,
        "freeBytes": 7945689497,
        "metadata": {}
      }
    ],
    "warnings": [],
    "capabilities": { "fileList": true, "fileDownload": true, "fileUpload": true }
  },
  "error": null,
  "meta": { "command": "files roots", "durationMs": 117 }
}
```

## 9.2 `files list <printer> [<device-path>...]`

```bash
polimero files list office-x1
polimero files list office-x1 sdcard:/models --recursive
```

Sample human output:

```text
Printer: office-x1
Path: sdcard:/models

TYPE       SIZE      MODIFIED              NAME
file       12.4 MiB  2026-07-10 23:44 UTC  benchy.3mf
directory  -         2026-07-10 21:10 UTC  calibration
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "paths": [
      {
        "devicePath": "sdcard:/models",
        "entries": [
          {
            "name": "benchy.3mf",
            "root": "sdcard",
            "path": "/models/benchy.3mf",
            "devicePath": "sdcard:/models/benchy.3mf",
            "type": "file",
            "sizeBytes": 13002342,
            "modifiedAt": "2026-07-10T23:44:12Z",
            "metadata": {}
          }
        ]
      }
    ],
    "warnings": [],
    "capabilities": { "fileList": true, "fileDownload": true, "fileUpload": true }
  },
  "error": null,
  "meta": { "command": "files list", "durationMs": 142 }
}
```

## 9.3 `files download <printer> <device-path>`

```bash
polimero files download office-x1 sdcard:/models/benchy.3mf
polimero files download office-x1 sdcard:/models/benchy.3mf --to ./downloads --overwrite
```

Sample human output:

```text
Downloaded sdcard:/models/benchy.3mf to downloads/benchy.3mf (12.4 MiB).
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "source": "sdcard:/models/benchy.3mf",
    "destination": "./downloads/benchy.3mf",
    "bytesTransferred": 13002342,
    "warnings": [],
    "capabilities": { "fileDownload": true }
  },
  "error": null,
  "meta": { "command": "files download", "durationMs": 1587 }
}
```

## 9.4 `files upload <printer> <local-path> <device-path>`

```bash
polimero files upload office-x1 ./models/benchy.3mf sdcard:/models/
polimero files upload office-x1 ./models/benchy.3mf sdcard:/models/benchy.3mf --overwrite
```

Sample human output:

```text
Uploaded ./models/benchy.3mf to sdcard:/models/benchy.3mf (12.4 MiB).
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "source": "./models/benchy.3mf",
    "destination": "sdcard:/models/benchy.3mf",
    "bytesTransferred": 13002342,
    "warnings": [],
    "capabilities": { "fileUpload": true }
  },
  "error": null,
  "meta": { "command": "files upload", "durationMs": 1721 }
}
```

---

## 10. Job control (`jobs`)

All control actions are capability-gated and state-checked.

## 10.1 `jobs start <printer> <device-path>`

```bash
polimero jobs start office-x1 sdcard:/models/benchy.3mf --yes
polimero jobs start office-x1 sdcard:/models/multi-plate.3mf --plate 2 --skip-leveling --yes
```

Sample human output:

```text
Printer: office-x1
Job started.
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "action": "start",
    "devicePath": "sdcard:/models/benchy.3mf",
    "state": "printing",
    "warnings": [],
    "capabilities": { "jobStart": true, "jobPause": true, "jobResume": true, "jobCancel": true }
  },
  "error": null,
  "meta": { "command": "jobs start", "durationMs": 388 }
}
```

## 10.2 `jobs pause <printer>`

```bash
polimero jobs pause office-x1 --yes
```

Sample human output:

```text
Printer: office-x1
Job paused.
```

## 10.3 `jobs resume <printer>`

```bash
polimero jobs resume office-x1 --yes
```

Sample human output:

```text
Printer: office-x1
Job resumed.
```

## 10.4 `jobs cancel <printer>`

```bash
polimero jobs cancel office-x1 --yes
```

Sample human output:

```text
Printer: office-x1
Job canceled.
```

Representative JSON shape for pause/resume/cancel:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "action": "pause",
    "state": "paused",
    "warnings": [],
    "capabilities": { "jobPause": true }
  },
  "error": null,
  "meta": { "command": "jobs pause", "durationMs": 201 }
}
```

---

## 11. Temperature control (`temperature`)

## 11.1 `temperature set <printer>`

Set one or more targets. At least one of `--nozzle`, `--bed`, `--chamber` is required.

```bash
polimero temperature set office-x1 --nozzle 220 --bed 60 --yes
polimero temperature set office-x1 --chamber 40 --yes
```

Safety bounds enforced by CLI:

- nozzle: `0..300`
- bed: `0..120`
- chamber: `0..65`

Sample human output:

```text
Printer: office-x1
Nozzle target set to 220.0 C
Bed target set to 60.0 C
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "targets": {
      "nozzleCelsius": 220.0,
      "bedCelsius": 60.0,
      "chamberCelsius": null
    },
    "warnings": [],
    "capabilities": { "temperatureWrite": true }
  },
  "error": null,
  "meta": { "command": "temperature set", "durationMs": 173 }
}
```

---

## 12. Motion control (`motion`)

Motion commands require idle state and confirmation (unless `--yes`).

## 12.1 `motion home <printer>`

```bash
polimero motion home office-x1 --yes
polimero motion home office-x1 --axis x,y --yes
```

Sample human output:

```text
Printer: office-x1
Homing x, y, z...
Homing command accepted.
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "action": "home",
    "state": "accepted",
    "axes": ["x", "y", "z"],
    "warnings": [],
    "capabilities": { "motionControl": true }
  },
  "error": null,
  "meta": { "command": "motion home", "durationMs": 95 }
}
```

## 12.2 `motion jog <printer>`

Relative move with optional X/Y/Z deltas.

```bash
polimero motion jog office-x1 --x 2.0 --y -1.5 --feedrate 1800 --yes
```

Jog range per axis:

- `-10.0` to `+10.0` mm

Sample human output:

```text
Printer: office-x1
Jogging x+2.0mm y-1.5mm at 1800mm/min...
Jog command accepted.
```

Sample JSON output:

```json
{
  "ok": true,
  "data": {
    "profile": "office-x1",
    "driver": "bambu-lan",
    "action": "jog",
    "state": "accepted",
    "delta": {
      "xMillimeters": 2.0,
      "yMillimeters": -1.5,
      "zMillimeters": null,
      "feedrateMmPerMin": 1800
    },
    "warnings": [],
    "capabilities": { "motionControl": true }
  },
  "error": null,
  "meta": { "command": "motion jog", "durationMs": 86 }
}
```

---

## 13. Shell completion (`completion`)

Generate shell completion scripts:

```bash
polimero completion bash
polimero completion zsh
polimero completion fish
polimero completion powershell
```

Quick Bash one-shot:

```bash
source <(polimero completion bash)
```

---

## 14. Common workflows

## 14.1 Add printer and verify status

```bash
polimero printer add office-x1 --driver bambu-lan --host 192.168.1.40 --serial 01S00A123456789 --access-code-file ~/.secrets/office-x1.access
polimero status office-x1
```

## 14.2 Upload and start a print

```bash
polimero files upload office-x1 ./models/benchy.3mf sdcard:/models/
polimero jobs start office-x1 sdcard:/models/benchy.3mf --yes
```

## 14.3 Pause, resume, cancel

```bash
polimero jobs pause office-x1 --yes
polimero jobs resume office-x1 --yes
polimero jobs cancel office-x1 --yes
```

## 14.4 Capture diagnostics

```bash
polimero status office-x1 --output json --protocol-trace ./status-trace.jsonl
```

---

## 15. Error behavior and examples

Common error codes in JSON:

- `config_error`
- `authentication_failed`
- `secret_not_found`
- `connection_failed`
- `timeout`
- `capability_unsupported`
- `invalid_printer_state`
- `unsafe_value`

Sample usage/config error:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "config_error",
    "message": "profile name is required",
    "details": {}
  },
  "meta": { "command": "jobs pause" }
}
```

Sample capability error:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "capability_unsupported",
    "message": "driver \"moonraker\" does not support camera snapshot",
    "details": {}
  },
  "meta": { "command": "camera snapshot" }
}
```

Sample invalid state error:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "invalid_printer_state",
    "message": "cannot perform action: printer is printing, expected idle",
    "details": {
      "profile": "office-x1",
      "currentState": "printing",
      "requiredState": "idle"
    }
  },
  "meta": { "command": "jobs start" }
}
```

---

## 16. Full command reference (quick index)

```text
polimero
polimero --version
polimero completion <bash|fish|zsh|powershell>

polimero printer drivers
polimero printer list
polimero printer add <name>
polimero printer remove <name>
polimero printer discover
polimero printer tls refresh <name>

polimero status <name>

polimero camera stream <name>
polimero camera snapshot <name>

polimero files roots <printer>
polimero files list <printer> [<device-path>...]
polimero files download <printer> <device-path>
polimero files upload <printer> <local-path> <device-path>

polimero jobs start <printer> <device-path>
polimero jobs pause <printer>
polimero jobs resume <printer>
polimero jobs cancel <printer>

polimero temperature set <printer>

polimero motion home <printer>
polimero motion jog <printer>

polimero fans set <printer> <fan> <percent>
polimero lights set <printer> <light> <state>
polimero speed set <printer> <profile>
```
