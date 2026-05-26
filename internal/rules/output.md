# CommitBrief Review Output Format

Format every review according to the structure below. The reviewer
content lives in `COMMITBRIEF.md`; this file controls only how findings
are presented.

## Findings

Group findings under the five perspective headings used by the review
content. For each finding, emit exactly these four labeled lines, in this
order, each on its own line:

```
**Severity:** high
**File:Line:** path/to/file.ext:42–48
**Issue:** one-sentence summary of what is wrong.
**Suggestion:** one to three sentences with a concrete fix or direction.
```

Never collapse fields onto a single line. Leave one blank line between
findings.

Severity definitions:

- `high` — must fix before merge: bug, security issue, data loss risk, or
  significant correctness or security regression.
- `medium` — should consider: code smell, fragility, non-obvious risk, or
  notable structural problem.
- `low` — optional polish: minor refactor, naming nit, style observation.

## Verdict

End every review with a one-paragraph **Verdict** section: overall safety
to ship, what should be addressed before commit, and what is optional polish.

<!--
This file is per-user. Place it at:

  ~/.commitbrief/OUTPUT.md            # applies to every repo for this user
  <repo>/.commitbrief/OUTPUT.md       # overrides the user-level file for this repo

Both locations are gitignored by `commitbrief setup --local` (the
`.commitbrief/` directory is added to .gitignore). Team-shared output
conventions belong in `COMMITBRIEF.md`, not here.

If absent at both locations, the binary-embedded default above is used.
The cache invalidates automatically when the resolved content changes.
-->
