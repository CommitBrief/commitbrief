// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/CommitBrief/commitbrief/internal/alias"
	"github.com/CommitBrief/commitbrief/internal/i18n"
)

// AliasOptions drives RunAlias, the interactive half of
// `commitbrief setup --alias`.
type AliasOptions struct {
	// Name is the alias to install. Empty means "ask" (when Interactive)
	// or "use alias.DefaultName" (when not).
	Name string

	// Interactive enables the huh prompts (shell picker, name input,
	// conflict choice). The CLI sets it only on a TTY.
	Interactive bool

	// AutoYes (from --yes) auto-proceeds past a detected conflict instead
	// of asking.
	AutoYes bool

	// Catalog localises the prompts and the conflict notice.
	Catalog *i18n.Catalog

	// Shell forces a specific installer by machine name ("zsh", "cmd", …);
	// empty means detect from the environment. Used by tests.
	Shell string
}

// AliasOutcome reports what RunAlias did so the CLI can print the localised
// result lines.
type AliasOutcome struct {
	Shell     string // installer machine name actually used
	Name      string // alias name installed
	Path      string // where it was written
	Changed   bool   // false when the block was already identical
	ReloadCmd string // shell reload command, or "" ⇒ suggest a restart
	Conflict  string // non-empty when a conflict was detected and overridden
	Canceled  bool   // user chose to cancel at the conflict prompt
}

// conflictAction is the user's decision at the conflict prompt.
type conflictAction int

const (
	conflictProceed conflictAction = iota
	conflictAnother
	conflictCancel
)

// RunAlias resolves the target shell, the alias name, checks for a conflict,
// and installs the alias. It performs no direct stdout I/O beyond the huh
// prompts — the CLI prints the localised result from the returned outcome.
func RunAlias(ctx context.Context, opts AliasOptions) (AliasOutcome, error) {
	inst, err := resolveInstaller(ctx, opts)
	if err != nil {
		return AliasOutcome{}, err
	}

	name := opts.Name
	if name == "" {
		if opts.Interactive {
			if name, err = promptAliasName(ctx, opts.Catalog); err != nil {
				return AliasOutcome{}, err
			}
		} else {
			name = alias.DefaultName
		}
	}

	// Validate, conflict-check, and (interactively) allow re-entry until the
	// name is acceptable or the user cancels.
	for {
		if !alias.IsValidName(name) {
			if !opts.Interactive {
				return AliasOutcome{}, fmt.Errorf("%s", tr(opts.Catalog, "setup.alias.invalid_name", "invalid alias name; use letters, digits, underscore or hyphen"))
			}
			if name, err = promptAliasName(ctx, opts.Catalog); err != nil {
				return AliasOutcome{}, err
			}
			continue
		}

		reason, err := inst.Conflict(name)
		if err != nil {
			return AliasOutcome{}, err
		}
		if reason == "" {
			break
		}

		// Conflict. --yes proceeds; interactive asks; non-interactive
		// without --yes proceeds but surfaces the warning in the outcome.
		if opts.AutoYes || !opts.Interactive {
			return install(inst, name, reason)
		}
		switch action, err := promptConflict(ctx, opts.Catalog, name, reason); {
		case err != nil:
			return AliasOutcome{}, err
		case action == conflictCancel:
			return AliasOutcome{Canceled: true}, nil
		case action == conflictAnother:
			if name, err = promptAliasName(ctx, opts.Catalog); err != nil {
				return AliasOutcome{}, err
			}
			continue
		default: // conflictProceed
			return install(inst, name, reason)
		}
	}

	return install(inst, name, "")
}

func install(inst alias.Installer, name, conflict string) (AliasOutcome, error) {
	changed, reload, err := inst.Install(name)
	if err != nil {
		return AliasOutcome{}, err
	}
	return AliasOutcome{
		Shell:     inst.Name(),
		Name:      name,
		Path:      inst.Target(),
		Changed:   changed,
		ReloadCmd: reload,
		Conflict:  conflict,
	}, nil
}

// resolveInstaller honours opts.Shell, else detects from the environment,
// else (interactive only) shows a picker. A non-interactive run with no
// detectable shell errors.
func resolveInstaller(ctx context.Context, opts AliasOptions) (alias.Installer, error) {
	if opts.Shell != "" {
		inst, ok := alias.ByName(opts.Shell)
		if !ok {
			return nil, fmt.Errorf("setup: unsupported shell %q on this OS", opts.Shell)
		}
		return inst, nil
	}
	if inst, ok := alias.Detect(); ok {
		return inst, nil
	}
	if !opts.Interactive {
		return nil, fmt.Errorf("%s", tr(opts.Catalog, "setup.alias.no_shell", "could not detect your shell; re-run on a terminal or set $SHELL"))
	}
	return pickInstaller(ctx, alias.All(), opts.Catalog)
}

func pickInstaller(ctx context.Context, installers []alias.Installer, cat *i18n.Catalog) (alias.Installer, error) {
	options := make([]huh.Option[string], 0, len(installers))
	for _, inst := range installers {
		options = append(options, huh.NewOption(inst.Label(), inst.Name()))
	}
	var chosen string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(tr(cat, "setup.alias.shell_prompt", "Which shell should the alias be added to?")).
			Options(options...).
			Value(&chosen),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return nil, err
	}
	inst, ok := alias.ByName(chosen)
	if !ok {
		return nil, fmt.Errorf("setup: unknown shell %q", chosen)
	}
	return inst, nil
}

// promptAliasName asks for the alias, defaulting to alias.DefaultName when
// the user leaves it blank.
func promptAliasName(ctx context.Context, cat *i18n.Catalog) (string, error) {
	value := ""
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title(tr(cat, "setup.alias.prompt", fmt.Sprintf("What would you like to use as the alias? (default: %s):", alias.DefaultName))).
			Placeholder(alias.DefaultName).
			Value(&value),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return "", err
	}
	if value == "" {
		return alias.DefaultName, nil
	}
	return value, nil
}

// promptConflict warns that the name is taken and asks how to proceed.
func promptConflict(ctx context.Context, cat *i18n.Catalog, name, reason string) (conflictAction, error) {
	var choice conflictAction
	title := fmt.Sprintf(tr(cat, "setup.alias.conflict_warn", "Alias %q already appears to be in use (%s)."), name, reason)
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[conflictAction]().
			Title(title).
			Description(tr(cat, "setup.alias.conflict_prompt", "How would you like to proceed?")).
			Options(
				huh.NewOption(tr(cat, "setup.alias.conflict_proceed", "Use it anyway"), conflictProceed),
				huh.NewOption(tr(cat, "setup.alias.conflict_another", "Choose a different name"), conflictAnother),
				huh.NewOption(tr(cat, "setup.alias.conflict_cancel", "Cancel"), conflictCancel),
			).
			Value(&choice),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return conflictCancel, err
	}
	return choice, nil
}
