// Command rulegen fetches upstream block/route lists, cleans and
// deduplicates them, and publishes the results when they change.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/fetch"
	"github.com/tracyhatemice/automate-rule-list/internal/pipeline"
	"github.com/tracyhatemice/automate-rule-list/internal/publish"
	"github.com/tracyhatemice/automate-rule-list/internal/state"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches the subcommand and returns the exit code: 0 success,
// 1 a job or the config failed, 2 usage error.
func run(args []string, stdout, stderr io.Writer) int {
	config.Version = version
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version", "-version", "--version":
		_, _ = fmt.Fprintln(stdout, "rulegen", version)
		return 0
	case "run":
		return runCmd(args[1:], stdout, stderr)
	case "check":
		return checkCmd(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `usage: rulegen <command> [flags]

commands:
  run      fetch, process and publish the jobs in the config file
  check    validate the config file and print a job summary
  version  print the version

run flags:
  -c, --config PATH   config file (default rulegen.yaml)
  -j, --job NAME      run only this job; repeatable
      --dry-run       report what would change, write and upload nothing
      --no-upload     write local outputs but skip S3 uploads
      --force         treat every job as changed: new timestamp, re-upload
      --every DUR     keep running: repeat every DUR (30m, 6h, 1d …) until
                      SIGINT/SIGTERM; the config is re-read each cycle
  -v                  debug logging
`)
}

// stringList collects a repeatable flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type runFlags struct {
	config   string
	jobs     stringList
	dryRun   bool
	noUpload bool
	force    bool
	every    string
	verbose  bool
}

func parseRunFlags(name string, args []string, stderr io.Writer) (runFlags, error) {
	var f runFlags
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.config, "c", "rulegen.yaml", "config file")
	fs.StringVar(&f.config, "config", "rulegen.yaml", "config file")
	fs.Var(&f.jobs, "j", "job to run (repeatable)")
	fs.Var(&f.jobs, "job", "job to run (repeatable)")
	fs.BoolVar(&f.dryRun, "dry-run", false, "report only")
	fs.BoolVar(&f.noUpload, "no-upload", false, "skip uploads")
	fs.BoolVar(&f.force, "force", false, "treat content as changed")
	fs.StringVar(&f.every, "every", "", "repeat every interval, e.g. 6h or 1d")
	fs.BoolVar(&f.verbose, "v", false, "debug logging")
	err := fs.Parse(args)
	return f, err
}

func newLogger(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

func checkCmd(args []string, stdout, stderr io.Writer) int {
	f, err := parseRunFlags("check", args, stderr)
	if err != nil {
		return 2
	}
	cfg, err := config.Load(f.config)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "JOB\tKIND\tMODE\tORDER\tSOURCES\tOUTPUTS")
	for _, j := range cfg.Jobs {
		var outs []string
		for _, o := range j.Outputs {
			s := o.Format + ":" + o.Path
			for _, t := range o.S3 {
				s += " → s3:" + cfg.S3.Prefix + t.Key
			}
			outs = append(outs, s)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n", j.Name, j.Kind, j.Mode, j.Order, len(j.Sources), strings.Join(outs, ", "))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(stdout, "\nconfig ok: %d jobs, state dir %s, s3 bucket %q\n", len(cfg.Jobs), cfg.Resolve(cfg.StateDir), cfg.S3.Bucket)
	return 0
}

func runCmd(args []string, stdout, stderr io.Writer) int {
	f, err := parseRunFlags("run", args, stderr)
	if err != nil {
		return 2
	}
	log := newLogger(stderr, f.verbose)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	once := func(ctx context.Context) int { return runOnce(ctx, f, stdout, stderr, log) }
	if f.every == "" {
		return once(ctx)
	}
	interval, err := parseEvery(f.every)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error: --every:", err)
		return 2
	}
	log.Info("running continuously", "every", interval)
	return runLoop(ctx, interval, stdout, once)
}

// minEvery keeps a misconfigured loop from hammering the upstream lists.
const minEvery = time.Minute

// parseEvery accepts Go durations ("30m", "6h", "1h30m") plus whole days
// ("1d", "2d") and enforces a one-minute minimum.
func parseEvery(s string) (time.Duration, error) {
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("%q: days must be a whole number, e.g. 1d", s)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, fmt.Errorf("%q: use a duration such as 30m, 6h or 1d", s)
		}
	}
	if d < minEvery {
		return 0, fmt.Errorf("%q: must be at least %s", s, minEvery)
	}
	return d, nil
}

