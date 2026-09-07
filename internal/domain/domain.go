// Package domain normalizes, validates, deduplicates and matches host
// names. Validation follows the openwrt adblock package: labels of
// [a-z0-9-] up to 63 characters without leading or trailing hyphens,
// at most 253 characters in total, and a top-level label that starts with
// a letter or is a punycode label.
package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/net/idna"
)

const (
	maxHostLen  = 253
	maxLabelLen = 63
)

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("invalid domain")

// Normalize cleans raw and returns a canonical lower-case ASCII host name.
// It strips whitespace, quotes, URL schemes, paths, ports, wildcard
// prefixes ("*.", "+.", ".") and trailing dots, converts internationalized
// names to punycode, and validates the result. Single-label names are only
// accepted when allowTLD is true.
func Normalize(raw string, allowTLD bool) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, `'"`)
	s = strings.ToLower(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	for {
		switch {
		case strings.HasPrefix(s, "*."), strings.HasPrefix(s, "+."):
			s = s[2:]
		case strings.HasPrefix(s, "."):
			s = s[1:]
		default:
			goto trimmed
		}
	}
trimmed:
	s = strings.TrimSuffix(s, ".")
	if s == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalid)
	}
	if !isASCII(s) {
		ascii, err := idna.Lookup.ToASCII(s)
		if err != nil {
			return "", fmt.Errorf("%w: %q: %w", ErrInvalid, raw, err)
		}
		s = ascii
	}
	if err := Validate(s, allowTLD); err != nil {
		return "", err
	}
	return s, nil
}

// Validate reports whether host is a syntactically valid, already
// normalized host name.
func Validate(host string, allowTLD bool) error {
	if host == "" {
		return fmt.Errorf("%w: empty", ErrInvalid)
	}
	if len(host) > maxHostLen {
		return fmt.Errorf("%w: %q: longer than %d characters", ErrInvalid, host, maxHostLen)
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 && !allowTLD {
		return fmt.Errorf("%w: %q: single label", ErrInvalid, host)
	}
	for _, l := range labels {
		if err := validateLabel(l); err != nil {
			return fmt.Errorf("%w: %q: %w", ErrInvalid, host, err)
		}
	}
	tld := labels[len(labels)-1]
	if !isLetter(tld[0]) && !strings.HasPrefix(tld, "xn--") {
		return fmt.Errorf("%w: %q: top-level label must start with a letter", ErrInvalid, host)
	}
	return nil
}

func validateLabel(l string) error {
	if l == "" {
		return errors.New("empty label")
	}
	if len(l) > maxLabelLen {
		return fmt.Errorf("label %q longer than %d characters", l, maxLabelLen)
	}
	if l[0] == '-' || l[len(l)-1] == '-' {
		return fmt.Errorf("label %q starts or ends with a hyphen", l)
	}
	for i := 0; i < len(l); i++ {
		c := l[i]
		if !isLetter(c) && !isDigit(c) && c != '-' {
			return fmt.Errorf("label %q contains %q", l, c)
		}
	}
	return nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// Collapse removes every domain that has a parent domain (with at least
// two labels) in the list, so that "example.com" swallows
// "sub1.example.com" and "sub2.sub1.example.com". Input order is kept.
func Collapse(domains []string) []string {
	if len(domains) == 0 {
		return nil
	}
	present := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		present[d] = struct{}{}
	}
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		if !hasParent(d, present) {
			out = append(out, d)
		}
	}
	return out
}

// hasParent reports whether any proper parent of d with at least two
// labels is in present.
func hasParent(d string, present map[string]struct{}) bool {
	for {
		i := strings.IndexByte(d, '.')
		if i < 0 {
			return false
		}
		d = d[i+1:]
		if strings.IndexByte(d, '.') < 0 {
			return false // single-label parent never collapses
		}
		if _, ok := present[d]; ok {
			return true
		}
	}
}

// ReverseLabels returns the labels of host in reverse order joined by
// dots: "a.b.com" becomes "com.b.a". Sorting by this key groups a TLD and
// then a registrable domain together.
func ReverseLabels(host string) string {
	labels := strings.Split(host, ".")
	slices.Reverse(labels)
	return strings.Join(labels, ".")
}

// SortReversed sorts domains in place by their reversed labels.
func SortReversed(domains []string) {
	keys := make(map[string]string, len(domains))
	for _, d := range domains {
		keys[d] = ReverseLabels(d)
	}
	slices.SortFunc(domains, func(a, b string) int {
		return strings.Compare(keys[a], keys[b])
	})
}
