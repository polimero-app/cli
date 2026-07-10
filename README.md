# Polimero CLI

Polimero is a command line interface for interacting with 3D printers through brand-specific drivers behind a common command surface.

The project is intentionally ADR and spec driven. Initial commands are implemented, and new behavior should continue to land incrementally, one command at a time, after the relevant ADRs and command specs are accepted.

## Current Status

This repository contains the ADR/spec foundation plus the first implemented printer-management and read-command slices.

Implemented commands currently include `printer add`, `printer list`, `printer remove`, `printer drivers`, `printer discover`, `printer tls refresh`, top-level `status`, `camera stream`, `camera snapshot`, `files roots`, `files list`, `files download`, `files upload`, `jobs start`, `jobs pause`, `jobs resume`, `jobs cancel`, `temperature set`, `motion home`, and `motion jog`.

Implemented drivers: `bambu-lan`, `moonraker`.

## Key Decisions

- Language: Go
- Module: `github.com/polimero-app/cli`
- CLI stack: Cobra
- License: AGPL-3.0-only
- First driver: Bambu LAN
- First read command: `polimero status <name>`
- Secret storage: OS keychain first
- Config storage: versioned YAML under `os.UserConfigDir`

## Global Flags

- `--output <format>`: output format. Values: `human`, `json`. Default: `human`.
- `--verbose`, `-v`: show detailed progress output in human mode. Verbose lines are suppressed when `--output json` is used.

## Documentation

- [PLAN.md](PLAN.md)
- [ADRs](docs/adr)
- [Command specs](docs/specs/commands)
- [Driver specs](docs/specs/drivers)
- [Security docs](docs/security)

## Development

Expected verification targets:

```sh
make test
make test-race
make lint
make ci
```

`make ci` runs the gofmt check, tests, race tests, and linting when the relevant tools are available.

H.264 camera snapshots use FFmpeg libraries through cgo for frame decoding and JPEG encoding. Development builds of that path require `pkg-config`, a C compiler, and FFmpeg development packages for `libavcodec`, `libavutil`, and `libswscale`. The project links against system FFmpeg libraries rather than vendoring codec code, so packagers should use maintained distro packages and verify their selected FFmpeg license configuration.

Release builds:

```sh
make release
```

This writes raw binaries to `dist/` for:
- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`

Artifact names follow `polimero_<os>_<arch>` (`.exe` suffix on Windows). Tag-triggered release builds inject version metadata from the git tag into `cmd.Version`.

## References

### Bambu LAN

* https://github.com/BambuTools/bambulabs_api
* https://github.com/ClusterM/open-bamboo-networking
* https://github.com/Doridian/OpenBambuAPI
* https://github.com/Keralots/BambuHelper
* https://github.com/maziggy/bambuddy

### Moonraker

* https://moonraker.readthedocs.io/
