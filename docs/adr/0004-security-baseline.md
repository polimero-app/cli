# ADR 0004: Security Baseline

## Status

Accepted

## Context

Polimero interacts with networked physical devices. It handles printer credentials and may later issue state-changing commands such as printing, heating, moving axes, or canceling jobs.

Security must be a design constraint rather than a later hardening pass.

## Decision

Polimero will follow these security defaults:

- Do not log secrets.
- Do not store secrets in config files.
- Sanitize user-facing errors.
- Use bounded network calls.
- Use no retry by default for read-only network commands.
- Do not perform automatic network scans during ordinary commands.
- Allow discovery only through explicit discovery commands.
- Require exact confirmation behavior in specs for future state-changing commands.
- Do not silently downgrade transport security.
- Treat debug logs as sensitive diagnostics that still require redaction.

Default read-only network timeout is 10 seconds unless a command spec defines a stricter value.

## Consequences

- Some convenience features are intentionally deferred.
- Operators must configure printers explicitly before normal commands work.
- Future dangerous commands must spend design effort on prompts, non-interactive behavior, and bypass flags.

