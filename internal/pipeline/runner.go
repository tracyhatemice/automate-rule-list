// Package pipeline runs jobs: it fetches and parses every source, builds
// the intermediate lines with the processor for the job kind, detects
// whether they changed since the last run, and renders, writes and
// uploads the outputs that need it.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/fetch"
	"github.com/tracyhatemice/automate-rule-list/internal/publish"
	"github.com/tracyhatemice/automate-rule-list/internal/state"
)

// Runner executes jobs.
type Runner struct {
	Cfg       *config.Config
	Fetcher   *fetch.Fetcher
	Store     state.Store
	Publisher publish.Publisher // nil disables uploads
	Now       func() time.Time
	Log       *slog.Logger

	DryRun   bool // report what would change without writing or uploading
	NoUpload bool // write local outputs but do not upload
	Force    bool // treat the content as changed even when it is not
}

// Result summarizes one job run.
type Result struct {
	Job      string
	Entries  int      // intermediate lines
	Changed  bool     // content differs from the last run (or first run / forced)
	Written  []string // output paths written (or, in dry-run, that would be)
	Uploaded []string // upload URIs (or, in dry-run, that would be)
	Skipped  []string // outputs left untouched
	Err      error
}

// Run executes the jobs named in only, or every job when only is empty.
// Jobs run one after another; a failing job does not stop the others.
func (r *Runner) Run(ctx context.Context, only []string) []Result {
	var results []Result
	if len(only) == 0 {
		for _, job := range r.Cfg.Jobs {
			results = append(results, r.RunJob(ctx, job))
		}
		return results
	}
	byName := map[string]config.Job{}
	for _, job := range r.Cfg.Jobs {
		byName[job.Name] = job
	}
	for _, name := range only {
		job, ok := byName[name]
		if !ok {
			results = append(results, Result{Job: name, Err: fmt.Errorf("job %q is not in the config", name)})
			continue
		}
		results = append(results, r.RunJob(ctx, job))
	}
	return results
}

// RunJob executes one job.
func (r *Runner) RunJob(ctx context.Context, job config.Job) Result {
	res := Result{Job: job.Name}
	log := r.logger().With("job", job.Name)
	log.Info("starting", "kind", job.Kind, "mode", job.Mode, "sources", len(job.Sources))

	srcs, err := r.loadSources(ctx, job.Sources, log)
	if err != nil {
		res.Err = fmt.Errorf("job %s: %w", job.Name, err)
		return res
	}
	for i := range srcs {
		if srcs[i].excl, err = r.compileFilter(ctx, srcs[i].src.Filter, log); err != nil {
			res.Err = fmt.Errorf("job %s: source %s: filter: %w", job.Name, srcs[i].spec.String(), err)
			return res
		}
	}
	excl, err := r.compileFilter(ctx, job.Filter, log)
	if err != nil {
		res.Err = fmt.Errorf("job %s: filter: %w", job.Name, err)
		return res
	}
	var lines []string
	switch job.Kind {
	case "domain":
		lines, err = processDomain(job, srcs, excl, log)
	case "ip":
		lines, err = processIP(job, srcs, excl, log)
	case "clash":
		lines, err = processClash(job, srcs, excl, log)
	default:
		err = fmt.Errorf("unknown kind %q", job.Kind)
	}
	if err != nil {
		res.Err = fmt.Errorf("job %s: %w", job.Name, err)
		return res
	}
	res.Entries = len(lines)
	if err := r.syncOutputs(ctx, job, lines, &res, log); err != nil {
		res.Err = fmt.Errorf("job %s: %w", job.Name, err)
	}
	log.Info("finished", "entries", res.Entries, "changed", res.Changed, "written", len(res.Written), "uploaded", len(res.Uploaded), "err", res.Err)
	return res
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// errNoValidEntries is returned for a required source that yields nothing.
var errNoValidEntries = errors.New("no valid entries")
