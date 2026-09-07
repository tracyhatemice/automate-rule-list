package ipset

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Matcher matches prefixes against exclude patterns: an address or CIDR
// block matches every prefix it contains, and "/regex/" matches the
// textual form produced by Format.
type Matcher struct {
	prefixes []netip.Prefix
	regex    []*regexp.Regexp
}

// NewMatcher compiles patterns.
func NewMatcher(patterns []string) (*Matcher, error) {
	m := &Matcher{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) > 2 && p[0] == '/' && p[len(p)-1] == '/' {
			re, err := regexp.Compile(p[1 : len(p)-1])
			if err != nil {
				return nil, fmt.Errorf("pattern %q: %w", p, err)
			}
			m.regex = append(m.regex, re)
			continue
		}
		pre, err := Parse(p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		m.prefixes = append(m.prefixes, pre)
	}
	return m, nil
}

// Len returns the number of compiled patterns.
func (m *Matcher) Len() int { return len(m.prefixes) + len(m.regex) }

// Match reports whether p is contained in any pattern prefix or matches
// any pattern regex.
func (m *Matcher) Match(p netip.Prefix) bool {
	if m == nil {
		return false
	}
	for _, pre := range m.prefixes {
		if pre.Bits() <= p.Bits() && pre.Contains(p.Addr()) {
			return true
		}
	}
	if len(m.regex) > 0 {
		s := Format(p)
		for _, re := range m.regex {
			if re.MatchString(s) {
				return true
			}
		}
	}
	return false
}
