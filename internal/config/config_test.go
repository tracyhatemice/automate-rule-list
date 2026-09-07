package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const minimal = `
jobs:
  - name: adblock
    kind: domain
    sources:
      - url: https://example.com/list.txt
    outputs:
      - format: plain
        path: dist/adblock.txt
`

func parseYAML(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	return Parse([]byte(yaml), "/base")
}

func TestDefaults(t *testing.T) {
	cfg, err := parseYAML(t, minimal)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateDir != "state" || cfg.TimestampLayout != "2006-01-02T15:04:05Z" {
		t.Errorf("defaults: %+v", cfg)
	}
	if cfg.HTTP.Timeout != 60*time.Second || cfg.HTTP.Retries != 3 || cfg.HTTP.Concurrency != 8 || cfg.HTTP.MaxBodyBytes != 64<<20 {
		t.Errorf("http defaults: %+v", cfg.HTTP)
	}
	if !strings.HasPrefix(cfg.HTTP.UserAgent, "rulegen/") {
		t.Errorf("user agent = %q", cfg.HTTP.UserAgent)
	}
	if cfg.BaseDir != "/base" {
		t.Errorf("BaseDir = %q", cfg.BaseDir)
	}
	job := cfg.Jobs[0]
	if job.Mode != "aggregate" || job.Order != "sorted" {
		t.Errorf("job defaults: %+v", job)
	}
	if job.Sources[0].Format != "auto" || job.Sources[0].Encoding != "auto" {
		t.Errorf("source defaults: %+v", job.Sources[0])
	}
	if !job.Outputs[0].WithTimestamp() {
		t.Error("timestamp should default to true")
	}
	if cfg.Resolve("dist/x") != "/base/dist/x" || cfg.Resolve("/abs") != "/abs" {
		t.Errorf("Resolve: %q %q", cfg.Resolve("dist/x"), cfg.Resolve("/abs"))
	}
}

