# Fixture: spring-transactional-self-invocation-java
**Category:** correctness · **Language:** Java

A Spring service calls its own `@Transactional(propagation = REQUIRES_NEW)` method via `this`. Self-invocation bypasses the transactional proxy, so the audit write runs in the caller's transaction and rolls back with it.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
