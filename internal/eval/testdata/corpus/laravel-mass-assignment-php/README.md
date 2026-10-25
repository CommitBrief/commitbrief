# Fixture: laravel-mass-assignment-php
**Category:** security · **Language:** PHP

A Laravel registration controller calls `User::unguard()` and then `User::create($request->all())`. The `users` table has an `is_admin` column, so a client can POST `is_admin=1` and promote themselves. The whole defect is in this one file.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
