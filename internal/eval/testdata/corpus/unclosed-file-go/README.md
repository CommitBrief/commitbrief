# Fixture: unclosed-file-go
**Category:** resource-leak + correctness · **Language:** Go
os.Open returns an *os.File that is never closed (line 8, fd leak) and the single fixed-1024-byte f.Read silently truncates larger files despite the ReadAll name (line 13); both are expected findings. Hand-authored planted defect (ADR-0018 §1).
