package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"

	"github.com/tracyhatemice/automate-rule-list/internal/clashrule"
	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/domain"
	"github.com/tracyhatemice/automate-rule-list/internal/ipset"
	"github.com/tracyhatemice/automate-rule-list/internal/parse"
)

// excludes holds compiled exclude patterns: domain patterns, prefix
// patterns, exact clash rules ("=DOMAIN-KEYWORD,foo") and regexes that
// also run against the full rule text.
type excludes struct {
	dom   *domain.Matcher
	ip    *ipset.Matcher
	rules map[string]struct{} // canonical "TYPE,value" of exact rule patterns
	regex []*regexp.Regexp
}

func (x excludes) matchDomain(d string) bool { return x.dom != nil && x.dom.Match(d) }

func (x excludes) matchPrefix(p netip.Prefix) bool { return x.ip != nil && x.ip.Match(p) }

// matchRule applies the domain patterns to domain rules, the prefix
// patterns to IP rules, and the exact-rule and regex patterns to the
// canonical text of any rule.
func (x excludes) matchRule(r clashrule.Rule) bool {
	switch {
	case r.IsDomain() && x.matchDomain(r.Value):
		return true
	case r.IsIP():
		if p, err := r.Prefix(); err == nil && x.matchPrefix(p) {
			return true
		}
	}
	if _, ok := x.rules[r.Type+","+r.Value]; ok {
		return true
	}
	text := r.String()
	for _, re := range x.regex {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// buildExcludes sorts patterns by shape: regexes apply to every kind,
// "=TYPE,value" is an exact clash rule, addresses and CIDR blocks apply to
// prefixes, everything else to domains.
func buildExcludes(patterns []string) (excludes, error) {
	var domPats, ipPats []string
	x := excludes{rules: map[string]struct{}{}}
	for _, p := range patterns {
		switch {
		case len(p) > 2 && p[0] == '/' && p[len(p)-1] == '/':
			re, err := regexp.Compile(p[1 : len(p)-1])
			if err != nil {
				return x, fmt.Errorf("pattern %q: %w", p, err)
			}
			x.regex = append(x.regex, re)
			domPats = append(domPats, p)
			ipPats = append(ipPats, p)
		case strings.HasPrefix(p, "=") && strings.Contains(p, ","):
			rule, err := clashrule.Parse(p[1:])
			if err != nil {
				return x, fmt.Errorf("pattern %q: %w", p, err)
			}
			c, err := clashrule.Canonical(rule)
			if err != nil {
				return x, fmt.Errorf("pattern %q: %w", p, err)
			}
			x.rules[c.Type+","+c.Value] = struct{}{}
		default:
			if _, err := ipset.Parse(p); err == nil {
				ipPats = append(ipPats, p)
			} else {
				domPats = append(domPats, p)
			}
		}
	}
	var err error
	if x.dom, err = domain.NewMatcher(domPats); err != nil {
		return x, err
	}
	if x.ip, err = ipset.NewMatcher(ipPats); err != nil {
		return x, err
	}
	return x, nil
}

// compileFilter compiles a filter block: its explicit patterns plus every
// entry of its exclude_from sources.
func (r *Runner) compileFilter(ctx context.Context, f config.Filter, log *slog.Logger) (excludes, error) {
	patterns := append([]string(nil), f.Exclude...)
	if len(f.ExcludeFrom) > 0 {
		srcs, err := r.loadSources(ctx, f.ExcludeFrom, log)
		if err != nil {
			return excludes{}, err
		}
		for _, ls := range srcs {
			n := 0
			for _, it := range ls.items {
				if p, ok := patternFromItem(it); ok {
					patterns = append(patterns, p)
					n++
				}
			}
			log.Debug("exclude_from loaded", "source", ls.spec.String(), "patterns", n)
		}
	}
	x, err := buildExcludes(patterns)
	if err != nil {
		return x, err
	}
	return x, nil
}

// patternFromItem turns a parsed item into an exclude pattern.
func patternFromItem(it parse.Item) (string, bool) {
	switch it.Kind {
	case parse.KindDomain:
		d, err := domain.Normalize(it.Value, true)
		return d, err == nil
	case parse.KindIP:
		p, err := ipset.Parse(it.Value)
		if err != nil {
			return "", false
		}
		return p.String(), true
	case parse.KindRule:
		rule, err := clashrule.Parse(it.Value)
		if err != nil {
			return "", false
		}
		c, err := clashrule.Canonical(rule)
		if err != nil || (!c.IsDomain() && !c.IsIP()) {
			return "", false
		}
		return c.Value, true
	}
	return "", false
}

// itemRule parses an item of any kind into a canonical clash rule.
// Domains become DOMAIN-SUFFIX rules and prefixes IP-CIDR rules with
// no-resolve.
func itemRule(it parse.Item, allowTLD bool) (clashrule.Rule, error) {
	switch it.Kind {
	case parse.KindDomain:
		d, err := domain.Normalize(it.Value, allowTLD)
		if err != nil {
			return clashrule.Rule{}, err
		}
		return clashrule.Canonical(clashrule.Rule{Type: "DOMAIN-SUFFIX", Value: d})
	case parse.KindIP:
		p, err := ipset.Parse(it.Value)
		if err != nil {
			return clashrule.Rule{}, err
		}
		return clashrule.Canonical(clashrule.Rule{Type: "IP-CIDR", Value: p.String(), Options: []string{"no-resolve"}})
	case parse.KindRule:
		rule, err := clashrule.Parse(it.Value)
		if err != nil {
			return clashrule.Rule{}, err
		}
		return clashrule.Canonical(rule)
	}
	return clashrule.Rule{}, fmt.Errorf("unsupported item kind %v", it.Kind)
}
