# Fixture: unclosed-resp-body-go
**Category:** resource-leak · **Language:** Go
http.Get returns a response whose Body is never closed, leaking the connection. Hand-authored planted defect (ADR-0018 §1).
