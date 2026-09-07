// Package clashrule models rule-provider payload items of Clash / mihomo:
// classical rules such as "DOMAIN-SUFFIX,example.com" or
// "IP-CIDR,10.0.0.0/8,no-resolve", and the scalar items of domain and
// ipcidr behaviour providers such as "+.example.com" or "10.0.0.0/8".
package clashrule

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/tracyhatemice/automate-rule-list/internal/domain"
	"github.com/tracyhatemice/automate-rule-list/internal/ipset"
)

// ErrInvalid is wrapped by every parse error.
var ErrInvalid = errors.New("invalid clash rule")

// KnownTypes lists the rule types accepted in classical payloads.
var KnownTypes = map[string]bool{
	"DOMAIN": true, "DOMAIN-SUFFIX": true, "DOMAIN-KEYWORD": true, "DOMAIN-REGEX": true, "DOMAIN-WILDCARD": true,
	"GEOSITE": true,
	"IP-CIDR": true, "IP-CIDR6": true, "IP-SUFFIX": true, "IP-ASN": true, "GEOIP": true,
	"SRC-GEOIP": true, "SRC-IP-ASN": true, "SRC-IP-CIDR": true, "SRC-IP-SUFFIX": true,
	"DST-PORT": true, "SRC-PORT": true, "IN-PORT": true, "IN-TYPE": true, "IN-USER": true, "IN-NAME": true,
	"PROCESS-PATH": true, "PROCESS-PATH-REGEX": true, "PROCESS-NAME": true, "PROCESS-NAME-REGEX": true,
	"UID": true, "NETWORK": true, "DSCP": true, "RULE-SET": true,
	"AND": true, "OR": true, "NOT": true, "SUB-RULE": true, "MATCH": true,
}

var (
	domainTypes = map[string]bool{"DOMAIN": true, "DOMAIN-SUFFIX": true}
	ipTypes     = map[string]bool{"IP-CIDR": true, "IP-CIDR6": true, "SRC-IP-CIDR": true}
	// classicalRe matches a rule that starts with an upper-case type name.
	classicalRe = regexp.MustCompile(`^[A-Z][A-Z0-9-]*(,|$)`)
	// typeOrder is the group order used by Order; unlisted types go last.
	typeOrder = map[string]int{
		"DOMAIN-KEYWORD": 0, "DOMAIN": 1, "DOMAIN-SUFFIX": 2, "DOMAIN-WILDCARD": 3, "DOMAIN-REGEX": 4, "GEOSITE": 5,
		"IP-CIDR": 6, "IP-CIDR6": 7, "IP-SUFFIX": 8, "IP-ASN": 9, "GEOIP": 10,
	}
)

// Rule is one payload item. Classical rules have a Type; scalar items
// (domain / ipcidr behaviour) have Scalar set and only a Value.
type Rule struct {
	Type    string
	Value   string
	Options []string
	Scalar  bool
	Raw     string
}

// Parse classifies and validates one payload item. It does not normalize
// the value; use Canonical for that.
func Parse(item string) (Rule, error) {
	s := strings.TrimSpace(item)
	s = unquote(s)
	if s == "" {
		return Rule{}, fmt.Errorf("%w: empty", ErrInvalid)
	}
	if classicalRe.MatchString(strings.ToUpper(s)) && (strings.Contains(s, ",") || KnownTypes[strings.ToUpper(s)]) {
		return parseClassical(s)
	}
	if _, err := ipset.Parse(s); err == nil {
		return Rule{Scalar: true, Value: s, Raw: s}, nil
	}
	if _, err := domain.Normalize(s, true); err != nil {
		return Rule{}, fmt.Errorf("%w: %q: %w", ErrInvalid, s, err)
	}
	return Rule{Scalar: true, Value: s, Raw: s}, nil
}

