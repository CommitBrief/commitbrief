# Fixture: ctx-cancel-leak-go

**Category:** resource-leak · **Language:** Go · **Class:** framework-idiomatic

`Client.Get` derives a `context.WithTimeout` child context but only calls `cancel()` on the two
error paths. On the success path the cancel function is dropped, so the timer and the context's
registration with its parent live until the 5s deadline fires — under load every request pins a
timer. The idiomatic form is `defer cancel()` right after `WithTimeout`.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 21 lines.
