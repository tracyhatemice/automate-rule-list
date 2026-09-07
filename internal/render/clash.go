package render

import (
	"github.com/tracyhatemice/automate-rule-list/internal/clashrule"
	"github.com/tracyhatemice/automate-rule-list/internal/ipset"
)

// clashLine renders one intermediate line as a rule-provider list item:
// domains become DOMAIN-SUFFIX rules, addresses become IP-CIDR rules with
// no-resolve, and clash lines are emitted as they are (quoted when they
// are domain-behaviour scalars).
func clashLine(kind, line string) string {
	switch kind {
	case "domain":
		return "  - DOMAIN-SUFFIX," + line
	case "ip":
		cidr := line
		if p, err := ipset.Parse(line); err == nil {
			cidr = p.String()
		}
		return "  - IP-CIDR," + cidr + ",no-resolve"
	default:
		return "  - " + clashrule.QuoteYAML(line)
	}
}
