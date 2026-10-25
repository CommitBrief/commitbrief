# Fixture: refactor-split-ratelimit-python
**Category:** correctness · **Language:** Python

A large refactor moves `SlidingWindowLimiter` and `TokenBucket` out of `app/util.py` into `app/ratelimit.py` (about 160 changed lines). The move is otherwise verbatim, but the copy of `allow()` changed `>=` to `>`, so a key can make `limit + 1` calls per window.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
