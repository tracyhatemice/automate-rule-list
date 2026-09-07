package pipeline

import (
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/tracyhatemice/automate-rule-list/internal/clashrule"
	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/domain"
	"github.com/tracyhatemice/automate-rule-list/internal/ipset"
	"github.com/tracyhatemice/automate-rule-list/internal/parse"
)

// processClash dispatches on the job mode.
func processClash(job config.Job, srcs []loadedSource, excl excludes, log *slog.Logger) ([]string, error) {
	if job.Mode == "mirror" {
		return mirrorClash(srcs, excl, log)
	}
	return aggregateClash(job, srcs, excl, log)
}

// aggregateClash merges every source into canonical rules, deduplicates
// and collapses them, and groups them by type.
func aggregateClash(job config.Job, srcs []loadedSource, excl excludes, log *slog.Logger) ([]string, error) {
	seen := map[string]struct{}{}
	var merged []clashrule.Rule
	for _, ls := range srcs {
		allowTLD := job.AllowTLD || ls.src.AllowTLD
		var st sourceStats
		for _, it := range ls.items {
			rule, err := itemRule(it, allowTLD)
			if err != nil {
				st.reject(it.Value)
				continue
			}
			st.valid++
			if ls.excl.matchRule(rule) {
				st.excluded++
				continue
			}
			key := rule.Type + "," + rule.Value
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, rule)
		}
		if err := st.finish(ls, log); err != nil {
			return nil, err
		}
	}
	filtered := merged[:0:0]
	for _, rule := range merged {
		if !excl.matchRule(rule) {
			filtered = append(filtered, rule)
		}
	}
	final := clashrule.Order(clashrule.Collapse(filtered))
	if job.Order == "sorted" {
		sortRules(final)
	}
	lines := make([]string, len(final))
	for i, rule := range final {
		lines[i] = rule.String()
	}
	log.Info("processed", "merged", len(merged), "filtered", len(filtered), "final", len(lines))
	return lines, nil
}

// sortRules sorts rules inside each type group: domains by reversed
// labels, prefixes numerically, everything else by value.
func sortRules(rules []clashrule.Rule) {
	slices.SortStableFunc(rules, func(a, b clashrule.Rule) int {
		if a.Type != b.Type {
			return 0 // keep the group order produced by clashrule.Order
		}
		switch {
		case a.IsDomain():
			return strings.Compare(domain.ReverseLabels(a.Value), domain.ReverseLabels(b.Value))
		case a.IsIP():
			pa, errA := ipset.Parse(a.Value)
			pb, errB := ipset.Parse(b.Value)
			if errA == nil && errB == nil {
				if c := pa.Addr().Compare(pb.Addr()); c != 0 {
					return c
				}
				return pa.Bits() - pb.Bits()
			}
		}
		return strings.Compare(a.Value, b.Value)
	})
}

// mirrorClash keeps the single source's payload as it is, minus comments,
// blank lines, invalid items and excluded rules.
func mirrorClash(srcs []loadedSource, excl excludes, log *slog.Logger) ([]string, error) {
	if len(srcs) != 1 {
		return nil, errors.New("mirror needs exactly one source")
	}
	ls := srcs[0]
	if ls.err != nil {
		return nil, ls.err
	}
	var st sourceStats
	var lines []string
	for _, it := range ls.items {
		if it.Kind != parse.KindRule {
			st.reject(it.Value)
			continue
		}
		rule, err := clashrule.Parse(it.Value)
		if err != nil {
			st.reject(it.Value)
			continue
		}
		st.valid++
		if c, err := clashrule.Canonical(rule); err == nil && excl.matchRule(c) {
			st.excluded++
			continue
		}
		lines = append(lines, rule.Raw)
	}
	ls.src.Optional = false // a mirror without content is always an error
	if err := st.finish(ls, log); err != nil {
		return nil, err
	}
	return lines, nil
}
