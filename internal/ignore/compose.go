// SPDX-License-Identifier: GPL-3.0-or-later

package ignore

import "github.com/go-git/go-git/v5/plumbing/format/gitignore"

func Compose(layers ...*Matcher) *Matcher {
	var all []gitignore.Pattern
	for _, l := range layers {
		if l == nil {
			continue
		}
		all = append(all, l.patterns...)
	}
	return newMatcher(all)
}