func parseClassical(s string) (Rule, error) {
	parts := strings.Split(s, ",")
	typ := strings.ToUpper(strings.TrimSpace(parts[0]))
	if !KnownTypes[typ] {
		return Rule{}, fmt.Errorf("%w: %q: unknown type %q", ErrInvalid, s, typ)
	}
	r := Rule{Type: typ, Raw: s}
	if len(parts) > 1 {
		r.Value = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		for _, o := range parts[2:] {
			r.Options = append(r.Options, strings.TrimSpace(o))
		}
	}
	if r.Value == "" && typ != "MATCH" {
		return Rule{}, fmt.Errorf("%w: %q: missing value", ErrInvalid, s)
	}
	switch {
	case domainTypes[typ]:
		if _, err := domain.Normalize(r.Value, typ == "DOMAIN-SUFFIX"); err != nil {
			return Rule{}, fmt.Errorf("%w: %q: %w", ErrInvalid, s, err)
		}
	case ipTypes[typ]:
		if _, err := ipset.Parse(r.Value); err != nil {
			return Rule{}, fmt.Errorf("%w: %q: %w", ErrInvalid, s, err)
		}
	}
	return r, nil
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// IsDomain reports whether the rule value is a host name (DOMAIN,
// DOMAIN-SUFFIX). Scalars are not domains until canonicalized.
func (r Rule) IsDomain() bool { return domainTypes[r.Type] }

// IsIP reports whether the rule value is an address or CIDR block.
func (r Rule) IsIP() bool { return r.Type == "IP-CIDR" || r.Type == "IP-CIDR6" }

// Prefix returns the value of an IP rule as a prefix.
func (r Rule) Prefix() (netip.Prefix, error) { return ipset.Parse(r.Value) }

// String renders the rule in classical form ("TYPE,value,options") or, for
// scalars, the raw value.
func (r Rule) String() string {
	if r.Scalar {
		return r.Value
	}
	var b strings.Builder
	b.WriteString(r.Type)
	if r.Value != "" {
		b.WriteByte(',')
		b.WriteString(r.Value)
	}
	for _, o := range r.Options {
		b.WriteByte(',')
		b.WriteString(o)
	}
	return b.String()
}

// Canonical converts scalars to classical rules and normalizes values:
// host names are lower-cased and converted to punycode, prefixes are
// masked and always written in CIDR notation, types are upper-case and
// IP-CIDR6 becomes IP-CIDR.
func Canonical(r Rule) (Rule, error) {
	if r.Scalar {
		return canonicalScalar(r.Value)
	}
	out := Rule{Type: strings.ToUpper(r.Type), Value: r.Value, Options: r.Options}
	if out.Type == "IP-CIDR6" {
		out.Type = "IP-CIDR" // mihomo treats both alike; one spelling makes dedupe work
	}
	switch {
	case domainTypes[out.Type]:
		d, err := domain.Normalize(r.Value, out.Type == "DOMAIN-SUFFIX")
		if err != nil {
			return Rule{}, fmt.Errorf("%w: %q: %w", ErrInvalid, r.Raw, err)
		}
		out.Value = d
	case ipTypes[out.Type]:
		p, err := ipset.Parse(r.Value)
		if err != nil {
			return Rule{}, fmt.Errorf("%w: %q: %w", ErrInvalid, r.Raw, err)
		}
		out.Value = p.String()
	}
	out.Raw = out.String()
	return out, nil
}

func canonicalScalar(v string) (Rule, error) {
	if p, err := ipset.Parse(v); err == nil {
		r := Rule{Type: "IP-CIDR", Value: p.String()}
		r.Raw = r.String()
		return r, nil
	}
	typ := "DOMAIN"
	switch {
	case strings.HasPrefix(v, "+."), strings.HasPrefix(v, "."):
		typ = "DOMAIN-SUFFIX"
	case strings.HasPrefix(v, "*."):
		typ = "DOMAIN-WILDCARD"
	}
	d, err := domain.Normalize(v, typ == "DOMAIN-SUFFIX")
	if err != nil {
		return Rule{}, fmt.Errorf("%w: %q: %w", ErrInvalid, v, err)
	}
	if typ == "DOMAIN-WILDCARD" {
		d = "*." + d
	}
	r := Rule{Type: typ, Value: d}
	r.Raw = r.String()
	return r, nil
}

// Collapse removes duplicates (same type and value, first wins),
// DOMAIN-SUFFIX rules covered by a parent DOMAIN-SUFFIX, DOMAIN rules
// covered by any DOMAIN-SUFFIX, and IP rules covered by a wider IP rule.
// Rules must be canonical. Input order is kept.
func Collapse(rules []Rule) []Rule {
	seen := make(map[string]struct{}, len(rules))
	uniq := make([]Rule, 0, len(rules))
	var suffixes []string
	var prefixes []netip.Prefix
	for _, r := range rules {
		key := r.Type + "," + r.Value
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		uniq = append(uniq, r)
		switch {
		case r.Type == "DOMAIN-SUFFIX":
			suffixes = append(suffixes, r.Value)
		case r.IsIP():
			if p, err := r.Prefix(); err == nil {
				prefixes = append(prefixes, p)
			}
		}
	}
	keptSuffix := toSet(domain.Collapse(suffixes))
	suffixMatcher, _ := domain.NewMatcher(suffixes)
	keptPrefix := make(map[netip.Prefix]struct{})
	for _, p := range ipset.Dedupe(prefixes) {
		keptPrefix[p] = struct{}{}
	}
	out := make([]Rule, 0, len(uniq))
	for _, r := range uniq {
		switch {
		case r.Type == "DOMAIN-SUFFIX":
			if _, ok := keptSuffix[r.Value]; !ok {
				continue
			}
		case r.Type == "DOMAIN":
			if suffixMatcher.Match(r.Value) {
				continue
			}
		case r.IsIP():
			p, err := r.Prefix()
			if err == nil {
				if _, ok := keptPrefix[p]; !ok {
					continue
				}
			}
		}
		out = append(out, r)
	}
	return out
}

func toSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// Order groups rules by type (keywords, domains, suffixes, other domain
// rules, IP rules, everything else) keeping the input order inside each
// group.
func Order(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	rank := func(r Rule) int {
		if n, ok := typeOrder[r.Type]; ok {
			return n
		}
		return len(typeOrder) + 100
	}
	// insertion-stable sort by rank
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && rank(out[j]) < rank(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// QuoteYAML renders a payload item as a YAML list element: classical
// rules are emitted bare, scalars are single-quoted.
func QuoteYAML(item string) string {
	s := strings.TrimSpace(item)
	if classicalRe.MatchString(s) {
		return s
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
