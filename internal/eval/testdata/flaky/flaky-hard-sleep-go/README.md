# Flaky fixture: flaky-hard-sleep-go

**Rule:** `hard-sleep` · **Language:** Go · **Severity:** medium

A test waits for an asynchronous `drain` goroutine with a fixed
`time.Sleep(2 * time.Second)` before asserting the queue is empty. The static
flaky detector (ADR-0022) must flag the sleep at the added line. Known-answer
fixture for the deterministic flaky eval slice (ADR-0018).