// runLoop calls once immediately and then every interval, measured from
// the start of each run, until ctx is cancelled. Failures are reported by
// each run and never stop the loop; the exit code is 0 on cancellation.
func runLoop(ctx context.Context, interval time.Duration, stdout io.Writer, once func(context.Context) int) int {
	for {
		start := time.Now()
		once(ctx)
		if ctx.Err() != nil {
			return 0
		}
		wait := max(interval-time.Since(start), 0)
		next := time.Now().Add(wait)
		_, _ = fmt.Fprintf(stdout, "next run at %s (in %s)\n", next.Format(time.RFC3339), wait.Round(time.Second))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0
		case <-timer.C:
		}
	}
}

// runOnce loads the config and runs the selected jobs one time.
func runOnce(ctx context.Context, f runFlags, stdout, stderr io.Writer, log *slog.Logger) int {
	cfg, err := config.Load(f.config)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	runner := &pipeline.Runner{
		Cfg: cfg,
		Fetcher: &fetch.Fetcher{
			Client:    &http.Client{Timeout: cfg.HTTP.Timeout},
			Retries:   cfg.HTTP.Retries,
			UserAgent: cfg.HTTP.UserAgent,
			MaxBody:   cfg.HTTP.MaxBodyBytes,
			BaseDir:   cfg.BaseDir,
			Log:       log,
		},
		Store:    state.Store{Dir: cfg.Resolve(cfg.StateDir)},
		Log:      log,
		DryRun:   f.dryRun,
		NoUpload: f.noUpload,
		Force:    f.force,
	}
	if !f.dryRun && !f.noUpload && wantsS3(cfg) {
		pub, err := publish.NewS3(ctx, publish.S3Options{Region: cfg.S3.Region, Endpoint: cfg.S3.Endpoint, ForcePathStyle: cfg.S3.ForcePathStyle})
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		runner.Publisher = pub
	}

	results := runner.Run(ctx, f.jobs)
	_, _ = fmt.Fprintf(stdout, "run at %s\n", time.Now().UTC().Format(time.RFC3339))
	printResults(stdout, results, f.dryRun)
	for _, r := range results {
		if r.Err != nil {
			return 1
		}
	}
	if err := ctx.Err(); errors.Is(err, context.Canceled) {
		return 1
	}
	return 0
}

// wantsS3 reports whether any output could be uploaded.
func wantsS3(cfg *config.Config) bool {
	for _, j := range cfg.Jobs {
		for _, o := range j.Outputs {
			for _, t := range o.S3 {
				if t.Bucket != "" || cfg.S3.Bucket != "" {
					return true
				}
			}
		}
	}
	return false
}

func printResults(w io.Writer, results []pipeline.Result, dryRun bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	verb := "WRITTEN\tUPLOADED"
	if dryRun {
		verb = "WOULD WRITE\tWOULD UPLOAD"
	}
	_, _ = fmt.Fprintf(tw, "JOB\tENTRIES\tCHANGED\t%s\tSTATUS\n", verb)
	for _, r := range results {
		status := "ok"
		if r.Err != nil {
			status = "error: " + r.Err.Error()
		}
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%v\t%d\t%d\t%s\n", r.Job, r.Entries, r.Changed, len(r.Written), len(r.Uploaded), status)
	}
	_ = tw.Flush()
}
