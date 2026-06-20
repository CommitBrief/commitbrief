# Flaky fixture: flaky-time-dependency-go

**Rule:** `time-dependency` · **Language:** Go · **Severity:** low

Two assertions read the wall clock directly (`require.Equal(..., time.Now()...)`
and `assert.True(t, got.After(time.Now()))`) instead of an injected clock. The
detector flags only the assertion lines, not the `start := time.Now()` setup
capture above (a non-injected clock used outside an assertion path is not
flagged). Known-answer fixture for the deterministic flaky eval slice
(ADR-0018).
