package parse

import (
	"net/netip"
	"strings"
)

// skippedModifiers mark AdGuard rules that are not plain DNS blocks.
var skippedModifiers = []string{"dnsrewrite", "denyallow", "badfilter", "ctag", "client"}

// extractAdblock handles AdGuard / Adblock Plus DNS filters: "||host^"
// rules with optional "$modifiers", hosts-style lines and bare hosts.
// Exceptions, comments, headers, regexes, cosmetic and URL rules are
// skipped.
func extractAdblock(line string) []string {
	s := line
	switch {
	case s == "":
		return nil
	case s[0] == '!', s[0] == '#', s[0] == '[', s[0] == '/':
		return nil
	case strings.HasPrefix(s, "@@"):
		return nil
	case strings.Contains(s, "##"), strings.Contains(s, "#@#"), strings.Contains(s, "#?#"), strings.Contains(s, "#$#"), strings.Contains(s, "$$"):
		return nil
	case strings.Contains(s, "://"):
		return nil
	}
	if strings.HasPrefix(s, "||") {
		return adblockDomainRule(s[2:])
	}
	if s[0] == '|' {
		return nil
	}
	fields := strings.Fields(StripComment(s))
	switch {
	case len(fields) == 0:
		return nil
	case len(fields) >= 2:
		if _, err := netip.ParseAddr(fields[0]); err != nil {
			return nil
		}
		var out []string
		for _, h := range fields[1:] {
			if !localNames[strings.ToLower(h)] {
				out = append(out, h)
			}
		}
		return out
	}
	if strings.ContainsAny(fields[0], "^|$*") {
		return nil
	}
	return []string{fields[0]}
}

// adblockDomainRule parses the part after "||".
func adblockDomainRule(s string) []string {
	rule, modifiers, _ := strings.Cut(s, "$")
	for _, m := range strings.Split(modifiers, ",") {
		name, _, _ := strings.Cut(m, "=")
		for _, skip := range skippedModifiers {
			if name == skip {
				return nil
			}
		}
	}
	host := strings.TrimSuffix(rule, "^")
	if strings.ContainsAny(host, "/^|*") || host == "" {
		return nil
	}
	return []string{host}
}
