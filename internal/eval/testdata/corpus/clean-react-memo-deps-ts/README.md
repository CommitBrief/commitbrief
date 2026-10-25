# Fixture: clean-react-memo-deps-ts

**Category:** (none — clean diff) · **Language:** TypeScript

Adds `useMemo` and `useCallback` with *complete* dependency arrays to a list component. It is a
clean control for the React hook-dependency class: a reviewer that pattern-matches on "hooks with
dependency arrays" must not raise a false alarm here. A good review must stay **silent**.

**Provenance:** hand-authored clean control (ADR-0018 §1).
