# Fixture: sql-injection-statement-java
**Category:** security · **Language:** Java

A DAO concatenates a request parameter into a SQL string and runs it through `Statement.executeQuery`. A `PreparedStatement` with a bound parameter is required.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
