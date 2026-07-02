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
- Symbolic links are rejected where the operating system supports no-follow opens.
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

Secret-store operations must accept `context.Context`. Commands with explicit timeouts must use those timeouts for secret-store reads or writes on the critical path; commands without a user-facing timeout must still use a bounded internal timeout for secret cleanup.

## TLS Fingerprint

The TLS fingerprint is the SHA-256 digest of the DER-encoded leaf certificate, formatted as:

```text
sha256:<64 lowercase hex characters>
```

The fingerprint is stored in the OS keychain and loaded on each network connection. If the stored fingerprint is empty, malformed, or does not match the presented certificate, the command fails with exit code `3`.

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

Authentication, transport, protocol parse, and secret-store backend errors must be rendered as sanitized categories. Raw backend causes may be preserved for internal error wrapping but must not appear in human output, JSON output, logs, tests, or fixtures.

## Protocol Trace Files

`--protocol-trace <file>` creates an explicit local diagnostic file for commands that perform printer protocol work.

Protocol trace files must:

- Be opt-in per command invocation.
- Be created before protocol work starts.
- Refuse to overwrite an existing file.
- Use owner-only read/write permissions (`0600`) on POSIX-like systems.
- Contain JSON Lines events with sanitized metadata only.
- Omit access codes, passwords, tokens, private keys, TLS private material, raw authentication payloads, protocol payloads containing credential material, transferred file contents, camera frames, decoded images, and unsanitized backend errors.

Protocol trace files may include profile names, driver names, printer host names or addresses, printer serial numbers, job names, file names, device paths, byte counts, durations, selected protocol names, response key inventories, parser warnings, sanitized error categories, and secret-free protocol payloads (MQTT command and report JSON, discovery records) per ADR 0013.

Trace files are more sensitive than ordinary command output because they can combine operational metadata across multiple protocol phases. They must never be automatically uploaded, attached to errors, embedded in JSON output, or written without an explicit `--protocol-trace` path.
