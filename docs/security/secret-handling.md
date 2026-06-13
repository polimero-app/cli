# Secret Handling

## Principles

- Secrets must not be stored in Polimero YAML config.
- Secrets must not appear in logs, output, errors, or command arguments.
- Secrets must be accessed only through a narrow secret-store abstraction.
- If secure storage is unavailable, fail closed.

## Supported Secret Storage

Polimero stores real printer secrets in OS keychain services:

- macOS Keychain.
- Windows Credential Manager.
- Linux Secret Service where available.

The implementation must provide mock secret stores for tests.

## Secret Input

Interactive input:

```text
Enter Bambu LAN access code for <name>:
```

- Input is hidden.
- Empty input is rejected.
- The value is stored directly in OS keychain.

File input:

```text
--access-code-file <path>
```

Requirements:

- Regular file.
- Maximum size 4 KiB.
- Current user can read it.
- Owner-only permissions where POSIX-like modes are available.
- Trim one trailing newline.
- Preserve other whitespace.
- Never log file contents.

Command-line secret flags such as `--access-code` are prohibited.

## Non-interactive Behavior

Commands must not prompt in non-interactive mode.

If a command needs a new secret in non-interactive mode, it must receive `--access-code-file` or fail with a usage/config error.

Environment-variable secret injection is allowed only under test or integration build tags, or an explicit test mode.

## Secret Naming

Each profile stores up to two keychain entries:

| Secret | Service | Account |
|---|---|---|
| Printer access code | `polimero` | `<driver>:<name>:access-code` |
| TLS certificate fingerprint | `polimero` | `<driver>:<name>:tls-fingerprint` |

Example for profile `garage-x1c` using driver `bambu-lan`:

- `polimero` / `bambu-lan:garage-x1c:access-code`
- `polimero` / `bambu-lan:garage-x1c:tls-fingerprint`

The TLS fingerprint is stored as `sha256:<lowercase-hex-string>`.

Profiles created with `--insecure` do not store a TLS fingerprint entry.

## TLS Fingerprint

The TLS fingerprint is the SHA-256 digest of the DER-encoded leaf certificate, formatted as:

```text
sha256:<lowercase-hex-string>
```

The fingerprint is stored in the OS keychain and loaded on each network connection. If the presented certificate does not match the pinned fingerprint, the command fails with exit code `3`.

## Rotation

Access code rotation is not handled by `printer add`.

A later command spec, `printer secret set <name>`, will define rotation behavior.

## Logging And Errors

Logs and errors must redact:

- Access codes.
- Tokens.
- Passwords.
- Raw auth payloads.
- Private keys or certificates.

Sanitized errors may include:

- Profile name.
- Driver name.
- Error category.
- Timeout value.

Sanitized errors must not include credential values or sensitive protocol payloads.

