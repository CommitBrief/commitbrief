// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Scope says where a flag was declared, which is the only thing that
// distinguishes two flags with the same name.
type Scope string

const (
	// ScopeGlobal is a flag declared on a command's PersistentFlags: it
	// applies to that command and everything below it. Root's persistent set
	// is what users call "the global flags".
	ScopeGlobal Scope = "global"

	// ScopeCommand is a flag declared on a command's own Flags: it applies to
	// that one command only.
	ScopeCommand Scope = "command"
)

// mutuallyExclusiveAnnotation is the pflag annotation key cobra writes when
// MarkFlagsMutuallyExclusive is called.
//
// It is spelled as a literal because cobra's own constant is UNEXPORTED
// (mutuallyExclusiveAnnotation, cobra@v1.10.2/flag_groups.go:28) — there is
// nothing to import, and the next reader will otherwise go looking for one.
// TestBuildRecordsMutexGroups asserts the literal still matches the linked
// cobra by reading the annotation straight off a flag cobra just marked, so a
// rename in a future cobra fails the build rather than silently emptying the
// mutex groups from the inventory.
//
// The annotation VALUE is a []string, not a single string: each element is
// one group, spelled as its members space-joined ("update-baseline
// no-baseline"), and a flag can belong to several groups at once — root's
// --cli belongs to three.
const mutuallyExclusiveAnnotation = "cobra_annotation_mutually_exclusive"

// Command is one command in the tree.
type Command struct {
	// Path is cobra's full command path, e.g. "commitbrief cache prune".
	// It is a command path, not a filesystem path: the separator is a space
	// on every platform and nothing here touches the OS path rules.
	Path   string `json:"path"`
	Short  string `json:"short,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
}

// Flag is one flag, attributed to the command that declares it.
//
// Flags are NOT deduplicated by name and must not be. doctor --quiet/-q,
// cache prune --provider, cache prune --model and guard --unstaged
// deliberately re-declare names that also exist on root's persistent set;
// pflag resolves local-over-persistent silently, so a name-keyed map would
// quietly under-report the real surface and hide the shadowing entirely.
// Command + Scope is what makes a row unique.
type Flag struct {
	Command     string `json:"command"`
	Name        string `json:"name"`
	Shorthand   string `json:"shorthand,omitempty"`
	Type        string `json:"type"`
	Default     string `json:"default,omitempty"`
	NoOptDefVal string `json:"no_opt_def_val,omitempty"`
	Usage       string `json:"usage,omitempty"`
	Scope       Scope  `json:"scope"`
	Hidden      bool   `json:"hidden,omitempty"`
	Deprecated  string `json:"deprecated,omitempty"`

	// MutexGroups lists the mutually-exclusive groups this flag belongs to,
	// each one its members space-joined, in the order cobra recorded them
	// (which is the order of the MarkFlagsMutuallyExclusive calls).
	MutexGroups []string `json:"mutex_groups,omitempty"`
}

// walkCommands returns the commands and flags of the tree rooted at root, in
// depth-first tree order.
//
// Order is deterministic without sorting anything: cobra's Commands() sorts
// children by name (EnableCommandSorting defaults to true) and pflag's
// VisitAll visits in lexical order (SortFlags defaults to true).
func walkCommands(root *cobra.Command) ([]Command, []Flag) {
	cmds := []Command{}
	flags := []Flag{}
	if root == nil {
		return cmds, flags
	}
	walk(root, nil, &cmds, &flags)
	return cmds, flags
}

// walk visits one command, then its children. ancestors is the chain from the
// root down to (but excluding) cmd; it is what lets the flag pass tell an
// inherited flag from a locally declared one.
func walk(cmd *cobra.Command, ancestors []*cobra.Command, cmds *[]Command, flags *[]Flag) {
	if isGeneratedCommand(cmd) {
		// Skipping without recursing also drops `completion`'s four shell
		// children, which are generated for the same reason the parent is.
		return
	}

	*cmds = append(*cmds, Command{
		Path:   cmd.CommandPath(),
		Short:  cmd.Short,
		Hidden: cmd.Hidden,
	})
	*flags = append(*flags, commandFlags(cmd, ancestors)...)

	child := append(append([]*cobra.Command(nil), ancestors...), cmd)
	for _, sub := range cmd.Commands() {
		walk(sub, child, cmds, flags)
	}
}

// isGeneratedCommand reports whether cobra injected this command rather than
// CommitBrief declaring it.
//
// Detection is by name because cobra leaves no marker on a generated command
// (it marks generated FLAGS with an exported annotation, but not commands).
// That is safe here: CommitBrief declares no command called help or
// completion, and the "__" prefix is cobra's reserved namespace for the
// hidden shell-completion helpers (__complete, __completeNoDesc).
func isGeneratedCommand(cmd *cobra.Command) bool {
	switch name := cmd.Name(); {
	case name == "help", name == "completion":
		return true
	case strings.HasPrefix(name, "__"):
		return true
	default:
		return false
	}
}

// commandFlags collects the flags cmd itself declares.
//
// Two passes, because the two flag sets answer different questions:
//
//   - PersistentFlags() holds exactly what was declared persistent HERE, so
//     every entry is this command's own and gets ScopeGlobal.
//   - Flags() holds locally declared flags plus, once cobra has merged them,
//     this command's persistent flags and every ancestor's. Cobra merges by
//     adding the SAME *pflag.Flag pointer, so identity — not name — separates
//     a merged-in inherited flag from a local one that shadows it. That is
//     what keeps guard's own --unstaged in the inventory while root's
//     persistent --unstaged is not reported a second time under guard.
func commandFlags(cmd *cobra.Command, ancestors []*cobra.Command) []Flag {
	out := []Flag{}

	inherited := map[*pflag.Flag]bool{}
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		inherited[f] = true
		if isGeneratedFlag(f) {
			return
		}
		out = append(out, newFlag(cmd, f, ScopeGlobal))
	})
	for _, a := range ancestors {
		a.PersistentFlags().VisitAll(func(f *pflag.Flag) { inherited[f] = true })
	}

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if inherited[f] || isGeneratedFlag(f) {
			return
		}
		out = append(out, newFlag(cmd, f, ScopeCommand))
	})

	return out
}

// isGeneratedFlag reports whether cobra injected this flag (--help, and
// --version on a command with a Version set).
//
// They are cobra's, not CommitBrief's — the same category as the help and
// completion commands. They are also injected lazily during Execute, so
// including them would make the inventory depend on which command happened to
// run. cobra marks them with an exported annotation, so unlike the generated
// commands this needs no name matching.
func isGeneratedFlag(f *pflag.Flag) bool {
	return len(f.Annotations[cobra.FlagSetByCobraAnnotation]) > 0
}

func newFlag(cmd *cobra.Command, f *pflag.Flag, scope Scope) Flag {
	return Flag{
		Command:     cmd.CommandPath(),
		Name:        f.Name,
		Shorthand:   f.Shorthand,
		Type:        f.Value.Type(),
		Default:     f.DefValue,
		NoOptDefVal: f.NoOptDefVal,
		Usage:       f.Usage,
		Scope:       scope,
		Hidden:      f.Hidden,
		Deprecated:  f.Deprecated,
		MutexGroups: mutexGroups(f),
	}
}

// mutexGroups copies the mutually-exclusive annotation off a flag. The copy
// matters: the returned slice ends up in the marshalled artifact and must not
// alias pflag's live annotation map.
func mutexGroups(f *pflag.Flag) []string {
	groups := f.Annotations[mutuallyExclusiveAnnotation]
	if len(groups) == 0 {
		return nil
	}
	return append([]string(nil), groups...)
}
