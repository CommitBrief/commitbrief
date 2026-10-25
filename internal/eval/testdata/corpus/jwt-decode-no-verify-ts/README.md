# Fixture: jwt-decode-no-verify-ts

**Category:** security · **Language:** TypeScript

The auth middleware was changed to call `jwt.decode` instead of `jwt.verify` (to "avoid the
extra crypto cost on every request"). `decode` does not check the signature, so anyone can forge a token
claiming any `sub` or `roles`.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 10 lines.
