# Fixture: path-traversal-download-java
**Category:** security · **Language:** Java

A download endpoint resolves a user-supplied filename against the storage directory without normalising or checking containment, so `../../etc/passwd` escapes the directory.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
