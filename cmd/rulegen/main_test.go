package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testCfg = `
jobs:
  - name: demo
    kind: domain
    sources:
      - inline: "b.com\na.com\n"
    outputs:
      - {format: plain, path: out/demo.txt, s3: {key: demo.txt}}
`

func writeCfg(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "rulegen.yaml")
	if err := os.WriteFile(path, []byte(testCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func exec(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestVersionAndUsage(t *testing.T) {
	if code, out, _ := exec(t, "version"); code != 0 || !strings.HasPrefix(out, "rulegen ") {
		t.Errorf("version: %d %q", code, out)
	}
	if code, _, errs := exec(t); code != 2 || !strings.Contains(errs, "usage") {
		t.Errorf("no args: %d %q", code, errs)
	}
	if code, _, errs := exec(t, "bogus"); code != 2 || !strings.Contains(errs, "unknown command") {
		t.Errorf("bogus: %d %q", code, errs)
	}
	if code, _, _ := exec(t, "run", "--nope"); code != 2 {
		t.Errorf("bad flag should exit 2, got %d", code)
	}
}

func TestCheck(t *testing.T) {
	_, path := writeCfg(t)
	code, out, _ := exec(t, "check", "-c", path)
	if code != 0 || !strings.Contains(out, "demo") || !strings.Contains(out, "domain") {
		t.Errorf("check: %d %q", code, out)
	}
	if code, _, errs := exec(t, "check", "-c", filepath.Join(t.TempDir(), "missing.yaml")); code != 1 || errs == "" {
		t.Errorf("check missing: %d %q", code, errs)
	}
}

func TestRunDryRunAndNoUpload(t *testing.T) {
	dir, path := writeCfg(t)
	code, out, _ := exec(t, "run", "-c", path, "--dry-run")
	if code != 0 || !strings.Contains(out, "demo") {
		t.Errorf("dry run: %d %q", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); err == nil {
		t.Error("dry run wrote outputs")
	}
	code, out, _ = exec(t, "run", "-c", path, "--no-upload", "-j", "demo")
	if code != 0 {
		t.Fatalf("run: %d %q", code, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "out", "demo.txt"))
	if err != nil || !strings.HasSuffix(string(b), "a.com\nb.com\n") {
		t.Errorf("output: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "demo", "state.json")); err != nil {
		t.Errorf("state missing: %v", err)
	}
	if code, out, _ = exec(t, "run", "-c", path, "-j", "nope"); code != 1 || !strings.Contains(out, "nope") {
		t.Errorf("unknown job: %d %q", code, out)
	}
}

func TestParseEvery(t *testing.T) {
	good := map[string]time.Duration{
		"6h":    6 * time.Hour,
		"30m":   30 * time.Minute,
		"1h30m": 90 * time.Minute,
		"1d":    24 * time.Hour,
		"2d":    48 * time.Hour,
	}
	for in, want := range good {
		if got, err := parseEvery(in); err != nil || got != want {
			t.Errorf("parseEvery(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "30s", "-1h", "abc", "1.5d", "1w"} {
		if got, err := parseEvery(in); err == nil {
			t.Errorf("parseEvery(%q) = %v, want error", in, got)
		}
	}
}

func TestRunLoopRepeatsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var runs atomic.Int32
	var out bytes.Buffer
	code := runLoop(ctx, 5*time.Millisecond, &out, func(context.Context) int {
		if runs.Add(1) == 3 {
			cancel()
		}
		return 1 // failures must not stop the loop
	})
	if code != 0 {
		t.Errorf("code = %d, want 0 on cancellation", code)
	}
	if n := runs.Load(); n != 3 {
		t.Errorf("runs = %d, want 3", n)
	}
	if !strings.Contains(out.String(), "next run") {
		t.Errorf("expected a 'next run' line, got %q", out.String())
	}
}

func TestRunEveryFlagRejectsBadInterval(t *testing.T) {
	_, path := writeCfg(t)
	if code, _, errs := exec(t, "run", "-c", path, "--every", "10s"); code != 2 || !strings.Contains(errs, "every") {
		t.Errorf("bad --every: %d %q", code, errs)
	}
}
