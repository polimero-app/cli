# ADR 0003: Use Driver Adapter Architecture

## Status

Accepted

## Context

Polimero must provide one CLI surface for different printer brands. Individual brands may use different protocols, authentication methods, transport security models, capability sets, and firmware behaviors.

The command layer must stay stable even when brand-specific protocols differ.

## Decision

Polimero will use a driver adapter architecture.

- CLI commands depend on driver-neutral ports and services.
- Brand-specific implementation lives behind driver adapters.
- Bambu-specific types must not appear in command packages.
- Drivers expose capability metadata.
- Unsupported operations return a typed unsupported-capability error.
- Drivers must sanitize errors before they cross into command rendering.
- Driver operations must accept `context.Context`.

The initial driver-neutral contract is specified in `docs/specs/drivers/driver-contract.md`.

## Consequences

- New brands can be added without changing command syntax when capabilities match.
- Tests can mock driver ports without real hardware.
- Some brand-specific features may require later extension points instead of leaking into the generic command layer.

