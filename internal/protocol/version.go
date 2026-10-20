package protocol

import (
	"strconv"
	"strings"
)

// ParseVersion parses "1.2.3", "v1.2" or "1.2.3-rc1+abc" into its numeric
// major, minor and patch parts. Pre-release and build suffixes are ignored.
// Non-numeric versions (e.g. "dev") are not valid.
func ParseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// NewerVersion reports whether candidate is a higher semantic version than
// current ("1.2.10" > "1.2.9"). Non-numeric versions (e.g. "dev") never
// compare as newer, so development builds are never updated automatically.
func NewerVersion(candidate, current string) bool {
	c, ok1 := ParseVersion(candidate)
	cur, ok2 := ParseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range 3 {
		if c[i] != cur[i] {
			return c[i] > cur[i]
		}
	}
	return false
}
