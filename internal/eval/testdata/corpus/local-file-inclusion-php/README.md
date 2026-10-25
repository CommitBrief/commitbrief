# Fixture: local-file-inclusion-php
**Category:** security · **Language:** PHP

A front controller `include`s a file whose name comes from `$_GET['page']`, allowing local file inclusion (and code execution via log or upload poisoning).

**Provenance:** hand-authored planted defect (ADR-0018 §1).
