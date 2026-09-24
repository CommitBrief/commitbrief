# Fixture: clean-add-test-python
**Category:** (none — clean diff) · **Language:** Python

Adds a unit test for an existing, already-correct function, using
`pytest.approx` for the float assertions instead of `==` so the test itself
doesn't trip a legitimate float-equality finding. No production code
changes. A good review must stay **silent**.

**Provenance:** hand-authored clean control (ADR-0018 §1).
