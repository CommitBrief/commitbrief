# Fixture: sql-injection-pdo-query-php
**Category:** security · **Language:** PHP

A repository interpolates `$_GET['id']`-derived input directly into a SQL string and runs it with `PDO::query`. A prepared statement is required.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
