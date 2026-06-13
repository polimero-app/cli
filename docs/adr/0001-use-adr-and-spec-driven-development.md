# ADR 0001: Use ADR and Spec Driven Development

## Status

Accepted

## Context

Polimero will control physical 3D printers. Incorrect behavior can leak credentials, send unintended commands, start unsafe operations, or mislead operators about printer state. The project must support multiple printer brands through drivers while keeping the CLI behavior consistent.

The project will be created incrementally. Without a decision record and command contract for each increment, implementation details could become undocumented public interfaces.

## Decision

Polimero will use ADR and spec driven development.

Before implementation:

- Architectural and security decisions must be captured in ADRs.
- Every command must have a command contract spec.
- Every command implementation must reference an accepted ADR or an accepted ADR that already covers the decision.
- Command specs must define syntax, flags, config, secrets, output, errors, security behavior, tests, and non-goals.

ADR status values are:

- `Proposed`
- `Accepted`
- `Superseded`

## Consequences

- Initial development is slower, but behavior is explicit before code exists.
- Command tests can be derived from specs.
- Security-sensitive behavior is reviewed before implementation.
- Public CLI and JSON contracts are less likely to drift accidentally.

