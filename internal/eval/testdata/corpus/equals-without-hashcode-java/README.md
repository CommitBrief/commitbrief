# Fixture: equals-without-hashcode-java
**Category:** correctness · **Language:** Java

A value class that is used as a `HashMap` key gets an `equals` override but no matching `hashCode`. Equal keys land in different buckets, so lookups with a fresh-but-equal key miss.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
