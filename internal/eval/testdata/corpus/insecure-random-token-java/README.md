# Fixture: insecure-random-token-java
**Category:** security · **Language:** Java

Password-reset tokens are generated with `java.util.Random`, which is a predictable LCG; an attacker who observes a few tokens can predict the next ones.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
