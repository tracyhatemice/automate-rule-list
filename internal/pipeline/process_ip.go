package pipeline

import (
	"log/slog"
	"net/netip"

	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/ipset"
	"github.com/tracyhatemice/automate-rule-list/internal/parse"
)

// processIP builds the intermediate lines of an ip job: one address or
// CIDR block per line, validated, filtered, deduplicated and, unless
// disabled, aggregated.
func processIP(job config.Job, srcs []loadedSource, excl excludes, log *slog.Logger) ([]string, error) {
	seen := map[netip.Prefix]struct{}{}
	var merged []netip.Prefix
	for _, ls := range srcs {
		var st sourceStats
		for _, it := range ls.items {
			p, ok := prefixFromItem(it)
			if !ok {
				st.reject(it.Value)
				continue
			}
			st.valid++
			if ls.excl.matchPrefix(p) {
				st.excluded++
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			merged = append(merged, p)
		}
		if err := st.finish(ls, log); err != nil {
			return nil, err
		}
	}
	filtered := merged[:0:0]
	for _, p := range merged {
		if !excl.matchPrefix(p) {
			filtered = append(filtered, p)
		}
	}
	var final []netip.Prefix
	if job.AggregateEnabled() {
		final = ipset.Aggregate(filtered)
	} else {
		final = ipset.Dedupe(filtered)
	}
	lines := make([]string, len(final))
	for i, p := range final {
		lines[i] = ipset.Format(p)
	}
	log.Info("processed", "merged", len(merged), "filtered", len(filtered), "final", len(lines))
	return lines, nil
}

// prefixFromItem extracts a prefix from an item of any kind.
func prefixFromItem(it parse.Item) (netip.Prefix, bool) {
	switch it.Kind {
	case parse.KindIP:
		p, err := ipset.Parse(it.Value)
		return p, err == nil
	case parse.KindRule:
		rule, err := itemRule(it, true)
		if err != nil || !rule.IsIP() {
			return netip.Prefix{}, false
		}
		p, err := rule.Prefix()
		return p, err == nil
	}
	return netip.Prefix{}, false
}
