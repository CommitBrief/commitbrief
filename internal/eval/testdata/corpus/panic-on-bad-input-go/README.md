# Fixture: panic-on-bad-input-go
**Category:** error-handling · **Language:** Go
ParsePort panics on bad input instead of returning an error — twice: on an unparseable string (line 9) and on an out-of-range value (line 12); both are expected findings. Hand-authored planted defect (ADR-0018 §1).
