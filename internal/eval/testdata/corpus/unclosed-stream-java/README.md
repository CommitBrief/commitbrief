# Fixture: unclosed-stream-java
**Category:** resource-leak · **Language:** Java

A file copy helper replaces try-with-resources with manual `close()` calls at the end of the method; if `write` throws, the streams are never closed.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