func TestFullConfig(t *testing.T) {
	t.Setenv("RULEGEN_TEST_BUCKET", "my-bucket")
	cfg, err := parseYAML(t, `
state_dir: var/state
timestamp_layout: "2006-01-02"
http:
  timeout: 10s
  retries: 1
  user_agent: custom/1
  concurrency: 2
  max_body_bytes: 1024
s3:
  bucket: ${RULEGEN_TEST_BUCKET}
  region: us-east-1
  endpoint: http://minio:9000
  prefix: lists/
  force_path_style: true
jobs:
  - name: badips
    kind: ip
    aggregate: false
    sources:
      - url: https://example.com/ips.txt
        format: iplist
        optional: true
      - inline: |
          10.0.0.0/8
        filter:
          exclude: ["10.1.0.0/16"]
          exclude_from:
            - inline: "10.2.0.0/16\n"
    filter:
      exclude: ["192.168.0.0/16"]
      exclude_from:
        - file: allow.txt
    outputs:
      - format: iplist
        path: dist/badips.txt
        timestamp: false
        header: ["a", "b"]
        s3:
          key: badips.txt
          bucket: other
  - name: disney
    kind: clash
    mode: mirror
    order: source
    sources:
      - url: https://example.com/disney.yaml
        format: clash
        encoding: plain
    outputs:
      - format: clash
        path: dist/clash/disney.yaml
        s3: { key: clash/disney.yaml }
  - name: pbr
    kind: domain
    allow_tld: true
    sources:
      - file: src/google.txt
        allow_tld: true
    outputs:
      - { format: smartdns, path: dist/pbr.conf }
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.S3.Bucket != "my-bucket" || !cfg.S3.ForcePathStyle || cfg.S3.Prefix != "lists/" {
		t.Errorf("s3: %+v", cfg.S3)
	}
	if cfg.HTTP.Timeout != 10*time.Second || cfg.HTTP.Retries != 1 || cfg.HTTP.UserAgent != "custom/1" || cfg.HTTP.Concurrency != 2 || cfg.HTTP.MaxBodyBytes != 1024 {
		t.Errorf("http: %+v", cfg.HTTP)
	}
	badips := cfg.Jobs[0]
	if badips.AggregateEnabled() {
		t.Error("aggregate: false not honoured")
	}
	if !badips.Sources[0].Optional || badips.Sources[1].Filter.Exclude[0] != "10.1.0.0/16" || badips.Sources[1].Filter.ExcludeFrom[0].Inline != "10.2.0.0/16\n" {
		t.Errorf("sources: %+v", badips.Sources)
	}
	if badips.Filter.Exclude[0] != "192.168.0.0/16" || badips.Filter.ExcludeFrom[0].File != "allow.txt" {
		t.Errorf("filter: %+v", badips.Filter)
	}
	out := badips.Outputs[0]
	if out.WithTimestamp() || len(out.Header) != 2 || len(out.S3) != 1 || out.S3[0].Key != "badips.txt" || out.S3[0].Bucket != "other" {
		t.Errorf("output: %+v", out)
	}
	if cfg.Jobs[1].Mode != "mirror" || cfg.Jobs[1].Order != "source" {
		t.Errorf("disney: %+v", cfg.Jobs[1])
	}
	if clashDefault, err := parseYAML(t, "jobs:\n  - {name: c, kind: clash, sources: [{url: http://x}], outputs: [{format: clash, path: p}]}\n"); err != nil || clashDefault.Jobs[0].Order != "source" {
		t.Errorf("clash default order: %v %v", err, clashDefault)
	}
	if cfg.Jobs[1].Mode != "mirror" {
		t.Errorf("disney: %+v", cfg.Jobs[1])
	}
	if !cfg.Jobs[2].AllowTLD || !cfg.Jobs[2].Sources[0].AllowTLD {
		t.Errorf("pbr: %+v", cfg.Jobs[2])
	}
	if cfg.Jobs[0].AggregateEnabled() || !cfg.Jobs[2].AggregateEnabled() {
		t.Error("AggregateEnabled defaults")
	}
}

func TestOutputS3List(t *testing.T) {
	cfg, err := parseYAML(t, `
jobs:
  - name: a
    kind: domain
    sources: [{url: http://x}]
    outputs:
      - format: plain
        path: p
        s3:
          - key: one.txt
          - { key: two.txt, bucket: other }
      - format: smartdns
        path: q
`)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Jobs[0].Outputs[0].S3
	if len(got) != 2 || got[0].Key != "one.txt" || got[0].Bucket != "" || got[1].Key != "two.txt" || got[1].Bucket != "other" {
		t.Errorf("s3 list = %+v", got)
	}
	if cfg.Jobs[0].Outputs[1].S3 != nil {
		t.Errorf("no s3 should be nil, got %+v", cfg.Jobs[0].Outputs[1].S3)
	}
	for name, yaml := range map[string]string{
		"list with unknown key":  "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p, s3: [{key: k, nope: 1}]}]}\n",
		"list entry without key": "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p, s3: [{bucket: b}]}]}\n",
		"duplicate target":       "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p, s3: [{key: k}, {key: k}]}]}\n",
		"empty list":             "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p, s3: []}]}\n",
		"scalar":                 "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p, s3: k}]}\n",
	} {
		if _, err := parseYAML(t, yaml); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	tests := map[string]string{
		"unknown key":             "jobs:\n  - name: a\n    kind: domain\n    bogus: 1\n    sources: [{url: http://x}]\n    outputs: [{format: plain, path: p}]\n",
		"no jobs":                 "jobs: []\n",
		"duplicate name":          "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p}]}\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p}]}\n",
		"bad kind":                "jobs:\n  - {name: a, kind: foo, sources: [{url: http://x}], outputs: [{format: plain, path: p}]}\n",
		"mirror on domain":        "jobs:\n  - {name: a, kind: domain, mode: mirror, sources: [{url: http://x}], outputs: [{format: plain, path: p}]}\n",
		"mirror two sources":      "jobs:\n  - {name: a, kind: clash, mode: mirror, sources: [{url: http://x}, {url: http://y}], outputs: [{format: clash, path: p}]}\n",
		"bad mode":                "jobs:\n  - {name: a, kind: clash, mode: copy, sources: [{url: http://x}], outputs: [{format: clash, path: p}]}\n",
		"bad order":               "jobs:\n  - {name: a, kind: domain, order: random, sources: [{url: http://x}], outputs: [{format: plain, path: p}]}\n",
		"source two locations":    "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x, file: y}], outputs: [{format: plain, path: p}]}\n",
		"source no location":      "jobs:\n  - {name: a, kind: domain, sources: [{format: domain}], outputs: [{format: plain, path: p}]}\n",
		"no sources":              "jobs:\n  - {name: a, kind: domain, sources: [], outputs: [{format: plain, path: p}]}\n",
		"bad format":              "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x, format: xml}], outputs: [{format: plain, path: p}]}\n",
		"bad encoding":            "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x, encoding: rot13}], outputs: [{format: plain, path: p}]}\n",
		"no outputs":              "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: []}\n",
		"bad output format":       "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: pdf, path: p}]}\n",
		"unsupported kind":        "jobs:\n  - {name: a, kind: ip, sources: [{url: http://x}], outputs: [{format: smartdns, path: p}]}\n",
		"empty path":              "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain}]}\n",
		"duplicate path":          "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p}, {format: smartdns, path: p}]}\n",
		"s3 without key":          "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p, s3: {bucket: b}}]}\n",
		"bad timeout":             "http: {timeout: soon}\njobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p}]}\n",
		"bad filter exclude_from": "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x}], filter: {exclude_from: [{}]}, outputs: [{format: plain, path: p}]}\n",
		"empty name":              "jobs:\n  - {name: '', kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p}]}\n",
		"name with slash":         "jobs:\n  - {name: a/b, kind: domain, sources: [{url: http://x}], outputs: [{format: plain, path: p}]}\n",
		"invalid yaml":            "jobs: [\n",
		"old per-source exclude":  "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x, exclude: [a.com]}], outputs: [{format: plain, path: p}]}\n",
		"bad source exclude_from": "jobs:\n  - {name: a, kind: domain, sources: [{url: http://x, filter: {exclude_from: [{}]}}], outputs: [{format: plain, path: p}]}\n",
	}
	for name, yaml := range tests {
		if _, err := parseYAML(t, yaml); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rulegen.yaml")
	if err := os.WriteFile(path, []byte(minimal), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseDir != dir {
		t.Errorf("BaseDir = %q, want %q", cfg.BaseDir, dir)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("missing file should error")
	}
}
