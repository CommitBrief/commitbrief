# Fixture: sql-injection-go

**Category:** security · **Language:** Go

A new `FindUserByName` method builds its SQL statement by concatenating an
untrusted `name` argument straight into the query string — a textbook SQL
injection. A competent review must flag the concatenation line (new-file
line 14) at `high` severity or above and suggest a parameterized query.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Representative
of the `database/sql` string-building anti-pattern.
