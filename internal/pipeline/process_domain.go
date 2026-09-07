package pipeline

import (
	"log/slog"
	"strings"

	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/domain"
	"github.com/tracyhatemice/automate-rule-list/internal/parse"
)

// processDomain builds the intermediate lines of a domain job: one
// normalized host per line, deduplicated, filtered, collapsed under
// parent domains and ordered.
func processDomain(job config.Job, srcs []loadedSource, excl excludes, log *slog.Logger) ([]string, error) {
	seen := map[string]struct{}{}
	var merged []string
	for _, ls := range srcs {
		allowTLD := job.AllowTLD || ls.src.AllowTLD
		var st sourceStats
		for _, it := range ls.items {
			d, ok := domainFromItem(it, allowTLD)
			if !ok {
				st.reject(it.Value)
				continue
			}
			st.valid++
			if ls.excl.matchDomain(d) {
				st.excluded++
				continue
			}
			if _, dup := seen[d]; dup {
				continue
			}
			seen[d] = struct{}{}
			merged = append(merged, d)
		}
		if err := st.finish(ls, log); err != nil {
			return nil, err
		}
	}
	filtered := merged[:0:0]
	for _, d := range merged {
		if !excl.matchDomain(d) {
			filtered = append(filtered, d)
		}
	}
	out := domain.Collapse(filtered)
	if job.Order == "sorted" {
		domain.SortReversed(out)
	}
	log.Info("processed", "merged", len(merged), "filtered", len(filtered), "final", len(out))
	return out, nil
}

// domainFromItem extracts a normalized host from an item of any kind.
func domainFromItem(it parse.Item, allowTLD bool) (string, bool) {
	switch it.Kind {
	case parse.KindDomain:
		d, err := domain.Normalize(it.Value, allowTLD)
		return d, err == nil
	case parse.KindRule:
		rule, err := itemRule(it, true)
		if err != nil || !rule.IsDomain() {
			return "", false
		}
		if !allowTLD && !strings.Contains(rule.Value, ".") {
			return "", false
		}
		return rule.Value, true
	}
	return "", false
}
