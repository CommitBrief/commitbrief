# Fixture: clean-optional-chaining-ts

**Category:** (none — clean diff) · **Language:** TypeScript

Behavior-preserving rewrite of nested `&&` guards to optional chaining and a `??` default.
`Address.city` and `Address.zip` are typed `string | undefined` (never `null`, and the old guards compare
against `undefined` explicitly rather than testing truthiness), so `??` is exactly equivalent to the old
ternaries, including for empty strings. A good review must stay **silent**.

**Provenance:** hand-authored clean control (ADR-0018 §1).
