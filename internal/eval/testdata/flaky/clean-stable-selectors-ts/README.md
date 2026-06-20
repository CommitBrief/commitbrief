# Clean control: clean-stable-selectors-ts

**Category:** clean · **Language:** TypeScript

The recommended Playwright/Cypress patterns: `getByRole`, `getByTestId` with a
condition-based `waitFor`, an aliased `cy.wait('@session')`, a `getByText`
assertion, and a `[data-test=...]` attribute selector. None is brittle, fixed,
or wall-clock dependent — the detector must produce zero findings. The
`must_stay_silent_on` anchors make every false positive measurable. Clean
control for the deterministic flaky eval slice (ADR-0018).
