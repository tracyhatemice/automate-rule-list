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
	"strings"
	"syscall"
	"text/tabwriter"

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
	cfg, err := config.Load(f.config)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
