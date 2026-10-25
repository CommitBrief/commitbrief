# Fixture: jwt-no-signature-verify-python
**Category:** security · **Language:** Python

Token decoding is changed to skip signature verification to "fix" a key-rotation error. Any forged token is now accepted as authentic.

**Provenance:** hand-authored planted defect (ADR-0018 §1).
