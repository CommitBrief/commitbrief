# Fixture: path-traversal-go
**Category:** security · **Language:** Go
ReadUserFile joins `userPath` to `baseDir` and reads it without cleaning, so `../` escapes the base directory. Hand-authored planted defect (ADR-0018 §1).
