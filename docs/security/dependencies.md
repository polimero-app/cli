# Dependency Posture

This document records the audit posture for direct dependencies, per the
supply-chain controls in `threat-model.md` and the dependency rules in
`AGENTS.md` (purpose, license, maintenance status, vulnerability/audit
posture). Update it when adding, upgrading, or removing a direct dependency.

## Direct Dependencies

| Module | Purpose | License | Maintenance |
| --- | --- | --- | --- |
| `github.com/spf13/cobra` | CLI command framework | Apache-2.0 | Active |
| `github.com/eclipse/paho.mqtt.golang` | Bambu LAN MQTT transport | EPL-2.0 / EDL-1.0 (dual) | Active |
| `github.com/bluenviron/gortsplib/v5` | Bambu LAN RTSPS camera streaming | MIT | Active |
| `github.com/bluenviron/mediacommon/v2` | Media format helpers for camera streaming | MIT | Active |
| `github.com/pion/rtp` | RTP packet handling for camera streaming | MIT | Active |
| `github.com/grandcat/zeroconf` | mDNS/DNS-SD discovery (`printer discover`) | MIT | **Unmaintained — see risk record** |
| `github.com/zalando/go-keyring` | OS keychain secret storage | MIT | Active |
| `golang.org/x/term` | Hidden TTY prompts | BSD-3-Clause | Active (Go team) |
| `gopkg.in/yaml.v3` | Versioned YAML config parsing | Apache-2.0 / MIT | Stable; effectively frozen upstream |

## Risk Record: grandcat/zeroconf

- **Status:** last release v1.0.0 (2020); repository has seen no maintenance
  since, and open issues/PRs are unaddressed.
- **Transitive pin:** requires `github.com/miekg/dns v1.1.27` (2019). The
  current upstream of miekg/dns is far ahead (v1.1.7x); staying on the old
  line means DNS parser fixes land here only if Go module consumers force an
  upgrade, which zeroconf does not.
- **Exposure:** used only by the Bambu LAN driver's `Discover()` for the
  opt-in `printer discover` command. Discovery parses untrusted multicast
  packets from the local network, so parser robustness matters. Ordinary
  commands never perform discovery or open mDNS sockets (see
  `docs/adr/0004-security-baseline.md`), which bounds the exposure to
  explicit user-initiated scans.
- **Decision:** risk accepted for the current slice. The dependency is
  isolated behind the driver boundary and a single import site, so a swap is
  low-cost when needed.
- **Follow-up:** evaluate a maintained fork (for example `libp2p/zeroconf`,
  which tracks newer miekg/dns releases) or force a newer `miekg/dns` via a
  module requirement once compatibility is verified. Adopt vulnerability
  scanning (`govulncheck`) in CI so this and other pins are checked
  continuously.
