// Package config loads and validates the YAML configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/tracyhatemice/automate-rule-list/internal/parse"
	"github.com/tracyhatemice/automate-rule-list/internal/render"
)

// Version is stamped into the default User-Agent by the CLI.
var Version = "dev"

// Defaults.
const (
	DefaultStateDir        = "state"
	DefaultTimestampLayout = "2006-01-02T15:04:05Z"
	DefaultTimeout         = 60 * time.Second
	DefaultRetries         = 3
	DefaultConcurrency     = 8
	DefaultMaxBodyBytes    = 64 << 20
)

// Config is the validated configuration.
type Config struct {
	StateDir        string
	TimestampLayout string
	HTTP            HTTP
	S3              S3
	Jobs            []Job
	BaseDir         string // directory of the config file; relative paths resolve against it
}

// HTTP configures fetching.
type HTTP struct {
	Timeout      time.Duration
	Retries      int
	UserAgent    string
	Concurrency  int
	MaxBodyBytes int64
}

// S3 configures publishing.
type S3 struct {
	Bucket         string
	Region         string
	Endpoint       string
	Prefix         string
	ForcePathStyle bool
}

// Job is one list to build.
type Job struct {
	Name      string
	Kind      string // domain | ip | clash
	Mode      string // aggregate | mirror
	Order     string // sorted | source
	Aggregate *bool  // ip jobs: merge adjacent prefixes (default true)
	AllowTLD  bool   // domain jobs: accept single-label names
	Sources   []Source
	Filter    Filter
	Outputs   []Output
}

// AggregateEnabled reports whether adjacent prefixes are merged.
func (j Job) AggregateEnabled() bool { return j.Aggregate == nil || *j.Aggregate }

// Source is one input.
type Source struct {
	URL      string
	File     string
	Inline   string
	Format   string // parser name or "auto"
	Encoding string // plain | base64 | auto
	Filter   Filter // applied to this source's items only
	AllowTLD bool
	Optional bool
}

// Filter holds exclude patterns and sources whose items become patterns.
// The same block is accepted at job level and per source.
type Filter struct {
	Exclude     []string
	ExcludeFrom []Source
}

// Output is one rendered file.
type Output struct {
	Format    string
	Path      string
	Behavior  string // clash only: classical | domain | ipcidr
	Header    []string
	Timestamp *bool
	S3        []S3Target // every target receives the same file; nil when not uploaded
}

// WithTimestamp reports whether the "# last updated" line is written.
func (o Output) WithTimestamp() bool { return o.Timestamp == nil || *o.Timestamp }

// S3Target is where an output is uploaded.
type S3Target struct {
	Key    string
	Bucket string // overrides the global bucket
}

// Resolve makes p absolute relative to the config directory.
func (c *Config) Resolve(p string) string {
	if filepath.IsAbs(p) || c.BaseDir == "" {
		return p
	}
	return filepath.Join(c.BaseDir, p)
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data, dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// raw mirrors the YAML document; every unknown key is an error.
type raw struct {
	StateDir        string   `yaml:"state_dir"`
	TimestampLayout string   `yaml:"timestamp_layout"`
	HTTP            rawHTTP  `yaml:"http"`
	S3              S3Raw    `yaml:"s3"`
	Jobs            []rawJob `yaml:"jobs"`
}

type rawHTTP struct {
	Timeout      string `yaml:"timeout"`
	Retries      *int   `yaml:"retries"`
	UserAgent    string `yaml:"user_agent"`
	Concurrency  *int   `yaml:"concurrency"`
	MaxBodyBytes *int64 `yaml:"max_body_bytes"`
}

// S3Raw is the s3 section as written.
type S3Raw struct {
	Bucket         string `yaml:"bucket"`
	Region         string `yaml:"region"`
	Endpoint       string `yaml:"endpoint"`
	Prefix         string `yaml:"prefix"`
	ForcePathStyle bool   `yaml:"force_path_style"`
}

type rawJob struct {
	Name      string      `yaml:"name"`
	Kind      string      `yaml:"kind"`
	Mode      string      `yaml:"mode"`
	Order     string      `yaml:"order"`
	Aggregate *bool       `yaml:"aggregate"`
	AllowTLD  bool        `yaml:"allow_tld"`
	Sources   []rawSource `yaml:"sources"`
	Filter    rawFilter   `yaml:"filter"`
	Outputs   []rawOutput `yaml:"outputs"`
}

type rawSource struct {
	URL      string    `yaml:"url"`
	File     string    `yaml:"file"`
	Inline   string    `yaml:"inline"`
	Format   string    `yaml:"format"`
	Encoding string    `yaml:"encoding"`
	Filter   rawFilter `yaml:"filter"`
	AllowTLD bool      `yaml:"allow_tld"`
	Optional bool      `yaml:"optional"`
}

type rawFilter struct {
	Exclude     []string    `yaml:"exclude"`
	ExcludeFrom []rawSource `yaml:"exclude_from"`
}

type rawOutput struct {
	Format    string        `yaml:"format"`
	Path      string        `yaml:"path"`
	Behavior  string        `yaml:"behavior"`
	Header    []string      `yaml:"header"`
	Timestamp *bool         `yaml:"timestamp"`
	S3        *rawS3Targets `yaml:"s3"`
}

type rawS3Target struct {
	Key    string `yaml:"key"`
	Bucket string `yaml:"bucket"`
}

// rawS3Targets accepts either one mapping ("s3: {key: k}") or a list of
// mappings ("s3: [{key: a}, {key: b, bucket: x}]").
type rawS3Targets []rawS3Target

// UnmarshalYAML implements yaml.BytesUnmarshaler.
func (t *rawS3Targets) UnmarshalYAML(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "[") {
		var list []rawS3Target
		if err := yaml.UnmarshalWithOptions(b, &list, yaml.Strict()); err != nil {
			return err
		}
		*t = list
		return nil
	}
	var one rawS3Target
	if err := yaml.UnmarshalWithOptions(b, &one, yaml.Strict()); err != nil {
		return err
	}
	*t = rawS3Targets{one}
	return nil
}

