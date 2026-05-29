# Fixture: command-injection-go
**Category:** security · **Language:** Go
RunUserCommand passes untrusted input to `exec.Command("sh", "-c", userInput)`, enabling OS command injection. Hand-authored planted defect (ADR-0018 §1).
