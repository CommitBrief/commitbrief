# Flaky fixture: flaky-brittle-selector-ts

**Rule:** `brittle-selector` · **Language:** TypeScript · **Severity:** low

A Playwright spec adds two fragile locators — an absolute-position XPath
(`//section[2]/form/button[1]`) and a CSS `:nth-child(3)` — alongside two
stable ones (`getByTestId`, `getByRole`). The detector must flag only the two
fragile lines and stay silent on the stable handles (embedded negative
controls). Known-answer fixture for the deterministic flaky eval slice
(ADR-0018).
