# Flaky fixture: flaky-over-mock-ts

**Rule:** `over-mock` · **Language:** TypeScript · **Severity:** low

A single `it(...)` test stubs six collaborators (three `jest.mock`, one
`jest.spyOn`, two `mock*Value` configs) — above the `overMockThreshold` of 5.
The file-scoped detector emits one finding anchored at the sixth
(threshold-crossing) setup. Known-answer fixture for the deterministic flaky
eval slice (ADR-0018).
