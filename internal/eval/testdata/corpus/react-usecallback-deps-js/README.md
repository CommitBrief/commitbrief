# Fixture: react-usecallback-deps-js

**Category:** correctness · **Language:** JavaScript · **Class:** framework-idiomatic

`handleSubmit` is memoized with `useCallback` but its dependency array lists only `onDone`
while the callback reads `form` and `workspaceId`. After the first render the submit handler keeps
posting the *initial* form values. `handleCancel` (correct deps) must not be flagged.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 13 lines.
