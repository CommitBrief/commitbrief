// SPDX-License-Identifier: GPL-3.0-or-later

// Package upgrade implements `commitbrief upgrade`: detecting how the
// running binary was installed, asking GitHub Releases whether a newer
// version exists, and either delegating to the owning package manager
// or replacing a manually installed binary in place. See ADR-0034.
package upgrade

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. Build metadata (+meta) is not
// modelled: CommitBrief never tags with it, and semver says it is
// ignored for precedence anyway.
type Version struct {
	Major int
	Minor int
	Patch int
	Pre   string // "" for a release; "rc.1" for v1.0.0-rc.1
}

// ParseVersion accepts "v1.2.3", "1.2.3" and "v1.2.3-rc.1". It returns
// ok=false for anything else — most importantly the "dev" placeholder a
// locally built binary carries, which the caller turns into a "this is a
// development build" message rather than a bogus comparison.
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return Version{}, false
	}
	core := s
	pre := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core, pre = s[:i], s[i+1:]
		if pre == "" {
			return Version{}, false
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, false
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre}, true
}

// String renders the version back in tag form (always v-prefixed).
func (v Version) String() string {
	if v.Pre == "" {
		return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	}
	return fmt.Sprintf("v%d.%d.%d-%s", v.Major, v.Minor, v.Patch, v.Pre)
}

// Compare returns -1, 0 or +1 as v sorts before, equal to, or after o,
// following semver precedence: numeric core first, then a release
// outranking any prerelease of the same core.
func (v Version) Compare(o Version) int {
	if c := cmpInt(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmpInt(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmpInt(v.Patch, o.Patch); c != 0 {
		return c
	}
	switch {
	case v.Pre == "" && o.Pre == "":
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	return comparePre(v.Pre, o.Pre)
}

// comparePre orders two dot-separated prerelease strings. Per semver:
// identifiers are compared left to right; a purely numeric identifier
// sorts below an alphanumeric one and compares numerically; a shorter
// identifier list sorts below a longer one when the shared prefix is
// equal (so rc < rc.1).
func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		switch {
		case aErr == nil && bErr == nil: // both numeric
			if c := cmpInt(an, bn); c != 0 {
				return c
			}
		case aErr == nil: // numeric sorts below alphanumeric
			return -1
		case bErr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
