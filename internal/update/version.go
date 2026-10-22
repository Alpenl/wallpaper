package update

import (
	"fmt"
	"regexp"
	"strings"
)

// SemVer core and numeric prerelease identifiers disallow leading zeroes.
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func numeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func parseVersion(s string) ([]string, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m != nil {
		for _, id := range strings.Split(m[4], ".") {
			if numeric(id) && len(id) > 1 && id[0] == '0' {
				m = nil
				break
			}
		}
	}
	if m == nil {
		return nil, fmt.Errorf("update: invalid SemVer %q", s)
	}
	return m[1:5], nil
}

// compareNumber compares arbitrary-size nonnegative decimal identifiers.
func compareNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

// Newer returns false for dev or malformed versions. Build metadata is ignored.
func Newer(a, b string) bool {
	x, err := parseVersion(a)
	if err != nil {
		return false
	}
	y, err := parseVersion(b)
	if err != nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if c := compareNumber(x[i], y[i]); c != 0 {
			return c > 0
		}
	}
	if x[3] == y[3] {
		return false
	}
	if x[3] == "" {
		return true
	}
	if y[3] == "" {
		return false
	}
	xp, yp := strings.Split(x[3], "."), strings.Split(y[3], ".")
	for i := 0; i < min(len(xp), len(yp)); i++ {
		a, b := xp[i], yp[i]
		var c int
		switch {
		case numeric(a) && numeric(b):
			c = compareNumber(a, b)
		case numeric(a):
			c = -1
		case numeric(b):
			c = 1
		default:
			c = strings.Compare(a, b)
		}
		if c != 0 {
			return c > 0
		}
	}
	return len(xp) > len(yp)
}

func validateManifest(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("update: missing manifest")
	}
	if m.Schema != 1 {
		return fmt.Errorf("update: unsupported manifest schema %d", m.Schema)
	}
	_, err := parseVersion(m.Version)
	return err
}
