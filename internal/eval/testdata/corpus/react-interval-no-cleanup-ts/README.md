# Fixture: react-interval-no-cleanup-ts

**Category:** resource-leak · **Language:** TypeScript · **Class:** framework-idiomatic

A polling hook starts `setInterval` inside `useEffect` and returns no cleanup function. Every
unmount (route change) leaves a timer running that keeps calling `setState` on an unmounted component and
keeps polling the API. Nearby code that is correct (the one-shot timeout with cleanup) must stay silent.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 19 lines.
