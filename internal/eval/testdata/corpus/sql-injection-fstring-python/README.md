# Fixture: sql-injection-fstring-python
**Category:** security · **Language:** Python

A search helper builds its SQL with an f-string around a request parameter and passes it straight to `cursor.execute`. Classic SQL injection; a parameterised query is required.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