var (
	nameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	kinds     = map[string]bool{"domain": true, "ip": true, "clash": true}
	modes     = map[string]bool{"aggregate": true, "mirror": true}
	orders    = map[string]bool{"sorted": true, "source": true}
	encodings = map[string]bool{"plain": true, "base64": true, "auto": true}
)

// Parse parses and validates a config document. Environment variables
// written as ${NAME} are expanded first. baseDir is recorded for
// resolving relative paths.
func Parse(data []byte, baseDir string) (*Config, error) {
	expanded := os.Expand(string(data), os.Getenv)
	var r raw
	if err := yaml.UnmarshalWithOptions([]byte(expanded), &r, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg := &Config{
		StateDir:        orDefault(r.StateDir, DefaultStateDir),
		TimestampLayout: orDefault(r.TimestampLayout, DefaultTimestampLayout),
		S3:              S3(r.S3),
		BaseDir:         baseDir,
	}
	var err error
	if cfg.HTTP, err = buildHTTP(r.HTTP); err != nil {
		return nil, err
	}
	if len(r.Jobs) == 0 {
		return nil, errors.New("config has no jobs")
	}
	seen := map[string]bool{}
	for i, rj := range r.Jobs {
		job, err := buildJob(rj)
		if err != nil {
			return nil, fmt.Errorf("job %d (%q): %w", i+1, rj.Name, err)
		}
		if seen[job.Name] {
			return nil, fmt.Errorf("job %q: duplicate name", job.Name)
		}
		seen[job.Name] = true
		cfg.Jobs = append(cfg.Jobs, job)
	}
	return cfg, nil
}

func buildHTTP(r rawHTTP) (HTTP, error) {
	h := HTTP{
		Timeout:      DefaultTimeout,
		Retries:      DefaultRetries,
		UserAgent:    orDefault(r.UserAgent, "rulegen/"+Version),
		Concurrency:  DefaultConcurrency,
		MaxBodyBytes: DefaultMaxBodyBytes,
	}
	if r.Timeout != "" {
		d, err := time.ParseDuration(r.Timeout)
		if err != nil || d <= 0 {
			return h, fmt.Errorf("http.timeout %q: must be a positive duration such as 30s", r.Timeout)
		}
		h.Timeout = d
	}
	if r.Retries != nil {
		if *r.Retries < 0 {
			return h, errors.New("http.retries must not be negative")
		}
		h.Retries = *r.Retries
	}
	if r.Concurrency != nil {
		if *r.Concurrency < 1 {
			return h, errors.New("http.concurrency must be at least 1")
		}
		h.Concurrency = *r.Concurrency
	}
	if r.MaxBodyBytes != nil {
		if *r.MaxBodyBytes < 1 {
			return h, errors.New("http.max_body_bytes must be positive")
		}
		h.MaxBodyBytes = *r.MaxBodyBytes
	}
	return h, nil
}

func buildJob(r rawJob) (Job, error) {
	defaultOrder := "sorted"
	if r.Kind == "clash" {
		defaultOrder = "source" // keep upstream rule order in rule-provider files
	}
	j := Job{
		Name:      r.Name,
		Kind:      r.Kind,
		Mode:      orDefault(r.Mode, "aggregate"),
		Order:     orDefault(r.Order, defaultOrder),
		Aggregate: r.Aggregate,
		AllowTLD:  r.AllowTLD,
	}
	switch {
	case !nameRe.MatchString(j.Name):
		return j, fmt.Errorf("name %q must match %s", j.Name, nameRe)
	case !kinds[j.Kind]:
		return j, fmt.Errorf("kind %q must be one of domain, ip, clash", j.Kind)
	case !modes[j.Mode]:
		return j, fmt.Errorf("mode %q must be aggregate or mirror", j.Mode)
	case j.Mode == "mirror" && j.Kind != "clash":
		return j, errors.New("mode mirror is only valid for clash jobs")
	case j.Mode == "mirror" && len(r.Sources) != 1:
		return j, errors.New("mode mirror needs exactly one source")
	case !orders[j.Order]:
		return j, fmt.Errorf("order %q must be sorted or source", j.Order)
	case len(r.Sources) == 0:
		return j, errors.New("no sources")
	case len(r.Outputs) == 0:
		return j, errors.New("no outputs")
	}
	for i, rs := range r.Sources {
		s, err := buildSource(rs)
		if err != nil {
			return j, fmt.Errorf("source %d: %w", i+1, err)
		}
		j.Sources = append(j.Sources, s)
	}
	var err error
	if j.Filter, err = buildFilter(r.Filter); err != nil {
		return j, fmt.Errorf("filter: %w", err)
	}
	paths := map[string]bool{}
	for i, ro := range r.Outputs {
		o, err := buildOutput(ro, j.Kind)
		if err != nil {
			return j, fmt.Errorf("output %d: %w", i+1, err)
		}
		if paths[o.Path] {
			return j, fmt.Errorf("output %d: duplicate path %q", i+1, o.Path)
		}
		paths[o.Path] = true
		j.Outputs = append(j.Outputs, o)
	}
	return j, nil
}

// buildFilter validates a filter block; exclude_from entries are sources.
func buildFilter(r rawFilter) (Filter, error) {
	f := Filter{Exclude: r.Exclude}
	for i, rs := range r.ExcludeFrom {
		s, err := buildSource(rs)
		if err != nil {
			return f, fmt.Errorf("exclude_from %d: %w", i+1, err)
		}
		f.ExcludeFrom = append(f.ExcludeFrom, s)
	}
	return f, nil
}

func buildSource(r rawSource) (Source, error) {
	s := Source{
		URL:      r.URL,
		File:     r.File,
		Inline:   r.Inline,
		Format:   orDefault(r.Format, "auto"),
		Encoding: orDefault(r.Encoding, "auto"),
		AllowTLD: r.AllowTLD,
		Optional: r.Optional,
	}
	var err error
	if s.Filter, err = buildFilter(r.Filter); err != nil {
		return s, fmt.Errorf("filter: %w", err)
	}
	set := 0
	for _, v := range []string{s.URL, s.File, s.Inline} {
		if v != "" {
			set++
		}
	}
	if set != 1 {
		return s, errors.New("needs exactly one of url, file or inline")
	}
	if s.Format != "auto" {
		if _, ok := parse.Lookup(s.Format); !ok {
			return s, fmt.Errorf("unknown format %q (known: %s)", s.Format, strings.Join(parse.Names(), ", "))
		}
	}
	if !encodings[s.Encoding] {
		return s, fmt.Errorf("encoding %q must be plain, base64 or auto", s.Encoding)
	}
	return s, nil
}

func buildOutput(r rawOutput, kind string) (Output, error) {
	o := Output{Format: r.Format, Path: r.Path, Behavior: r.Behavior, Header: r.Header, Timestamp: r.Timestamp}
	rd, ok := render.Lookup(o.Format)
	if !ok {
		return o, fmt.Errorf("unknown format %q (known: %s)", o.Format, strings.Join(render.Names(), ", "))
	}
	if !rd.Supports(kind) {
		return o, fmt.Errorf("format %q cannot render %s jobs", o.Format, kind)
	}
	if o.Format == "clash" {
		if err := render.ValidateClashBehavior(kind, render.Options{Behavior: o.Behavior}); err != nil {
			return o, err
		}
		o.Behavior = orDefault(o.Behavior, render.BehaviorClassical)
	} else if o.Behavior != "" {
		return o, fmt.Errorf("behavior is only valid for clash outputs, not %q", o.Format)
	}
	if o.Path == "" {
		return o, errors.New("path is required")
	}
	if r.S3 != nil {
		if len(*r.S3) == 0 {
			return o, errors.New("s3 must name at least one target")
		}
		seen := map[string]bool{}
		for i, t := range *r.S3 {
			if t.Key == "" {
				return o, fmt.Errorf("s3 target %d: key is required", i+1)
			}
			id := t.Bucket + "/" + t.Key
			if seen[id] {
				return o, fmt.Errorf("s3 target %d: duplicate key %q", i+1, t.Key)
			}
			seen[id] = true
			o.S3 = append(o.S3, S3Target(t))
		}
	}
	return o, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
