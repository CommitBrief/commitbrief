# Fixture: jwt-secret-fallback-js

**Category:** security · **Language:** JavaScript

The token signer falls back to a hard-coded development secret when `JWT_SECRET` is unset.
A misconfigured production deploy (or a forgotten env var in a container) silently signs and accepts tokens
with a secret that is public in the repository, so anyone can mint admin tokens.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 8 lines.
