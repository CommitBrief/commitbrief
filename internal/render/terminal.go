// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"fmt"
	"io"

	"github.com/charmbracelet/glamour"
)

const terminalWordWrap = 100

func Terminal(w io.Writer, p Payload) error {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(terminalWordWrap),
	)
	if err != nil {
		return fmt.Errorf("render: glamour init: %w", err)
	}
	out, err := r.Render(p.Content)
	if err != nil {
		return fmt.Errorf("render: glamour render: %w", err)
	}
	if _, err := io.WriteString(w, out); err != nil {
		return fmt.Errorf("render: write: %w", err)
	}
	if p.Verbose {
		if _, err := io.WriteString(w, VerboseFooter(p.Meta)); err != nil {
			return fmt.Errorf("render: write footer: %w", err)
		}
	}
	return nil
}
