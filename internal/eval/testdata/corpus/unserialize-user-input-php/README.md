# Fixture: unserialize-user-input-php
**Category:** security · **Language:** PHP

A cookie value is passed to `unserialize()`. Crafted payloads instantiate arbitrary classes and trigger magic methods (PHP object injection). The previous code used `json_decode`.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
