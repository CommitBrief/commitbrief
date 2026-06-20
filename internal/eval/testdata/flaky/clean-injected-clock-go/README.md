# Clean control (held-out): clean-injected-clock-go

**Category:** clean · **Language:** Go · **Held-out:** yes

The correct way to test time-dependent and collaborator-heavy code: inject a
`fakeClock` and assert against a *fixed* time (`fixed.Add(time.Hour)`), and use
a single fake API rather than mocking everything. The detector must produce
zero findings — neither `time-dependency` (no `time.Now()` in the assertion
path) nor `over-mock` (well under threshold). Part of the held-out
generalization slice (ADR-0018 §Goodhart). Clean control for the deterministic
flaky eval slice.
