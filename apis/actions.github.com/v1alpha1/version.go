package v1alpha1

import "strings"

func IsVersionAllowed(resourceVersion, buildVersion string) bool {
	if buildVersion == "dev" || resourceVersion == buildVersion || strings.HasPrefix(buildVersion, "canary-") {
		return true
	}

	rv, ok := parseSemver(resourceVersion)
	if !ok {
		return false
	}
	bv, ok := parseSemver(buildVersion)
	if !ok {
		return false
	}
	return rv.major == bv.major && rv.minor == bv.minor
}

type semver struct {
	major string
	minor string
}

func parseSemver(v string) (p semver, ok bool) {
	if v == "" {
		return
	}
	p.major, v, ok = parseInt(v)
	if !ok {
		return p, false
	}
	if v == "" {
		p.minor = "0"
		return p, true
	}
	if v[0] != '.' {
		return p, false
	}
	p.minor, v, ok = parseInt(v[1:])
	if !ok {
		return p, false
	}
	if v == "" {
		return p, true
	}
	if v[0] != '.' {
		return p, false
	}
	if _, _, ok = parseInt(v[1:]); !ok {
		return p, false
	}
	return p, true
}

func parseInt(v string) (t, rest string, ok bool) {
	if v == "" {
		return
	}
	if v[0] < '0' || '9' < v[0] {
		return
	}
	i := 1
	for i < len(v) && '0' <= v[i] && v[i] <= '9' {
		i++
	}
	if v[0] == '0' && i != 1 {
		return
	}
	return v[:i], v[i:], true
}
