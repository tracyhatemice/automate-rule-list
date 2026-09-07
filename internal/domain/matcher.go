package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// Matcher matches host names against exclude patterns:
//
//   - "example.com"  matches example.com and every subdomain,
//   - "=example.com" matches example.com exactly,
//   - "/regex/"      matches hosts that match the RE2 regular expression.
type Matcher struct {
	suffix map[string]struct{}
	exact  map[string]struct{}
	regex  []*regexp.Regexp
	n      int
}

// NewMatcher compiles patterns. Domain patterns are normalized like hosts
// (single-label patterns are allowed) so "Example.COM" and "example.com"
// are the same pattern.
func NewMatcher(patterns []string) (*Matcher, error) {
	m := &Matcher{suffix: map[string]struct{}{}, exact: map[string]struct{}{}}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		switch {
		case len(p) > 2 && p[0] == '/' && p[len(p)-1] == '/':
			re, err := regexp.Compile(p[1 : len(p)-1])
			if err != nil {
				return nil, fmt.Errorf("pattern %q: %w", p, err)
			}
			m.regex = append(m.regex, re)
		case p[0] == '=':
			d, err := Normalize(p[1:], true)
			if err != nil {
				return nil, fmt.Errorf("pattern %q: %w", p, err)
			}
			m.exact[d] = struct{}{}
		default:
			d, err := Normalize(p, true)
			if err != nil {
				return nil, fmt.Errorf("pattern %q: %w", p, err)
			}
			m.suffix[d] = struct{}{}
		}
		m.n++
	}
	return m, nil
}

// Len returns the number of compiled patterns.
func (m *Matcher) Len() int { return m.n }

// Match reports whether host matches any pattern.
func (m *Matcher) Match(host string) bool {
	if m == nil || m.n == 0 {
		return false
	}
	if _, ok := m.exact[host]; ok {
		return true
	}
	for h := host; h != ""; {
		if _, ok := m.suffix[h]; ok {
			return true
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	for _, re := range m.regex {
		if re.MatchString(host) {
			return true
		}
	}
	return false
}
