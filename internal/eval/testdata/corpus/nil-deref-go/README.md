# Fixture: nil-deref-go

**Category:** correctness · **Language:** Go

`TouchEntry` calls `lookup`, which returns `nil` on a cache miss, then
dereferences the result (`e.AccessedAt = ...`) with no nil check — a
guaranteed panic on any absent key. A competent review must flag the
dereference (new-file line 24) at `high` severity or above.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Representative
of the "map lookup returns zero/nil, used unchecked" class.
