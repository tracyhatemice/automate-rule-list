package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/fetch"
	"github.com/tracyhatemice/automate-rule-list/internal/parse"
)

// loadedSource is a fetched and parsed source.
type loadedSource struct {
	src    config.Source
	spec   fetch.Spec
	format string
	items  []parse.Item
	excl   excludes // the source's own filter, compiled by RunJob
	err    error    // fetch or parse failure; only optional sources keep one
}

func specOf(s config.Source) fetch.Spec {
	return fetch.Spec{URL: s.URL, File: s.File, Inline: s.Inline, Encoding: s.Encoding}
}

// loadSources fetches and parses srcs concurrently, preserving order. A
// failing required source is an error; a failing optional source is
// logged and returned with err set and no items.
func (r *Runner) loadSources(ctx context.Context, srcs []config.Source, log *slog.Logger) ([]loadedSource, error) {
	out := make([]loadedSource, len(srcs))
	limit := r.Cfg.HTTP.Concurrency
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, s := range srcs {
		wg.Add(1)
		go func(i int, s config.Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = r.loadSource(ctx, s)
		}(i, s)
	}
	wg.Wait()
	for i := range out {
		ls := &out[i]
		if ls.err == nil {
			log.Debug("source loaded", "source", ls.spec.String(), "format", ls.format, "items", len(ls.items))
			continue
		}
		if !ls.src.Optional {
			return nil, fmt.Errorf("source %s: %w", ls.spec.String(), ls.err)
		}
		log.Warn("optional source failed", "source", ls.spec.String(), "err", ls.err)
	}
	return out, nil
}

func (r *Runner) loadSource(ctx context.Context, s config.Source) loadedSource {
	ls := loadedSource{src: s, spec: specOf(s)}
	data, err := r.Fetcher.Fetch(ctx, ls.spec)
	if err != nil {
		ls.err = err
		return ls
	}
	ls.format = s.Format
	if ls.format == "auto" {
		ls.format = parse.Detect(data)
	}
	f, ok := parse.Lookup(ls.format)
	if !ok {
		ls.err = fmt.Errorf("unknown format %q", ls.format)
		return ls
	}
	ls.items, ls.err = f.Parse(data)
	return ls
}

// sourceStats tracks how a source's items fared.
type sourceStats struct {
	valid, invalid, excluded int
	samples                  []string // a few invalid values for the log
}

func (st *sourceStats) reject(value string) {
	st.invalid++
	if len(st.samples) < 5 {
		st.samples = append(st.samples, value)
	}
}

// finish logs the stats and enforces the "no valid entries" rule.
func (st *sourceStats) finish(ls loadedSource, log *slog.Logger) error {
	if ls.err != nil { // optional source that failed to load
		return nil
	}
	log.Info("source", "source", ls.spec.String(), "format", ls.format, "valid", st.valid, "invalid", st.invalid, "excluded", st.excluded)
	if st.invalid > 0 {
		log.Debug("invalid entries", "source", ls.spec.String(), "samples", st.samples)
	}
	if st.valid == 0 {
		err := fmt.Errorf("source %s: %w (%d invalid)", ls.spec.String(), errNoValidEntries, st.invalid)
		if !ls.src.Optional {
			return err
		}
		log.Warn("optional source ignored", "err", err)
	}
	return nil
}
