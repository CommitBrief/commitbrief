# Fixture: error-suppression-operator-php
**Category:** error-handling · **Language:** PHP

The `@` operator is added in front of `file_put_contents`, and the method now returns `true` unconditionally. A failed write (disk full, permissions) is silently lost while callers believe the export was saved.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
