package render

import (
	"fmt"
	"io"
	"strings"
)

func Markdown(w io.Writer, p Payload) error {
	content := p.Content
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if _, err := io.WriteString(w, content); err != nil {
		return fmt.Errorf("render: write markdown: %w", err)
	}
	if p.Verbose {
		if _, err := io.WriteString(w, VerboseFooter(p.Meta)); err != nil {
			return fmt.Errorf("render: write footer: %w", err)
		}
	}
	return nil
}
