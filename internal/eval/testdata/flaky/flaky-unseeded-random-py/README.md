# Flaky fixture: flaky-unseeded-random-py

**Rule:** `unseeded-random` · **Language:** Python · **Severity:** low

Two unseeded `random.*` calls (`random.choice`, `random.randint`) feed
assertions, so the test passes or fails depending on the run. The detector must
flag both added lines. Known-answer fixture for the deterministic flaky eval
slice (ADR-0018).
