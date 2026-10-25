# Fixture: goroutine-leak-chan-go

**Category:** resource-leak · **Language:** Go

`Next` runs a blocking `Poll` in a goroutine and waits for either the result or context
cancellation. The result channel is unbuffered, so when the context is cancelled first the select returns
and nobody ever receives: the goroutine blocks forever on `ch <- ev` once `Poll` returns. Every cancelled
call leaks a goroutine (and the event it holds). A one-element buffer fixes it.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 20 lines.
