# Fixture: waitgroup-misuse-go
**Category:** concurrency · **Language:** Go
wg.Add(1) is called inside the spawned goroutine instead of before the go statement, racing with wg.Wait(). Hand-authored planted defect (ADR-0018 §1).
