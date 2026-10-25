# Fixture: promise-not-returned-js

**Category:** error-handling · **Language:** JavaScript

Inside a `.then` chain the audit-log write is started but not returned. The chain continues
immediately, so the caller's `await` resolves before the audit row exists, ordering guarantees are lost,
and a failing `audit.record` is an unhandled rejection that nobody can catch. The first `.then` that
returns its promise is correct.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 11 lines.
