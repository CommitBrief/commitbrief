# Fixture: open-redirect-login-js

**Category:** security · **Language:** JavaScript

After login the handler redirects to the `next` query parameter verbatim. An attacker can send
a victim to `/login?next=https://evil.example/` and, after the real login page succeeds, the user lands on
an attacker-controlled site that looks like a session timeout. Medium: needs user interaction but is a
well-known phishing primitive.

**Provenance:** hand-authored planted defect (ADR-0018 §1). Diff size: 7 lines.
