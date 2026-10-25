# Fixture: listener-leak-per-request-js

**Category:** resource-leak · **Language:** JavaScript

The SSE endpoint registers an `events.on("update", ...)` listener per connection but never
removes it when the client disconnects. Every reconnect adds another listener to the shared emitter:
memory grows without bound, dead sockets are written to (`write after end`), and Node eventually prints
`MaxListenersExceededWarning`.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 8 lines.
