# Fixture: cross-file-nonnull-bang-ts

**Category:** correctness · **Language:** TypeScript · **Class:** cross-file

`users.findById` is changed from throwing `NotFoundError` to returning `undefined`. The profile
handler, edited in the same diff, adds a non-null assertion `(await users.findById(id))!` and
dereferences `user.email`. The compiler is silenced, but a missing user now crashes with a TypeError (500)
instead of the 404 the old exception produced.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 19 lines.
