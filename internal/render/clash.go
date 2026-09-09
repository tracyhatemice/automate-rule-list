package render

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tracyhatemice/automate-rule-list/internal/clashrule"
	"github.com/tracyhatemice/automate-rule-list/internal/ipset"
)

// Clash rule-provider behaviors (mihomo: behavior: domain | ipcidr | classical).
const (
	BehaviorClassical = "classical"
	BehaviorDomain    = "domain"
	BehaviorIPCIDR    = "ipcidr"
)

// clashBehaviors lists the behaviors each job kind can be rendered as. A
// clash job may use domain or ipcidr only when every rule converts; the
// renderer reports the first rule that does not.
var clashBehaviors = map[string][]string{
	"domain": {BehaviorClassical, BehaviorDomain},
	"ip":     {BehaviorClassical, BehaviorIPCIDR},
	"clash":  {BehaviorClassical, BehaviorDomain, BehaviorIPCIDR},
}

// ValidateClashBehavior checks that opt.Behavior is valid for kind; an
// empty behavior means classical.
func ValidateClashBehavior(kind string, opt Options) error {
	b := opt.Behavior
	if b == "" {
		return nil
	}
	for _, ok := range clashBehaviors[kind] {
		if b == ok {
			return nil
		}
	}
	return fmt.Errorf("behavior %q is not valid for %s jobs (allowed: %v)", b, kind, clashBehaviors[kind])
}

// clashLine renders one intermediate line as a rule-provider list item.
// Classical: domains become DOMAIN-SUFFIX rules, addresses IP-CIDR rules
// with no-resolve, clash lines are kept (quoted when they are scalars).
// Domain behavior: "+.host", the multi-level wildcard mihomo's domain trie
// treats like DOMAIN-SUFFIX. Ipcidr behavior: bare CIDR blocks.
func clashLine(kind, line string, opt Options) (string, error) {
	switch kind {
	case "domain":
		if opt.Behavior == BehaviorDomain {
			return "  - '+." + line + "'", nil
		}
		return "  - DOMAIN-SUFFIX," + line, nil
	case "ip":
		cidr := line
		if p, err := ipset.Parse(line); err == nil {
			cidr = p.String()
		}
		if opt.Behavior == BehaviorIPCIDR {
			return "  - '" + cidr + "'", nil
		}
		return "  - IP-CIDR," + cidr + ",no-resolve", nil
	default:
		switch opt.Behavior {
		case BehaviorDomain:
			pat, err := ruleToDomainPattern(line)
			if err != nil {
				return "", err
			}
			return "  - '" + pat + "'", nil
		case BehaviorIPCIDR:
			cidr, err := ruleToCIDR(line)
			if err != nil {
				return "", err
			}
			return "  - '" + cidr + "'", nil
		}
		return "  - " + clashrule.QuoteYAML(line), nil
	}
}

// ruleToDomainPattern converts one clash rule into a domain-behavior
// pattern: DOMAIN → host, DOMAIN-SUFFIX → "+.host", DOMAIN-WILDCARD and
// scalars → themselves when they are valid wildcard patterns.
func ruleToDomainPattern(line string) (string, error) {
	rule, err := clashrule.Parse(line)
	if err != nil {
		return "", fmt.Errorf("rule %q: %w", line, err)
	}
	var pat string
	switch {
	case rule.Scalar:
		pat = strings.ToLower(rule.Value)
	case rule.Type == "DOMAIN":
		pat = rule.Value
	case rule.Type == "DOMAIN-SUFFIX":
		pat = "+." + rule.Value
	case rule.Type == "DOMAIN-WILDCARD":
		pat = strings.ToLower(rule.Value)
	default:
		return "", fmt.Errorf("rule %q cannot be expressed in a domain-behavior provider", line)
	}
	if err := validateDomainPattern(pat); err != nil {
		return "", fmt.Errorf("rule %q: %w", line, err)
	}
	return pat, nil
}

// validateDomainPattern applies mihomo's trie rules: "*" and "+" must be
// whole labels, "+" only as the first of several labels, and a leading
// empty label (".example.com") is the only empty label allowed.
func validateDomainPattern(pat string) error {
	if pat == "" || strings.HasSuffix(pat, ".") {
		return errors.New("invalid domain pattern")
	}
	parts := strings.Split(pat, ".")
	for i, part := range parts {
		switch {
		case part == "" && i == 0 && len(parts) > 1:
			continue
		case part == "":
			return fmt.Errorf("empty label %d in %q", i+1, pat)
		case part == "+":
			if i != 0 || len(parts) == 1 {
				return fmt.Errorf("%q: \"+\" is only allowed as the first of several labels", pat)
			}
		case part == "*":
			continue
		case strings.ContainsAny(part, "*+"):
			return fmt.Errorf("%q: a wildcard must occupy the entire label", pat)
		}
	}
	return nil
}

// ruleToCIDR converts one IP-CIDR / IP-CIDR6 rule or bare prefix scalar
// into CIDR notation.
func ruleToCIDR(line string) (string, error) {
	rule, err := clashrule.Parse(line)
	if err != nil {
		return "", fmt.Errorf("rule %q: %w", line, err)
	}
	if !rule.Scalar && !rule.IsIP() {
		return "", fmt.Errorf("rule %q cannot be expressed in an ipcidr-behavior provider", line)
	}
	p, err := ipset.Parse(rule.Value)
	if err != nil {
		return "", fmt.Errorf("rule %q: %w", line, err)
	}
	return p.String(), nil
}
