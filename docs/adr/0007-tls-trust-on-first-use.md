# ADR 0007: TLS Trust On First Use

## Status

Accepted

## Context

Bambu printers present self-signed TLS certificates issued by a Bambu Lab CA that is not present in any OS trust store. Polimero must protect against man-in-the-middle attacks on the local network without requiring users to install certificates into their OS trust store or accept insecure connections silently.

Standard TLS chain verification is not usable: the issuing CA is unknown to the OS. Pinning the Bambu CA certificate is fragile because Bambu may rotate it across firmware versions. The appropriate model for a trusted local device is Trust On First Use (TOFU): trust what is presented on first contact, pin it, and verify on every subsequent contact.

## Decision

Polimero will use Trust On First Use (TOFU) for Bambu LAN TLS connections.

### TLS Configuration

On every connection to a Bambu printer:

- TLS chain verification is **skipped**. Bambu printers present self-signed certificates not in OS trust stores; chain verification would always fail and provides no meaningful security here.
- The **SHA-256 fingerprint of the DER-encoded leaf certificate** is computed from the TLS handshake. The leaf certificate is the certificate presented by the server, not any intermediate or root.
- Fingerprint format:

```text
sha256:<lowercase-hex-string>
```

- The TLS SNI field is set to the printer's **serial number** (from the profile `serial` field). Bambu printer certificates have a CN equal to the printer serial number. Setting SNI to the serial allows the printer to present the correct certificate when the connection is made to an IP address.

### First Connection

`printer add` always connects to the printer before storing the profile. This connection performs the full MQTT handshake (TLS + MQTT CONNECT) to confirm both TLS reachability and access code validity. During the TLS handshake, Polimero captures the SHA-256 fingerprint of the leaf certificate and stores it in the OS keychain.

Keychain entry:

- Service: `polimero`
- Account: `bambu-lan:<name>:tls-fingerprint`

The profile is not stored if the connection fails or if MQTT authentication is rejected.

### Subsequent Connections

On each network command, Polimero loads the pinned fingerprint from the keychain and verifies that the presented leaf certificate's SHA-256 fingerprint matches. A fingerprint mismatch is treated as an authentication failure and exits with code `3`.

### Insecure Mode

`--insecure` may be passed to `printer add`, `printer status`, or `printer tls refresh`.

When `--insecure` is passed to `printer add`:

- No TLS connection or MQTT auth check is performed.
- No fingerprint is stored.
- The profile is stored with `insecure: true`.
- Human output includes a warning line.

When `--insecure` is passed to `printer status`:

- TLS verification is skipped for that invocation regardless of the profile setting.

When `--insecure` is passed to `printer tls refresh`:

- TLS verification is skipped for the reconnection.
- The stored fingerprint is removed from the keychain.
- The profile is updated to `insecure: true`.

### Certificate Recovery

When a printer certificate changes legitimately, the user runs `printer tls refresh <name>` to re-pin the certificate. `printer add` is not permitted to overwrite existing profiles.

## Consequences

- Users whose printers rotate certificates on firmware updates must run `printer tls refresh`.
- Headless environments without a valid printer certificate may require `--insecure`.
- `printer add` requires network connectivity to the printer and a valid access code.
- The OS keychain must be available at `printer add` time. If unavailable, the command fails closed.
- Rollback at `printer add` time must clean up both the access code and the TLS fingerprint keychain entries if a later step fails.
- Connections to IP addresses (not hostnames) require explicit SNI configuration; the TLS library must be configured to send SNI equal to the serial number rather than deriving it from the connection target.
