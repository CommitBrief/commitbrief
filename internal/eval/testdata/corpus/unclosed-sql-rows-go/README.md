# Fixture: unclosed-sql-rows-go
**Category:** resource-leak + error-handling · **Language:** Go
db.Query returns a *sql.Rows that is never closed (line 9, leaking a connection) and the rows.Scan error is discarded into _ (line 16); both are expected findings. Hand-authored planted defect (ADR-0018 §1).
