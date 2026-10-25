# Fixture: md5-password-hash-php
**Category:** security · **Language:** PHP

Password storage is changed from `password_hash` to unsalted `md5`, which is trivially reversible with rainbow tables.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
