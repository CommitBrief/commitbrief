# Fixture: defer-in-loop-go

**Category:** resource-leak · **Language:** Go · **Class:** framework-idiomatic

`LoadAll` opens every path in a loop and registers `defer f.Close()` inside the loop body. Deferred
calls run when the *function* returns, not at the end of each iteration, so all files stay open until
`LoadAll` finishes — a large directory exhausts the process file-descriptor limit.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 25 lines.
