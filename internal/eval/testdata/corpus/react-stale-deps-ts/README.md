# Fixture: react-stale-deps-ts

**Category:** correctness · **Language:** TypeScript · **Class:** framework-idiomatic

`UserCard` loads a user in `useEffect` with an empty dependency array, but the effect reads
`userId` and `onLoaded`. When the parent re-renders with a different `userId` the card keeps showing the
first user. The sibling effect further down has a correct dependency array and must not be flagged.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 28 lines.
