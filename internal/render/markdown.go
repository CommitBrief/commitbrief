package render

import (
	"fmt"
	"io"
	"strings"
	"text/template"
)

// Markdown writes a plain-markdown rendering of the review. Under ADR-0014
// the renderer runs the user's OUTPUT.md as a Go text/template against the
// parsed Findings; it never sees the system prompt or any LLM-side
// formatting. Graceful degrade paths:
//
//   - p.Findings == nil  → emit p.Content verbatim (parse failed upstream).
//   - p.OutputTemplate == "" → emit p.Content verbatim (no template loaded).
//   - template execute fails → return the error; pre-send validation
//     (ADR-0014 §5) should have caught this, so if we land here a regression
//     occurred and the caller deserves to see it rather than silently render
//     half-formed output.
func Markdown(w io.Writer, p Payload) error {
	body, err := renderMarkdownBody(p)
	if err != nil {
		return err
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	if _, err := io.WriteString(w, body); err != nil {
		return fmt.Errorf("render: write markdown: %w", err)
	}
	if p.Verbose {
		if _, err := io.WriteString(w, VerboseFooter(p.Meta)); err != nil {
			return fmt.Errorf("render: write footer: %w", err)
		}
	}
	return nil
}

func renderMarkdownBody(p Payload) (string, error) {
	// Degrade or no-template path: emit raw Content directly.
	if p.Findings == nil || p.OutputTemplate == "" {
		return p.Content, nil
	}
	t, err := template.New("output").Funcs(TemplateFuncs()).Parse(p.OutputTemplate)
	if err != nil {
		return "", fmt.Errorf("render: parse OUTPUT.md template: %w", err)
	}
	var sb strings.Builder
	if err := t.Execute(&sb, TemplateData{Findings: p.Findings}); err != nil {
		return "", fmt.Errorf("render: execute OUTPUT.md template: %w", err)
	}
	return sb.String(), nil
}
