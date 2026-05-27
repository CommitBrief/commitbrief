# Code Review Summary
{{- if .Findings }}
{{ len .Findings }} finding{{ if ne (len .Findings) 1 }}s{{ end }} across {{ countFiles .Findings }} file{{ if ne (countFiles .Findings) 1 }}s{{ end }}.
{{ range groupBySeverity .Findings }}
## {{ upper (printf "%s" .Severity) }} ({{ len .Items }})
{{ range .Items }}
### {{ .PathRef }} — {{ .Title }}

{{ .Description }}
{{- if .Snippet }}

```{{ .Language }}
{{ .Snippet }}
```
{{- end }}
{{- if .Suggestion }}

→ {{ .Suggestion }}
{{- end }}
{{ end }}
{{- end }}
{{- else }}
No findings. Looks good.
{{- end }}

<!--
This file is a Go text/template applied locally to the LLM's findings.
It is never sent to the LLM — the model produces a JSON document, and
this template shapes how `--markdown` and `--output <file>.md` render it.

Available data:
  .Findings  []Finding{ Severity, File, Line, LineEnd, Title, Description, Suggestion, Language, Snippet }

Per-finding methods (preferred over raw field access):
  .LineRef   returns "142" or "142-145" depending on whether LineEnd is set
  .PathRef   shortcut for File + ":" + LineRef; plain File when no line

Available functions:
  upper, lower
  groupBySeverity  []Finding -> []{ Severity, Items []Finding }   (critical → info, empty buckets dropped)
  countFiles       []Finding -> int                                (distinct file count)

Place this file at:
  ~/.commitbrief/OUTPUT.md            # applies to every repo for this user
  <repo>/.commitbrief/OUTPUT.md       # overrides the user-level file for this repo

Both locations are gitignored by `commitbrief setup --local`. Team-shared
output conventions belong in COMMITBRIEF.md (the system prompt), not here.

If absent at both locations, the binary-embedded default above is used.
`commitbrief init` writes this file from the embedded default; edit and
re-run reviews to see your customisations.
-->
