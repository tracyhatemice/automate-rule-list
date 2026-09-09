package pipeline

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tracyhatemice/automate-rule-list/internal/config"
	"github.com/tracyhatemice/automate-rule-list/internal/fetch"
	"github.com/tracyhatemice/automate-rule-list/internal/publish"
	"github.com/tracyhatemice/automate-rule-list/internal/state"
)

type env struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	content map[string]string
	status  map[string]int
	dir     string
	pub     *publish.Fake
	now     time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, content: map[string]string{}, status: map[string]int{}, dir: t.TempDir(), pub: &publish.Fake{}, now: time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		body, ok := e.content[r.URL.Path]
		code := e.status[r.URL.Path]
		e.mu.Unlock()
		if code != 0 {
			http.Error(w, "error", code)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) set(path, body string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.content[path] = body
	delete(e.status, path)
}

func (e *env) fail(path string, code int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status[path] = code
}

func (e *env) runner(yaml string, opts ...func(*Runner)) *Runner {
	e.t.Helper()
	yaml = strings.ReplaceAll(yaml, "SRV", e.srv.URL)
	cfg, err := config.Parse([]byte(yaml), e.dir)
	if err != nil {
		e.t.Fatalf("config: %v", err)
	}
	r := &Runner{
		Cfg:       cfg,
		Fetcher:   &fetch.Fetcher{Client: e.srv.Client(), Retries: 0, Backoff: time.Millisecond, BaseDir: e.dir, MaxBody: 1 << 20},
		Store:     state.Store{Dir: filepath.Join(e.dir, cfg.StateDir)},
		Publisher: e.pub,
		Now:       func() time.Time { return e.now },
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func (e *env) run(yaml string, opts ...func(*Runner)) Result {
	e.t.Helper()
	res := e.runner(yaml, opts...).Run(context.Background(), nil)
	if len(res) != 1 {
		e.t.Fatalf("expected one result, got %d", len(res))
	}
	return res[0]
}

func (e *env) read(rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.dir, rel))
	if err != nil {
		e.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func (e *env) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(e.dir, rel))
	return err == nil
}

func (e *env) writeFile(rel, body string) {
	e.t.Helper()
	p := filepath.Join(e.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

const domainCfg = `
s3: {bucket: bkt, prefix: "p/"}
jobs:
  - name: ad
    kind: domain
    sources:
      - url: SRV/list.txt
    outputs:
      - format: smartdns
        path: dist/ad.conf
        s3: {key: ad.conf}
      - format: clash
        path: dist/ad.yaml
`

func TestDomainJobLifecycle(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "b.com\nA.com\nsub.a.com\nbad_host\n")

	// 1. first run
	res := e.run(domainCfg)
	if res.Err != nil {
		t.Fatalf("first run: %v", res.Err)
	}
	if !res.Changed || res.Entries != 2 {
		t.Errorf("first run: changed=%v entries=%d", res.Changed, res.Entries)
	}
	if !slices.Equal(res.Written, []string{"dist/ad.conf", "dist/ad.yaml"}) || !slices.Equal(res.Uploaded, []string{"s3://bkt/p/ad.conf"}) {
		t.Errorf("first run: written=%v uploaded=%v", res.Written, res.Uploaded)
	}
	wantConf := "# last updated: 2026-09-07T01:02:03Z\naddress /a.com/#\naddress /b.com/#\n"
	if got := e.read("dist/ad.conf"); got != wantConf {
		t.Errorf("ad.conf =\n%q\nwant\n%q", got, wantConf)
	}
	if got := e.read("dist/ad.yaml"); got != "# last updated: 2026-09-07T01:02:03Z\npayload:\n  - DOMAIN-SUFFIX,a.com\n  - DOMAIN-SUFFIX,b.com\n" {
		t.Errorf("ad.yaml = %q", got)
	}
	if got := e.read("state/ad/intermediate.txt"); got != "a.com\nb.com\n" {
		t.Errorf("intermediate = %q", got)
	}
	if got := string(e.pub.Objects["bkt/p/ad.conf"]); got != wantConf {
		t.Errorf("uploaded body = %q", got)
	}

	// 2. unchanged run: nothing happens
	e.now = e.now.Add(time.Hour)
	res = e.run(domainCfg)
	if res.Err != nil || res.Changed || len(res.Written) != 0 || len(res.Uploaded) != 0 {
		t.Errorf("unchanged run: %+v", res)
	}
	if !slices.Equal(res.Skipped, []string{"dist/ad.conf", "dist/ad.yaml"}) {
		t.Errorf("unchanged skipped = %v", res.Skipped)
	}

	// 3. missing output is regenerated with the old timestamp, not re-uploaded
	if err := os.Remove(filepath.Join(e.dir, "dist/ad.conf")); err != nil {
		t.Fatal(err)
	}
	res = e.run(domainCfg)
	if res.Err != nil || res.Changed || !slices.Equal(res.Written, []string{"dist/ad.conf"}) || len(res.Uploaded) != 0 {
		t.Errorf("self-heal run: %+v", res)
	}
	if got := e.read("dist/ad.conf"); got != wantConf {
		t.Errorf("healed ad.conf = %q", got)
	}

	// 4. upstream change: new timestamp, everything rewritten and uploaded
	e.set("/list.txt", "b.com\na.com\nc.com\n")
	res = e.run(domainCfg)
	if res.Err != nil || !res.Changed || res.Entries != 3 {
		t.Fatalf("changed run: %+v", res)
	}
	if !slices.Equal(res.Written, []string{"dist/ad.conf", "dist/ad.yaml"}) || !slices.Equal(res.Uploaded, []string{"s3://bkt/p/ad.conf"}) {
		t.Errorf("changed run: written=%v uploaded=%v", res.Written, res.Uploaded)
	}
	if got := e.read("dist/ad.conf"); !strings.HasPrefix(got, "# last updated: 2026-09-07T02:02:03Z\n") || !strings.Contains(got, "address /c.com/#\n") {
		t.Errorf("changed ad.conf = %q", got)
	}

	// 5. force: same content, new timestamp, re-uploaded
	e.now = e.now.Add(time.Hour)
	res = e.run(domainCfg, func(r *Runner) { r.Force = true })
	if res.Err != nil || !res.Changed || len(res.Written) != 2 || len(res.Uploaded) != 1 {
		t.Errorf("forced run: %+v", res)
	}
	if got := e.read("dist/ad.conf"); !strings.HasPrefix(got, "# last updated: 2026-09-07T03:02:03Z\n") {
		t.Errorf("forced ad.conf = %q", got)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "a.com\n")
	res := e.run(domainCfg, func(r *Runner) { r.DryRun = true })
	if res.Err != nil || !res.Changed || res.Entries != 1 {
		t.Fatalf("dry run: %+v", res)
	}
	if e.exists("dist") || e.exists("state") || len(e.pub.Objects) != 0 {
		t.Error("dry run must not write or upload")
	}
	if !slices.Equal(res.Written, []string{"dist/ad.conf", "dist/ad.yaml"}) || !slices.Equal(res.Uploaded, []string{"s3://bkt/p/ad.conf"}) {
		t.Errorf("dry run should report planned work: %+v", res)
	}
}

func TestNoUploadThenUpload(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "a.com\n")
	res := e.run(domainCfg, func(r *Runner) { r.NoUpload = true })
	if res.Err != nil || len(res.Written) != 2 || len(res.Uploaded) != 0 || len(e.pub.Objects) != 0 {
		t.Fatalf("no-upload run: %+v", res)
	}
	res = e.run(domainCfg)
	if res.Err != nil || res.Changed || len(res.Written) != 0 || !slices.Equal(res.Uploaded, []string{"s3://bkt/p/ad.conf"}) {
		t.Fatalf("follow-up run: %+v", res)
	}
}

func TestUploadFailureIsRetriedNextRun(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "a.com\n")
	e.pub.Fail = os.ErrPermission
	res := e.run(domainCfg)
	if res.Err == nil || len(res.Written) != 2 {
		t.Fatalf("failed upload run: %+v", res)
	}
	e.pub.Fail = nil
	res = e.run(domainCfg)
	if res.Err != nil || res.Changed || !slices.Equal(res.Uploaded, []string{"s3://bkt/p/ad.conf"}) {
		t.Fatalf("retry run: %+v", res)
	}
}

func TestMissingBucketSkipsUpload(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "a.com\n")
	res := e.run(strings.Replace(domainCfg, "s3: {bucket: bkt, prefix: \"p/\"}\n", "", 1))
	if res.Err != nil || len(res.Written) != 2 || len(res.Uploaded) != 0 {
		t.Fatalf("no bucket: %+v", res)
	}
}

func TestRequiredSourceFailure(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "a.com\n")
	e.fail("/list.txt", http.StatusInternalServerError)
	res := e.run(domainCfg)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "500") {
		t.Fatalf("expected fetch error, got %v", res.Err)
	}
	if e.exists("state") || e.exists("dist") {
		t.Error("failed job must not write")
	}
	optional := strings.Replace(domainCfg, "- url: SRV/list.txt\n", "- url: SRV/list.txt\n        optional: true\n      - inline: x.com\n", 1)
	res = e.run(optional)
	if res.Err != nil || res.Entries != 1 {
		t.Fatalf("optional source: %+v", res)
	}
}

func TestSourceWithoutValidEntriesFails(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "Failed to fetch version info for ixmu/smartdns-conf.\n")
	res := e.run(domainCfg)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "no valid entries") {
		t.Fatalf("expected no-valid-entries error, got %v", res.Err)
	}
	optional := strings.Replace(domainCfg, "- url: SRV/list.txt\n", "- url: SRV/list.txt\n        optional: true\n      - inline: x.com\n", 1)
	if res = e.run(optional); res.Err != nil || res.Entries != 1 {
		t.Fatalf("optional junk source: %+v", res)
	}
}

func TestDomainFiltersAndOptions(t *testing.T) {
	e := newEnv(t)
	e.writeFile("allow.txt", "# allowlist\nads.example.com\n")
	res := e.run(`
jobs:
  - name: d
    kind: domain
    sources:
      - inline: |
          google
          google.com
          ads.example.com
          foo.example.com
          tracker.net
          x.tracker.net
          谷歌.com
        allow_tld: true
        filter:
          exclude: ["tracker.net"]
      - inline: "bad-only\n"
        optional: true
      - inline: "keep.example.org\ndrop.example.org\nalso-drop.example.org\n"
        filter:
          exclude_from:
            - inline: "drop.example.org\n"
            - file: allow.txt
          exclude: ["also-drop.example.org"]
    filter:
      exclude: ["/^foo\\./"]
      exclude_from:
        - file: allow.txt
    outputs:
      - {format: plain, path: out.txt}
`)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := e.read("out.txt"); got != "# last updated: 2026-09-07T01:02:03Z\ngoogle.com\nxn--flw351e.com\ngoogle\nkeep.example.org\n" {
		t.Errorf("out.txt = %q", got)
	}
}

func TestDomainSourceOrderAndClashSources(t *testing.T) {
	e := newEnv(t)
	e.set("/rules.yaml", "payload:\n  - DOMAIN-SUFFIX,z.com\n  - DOMAIN,www.y.com\n  - DOMAIN-KEYWORD,skip\n  - IP-CIDR,1.2.3.0/24\n  - '+.q.com'\n")
	res := e.run(`
jobs:
  - name: d
    kind: domain
    order: source
    sources:
      - inline: "b.com\na.com\n"
      - url: SRV/rules.yaml
    outputs:
      - {format: plain, path: out.txt, timestamp: false}
`)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := e.read("out.txt"); got != "b.com\na.com\nz.com\nwww.y.com\nq.com\n" {
		t.Errorf("out.txt = %q", got)
	}
}

func TestIPJob(t *testing.T) {
	e := newEnv(t)
	cfg := `
jobs:
  - name: ips
    kind: ip
AGG
    sources:
      - inline: |
          10.0.0.128/25
          10.0.0.0/25
          192.168.1.5 # private
          1.1.1.1
          not-an-ip
          2001:db8::1
        filter: { exclude: ["2001:db8::/32"] }
      - url: SRV/rules.yaml
    filter:
      exclude: ["192.168.0.0/16"]
    outputs:
      - {format: iplist, path: ips.txt, header: ["bad ips"]}
`
	e.set("/rules.yaml", "payload:\n  - IP-CIDR,8.8.8.0/24,no-resolve\n  - DOMAIN,skip.me\n")
	res := e.run(strings.Replace(cfg, "AGG\n", "", 1))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := e.read("ips.txt"); got != "# bad ips\n# last updated: 2026-09-07T01:02:03Z\n1.1.1.1\n8.8.8.0/24\n10.0.0.0/24\n" {
		t.Errorf("aggregated ips.txt = %q", got)
	}
	e2 := newEnv(t)
	e2.set("/rules.yaml", "payload:\n  - IP-CIDR,8.8.8.0/24,no-resolve\n")
	res = e2.run(strings.Replace(cfg, "AGG\n", "    aggregate: false\n", 1))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := e2.read("ips.txt"); got != "# bad ips\n# last updated: 2026-09-07T01:02:03Z\n1.1.1.1\n8.8.8.0/24\n10.0.0.0/25\n10.0.0.128/25\n" {
		t.Errorf("non-aggregated ips.txt = %q", got)
	}
}

func TestClashAggregate(t *testing.T) {
	e := newEnv(t)
	e.set("/netflix.yaml", "payload:\n  - DOMAIN-SUFFIX,netflix.com\n  - DOMAIN,www.netflix.com\n  - DOMAIN-KEYWORD,netflix\n  - IP-CIDR,1.2.3.0/24,no-resolve\n  - IP-CIDR,1.2.3.0/24\n  - BOGUS,rule\n")
	e.set("/v4.txt", "# AS2906\n5.6.7.0/24\n")
	res := e.run(`
jobs:
  - name: netflix
    kind: clash
    sources:
      - url: SRV/netflix.yaml
      - inline: "fast.com\nsub.fast.com\n"
      - url: SRV/v4.txt
      - inline: "2001:db8::/32\n"
        format: iplist
    filter:
      exclude: ["5.6.0.0/16"]
    outputs:
      - format: clash
        path: netflix.yaml
        header: ["aggregation of", "  SRV/netflix.yaml"]
`)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := "# aggregation of\n#   " + e.srv.URL + "/netflix.yaml\n# last updated: 2026-09-07T01:02:03Z\npayload:\n" +
		"  - DOMAIN-KEYWORD,netflix\n  - DOMAIN-SUFFIX,netflix.com\n  - DOMAIN-SUFFIX,fast.com\n" +
		"  - IP-CIDR,1.2.3.0/24,no-resolve\n  - IP-CIDR,2001:db8::/32,no-resolve\n"
	if got := e.read("netflix.yaml"); got != want {
		t.Errorf("netflix.yaml =\n%q\nwant\n%q", got, want)
	}
	if res.Entries != 5 {
		t.Errorf("entries = %d", res.Entries)
	}
}

func TestClashAggregateSorted(t *testing.T) {
	e := newEnv(t)
	res := e.run(`
jobs:
  - name: s
    kind: clash
    order: sorted
    sources:
      - inline: "payload:\n  - DOMAIN-SUFFIX,b.com\n  - DOMAIN-SUFFIX,a.org\n  - DOMAIN-SUFFIX,x.a.com\n  - IP-CIDR,9.0.0.0/8\n  - IP-CIDR,1.0.0.0/8\n  - DOMAIN-KEYWORD,z\n  - DOMAIN-KEYWORD,a\n"
    outputs:
      - {format: clash, path: s.yaml, timestamp: false}
`)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := "payload:\n  - DOMAIN-KEYWORD,a\n  - DOMAIN-KEYWORD,z\n  - DOMAIN-SUFFIX,x.a.com\n  - DOMAIN-SUFFIX,b.com\n  - DOMAIN-SUFFIX,a.org\n  - IP-CIDR,1.0.0.0/8\n  - IP-CIDR,9.0.0.0/8\n"
	if got := e.read("s.yaml"); got != want {
		t.Errorf("s.yaml =\n%q\nwant\n%q", got, want)
	}
}

func TestClashMirror(t *testing.T) {
	e := newEnv(t)
	e.set("/private.yaml", "payload:\n  # comment\n  - 'a.com'\n  - '+.b.com'\n  - DOMAIN-SUFFIX,c.com\n  - BOGUS,x\n  - DOMAIN-SUFFIX,c.com\n")
	cfg := `
jobs:
  - name: private
    kind: clash
    mode: mirror
    sources:
      - url: SRV/private.yaml
    outputs:
      - format: clash
        path: private.yaml
        header: ["mirror of SRV/private.yaml"]
`
	res := e.run(cfg)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := "# mirror of " + e.srv.URL + "/private.yaml\n# last updated: 2026-09-07T01:02:03Z\npayload:\n  - 'a.com'\n  - '+.b.com'\n  - DOMAIN-SUFFIX,c.com\n  - DOMAIN-SUFFIX,c.com\n"
	if got := e.read("private.yaml"); got != want {
		t.Errorf("private.yaml =\n%q\nwant\n%q", got, want)
	}
	// comment-only upstream change is not a change
	e.set("/private.yaml", "# new header comment\npayload:\n  - 'a.com'   # trailing\n  - '+.b.com'\n  - DOMAIN-SUFFIX,c.com\n  - DOMAIN-SUFFIX,c.com\n")
	e.now = e.now.Add(time.Hour)
	if res = e.run(cfg); res.Err != nil || res.Changed {
		t.Errorf("comment change: %+v", res)
	}
	// payload change is a change
	e.set("/private.yaml", "payload:\n  - 'a.com'\n  - '+.b.com'\n  - DOMAIN-SUFFIX,c.com\n  - DOMAIN-SUFFIX,c.com\n  - DOMAIN,d.com\n")
	if res = e.run(cfg); res.Err != nil || !res.Changed {
		t.Errorf("payload change: %+v", res)
	}
	if got := e.read("private.yaml"); !strings.HasPrefix(got, "# mirror of ") || !strings.Contains(got, "# last updated: 2026-09-07T02:02:03Z\n") || !strings.HasSuffix(got, "  - DOMAIN,d.com\n") {
		t.Errorf("updated mirror = %q", got)
	}
	// a mirror source with nothing valid fails
	e.set("/private.yaml", "payload:\n  - BOGUS,x\n")
	if res = e.run(cfg); res.Err == nil {
		t.Error("mirror with no valid rules should fail")
	}
}

func TestRunSelectsJobsAndIsolatesFailures(t *testing.T) {
	e := newEnv(t)
	e.set("/a.txt", "a.com\n")
	e.fail("/b.txt", http.StatusBadGateway)
	cfg := `
jobs:
  - {name: a, kind: domain, sources: [{url: SRV/a.txt}], outputs: [{format: plain, path: a.txt}]}
  - {name: b, kind: domain, sources: [{url: SRV/b.txt}], outputs: [{format: plain, path: b.txt}]}
`
	res := e.runner(cfg).Run(context.Background(), nil)
	if len(res) != 2 || res[0].Err != nil || res[1].Err == nil {
		t.Fatalf("results = %+v", res)
	}
	res = e.runner(cfg).Run(context.Background(), []string{"a", "nope"})
	if len(res) != 2 || res[0].Job != "a" || res[0].Err != nil || res[1].Job != "nope" || res[1].Err == nil {
		t.Fatalf("selected results = %+v", res)
	}
}

func TestClashExcludeByRuleText(t *testing.T) {
	e := newEnv(t)
	e.set("/netflix.yaml", "payload:\n  - DOMAIN-KEYWORD,apiproxy-device-prod-nlb-\n  - DOMAIN-KEYWORD,dualstack.apiproxy-\n  - DOMAIN-KEYWORD,netflixdnstest\n  - DOMAIN-SUFFIX,netflix.com\n  - IP-CIDR,1.2.3.0/24,no-resolve\n  - GEOIP,CN\n")
	res := e.run(`
jobs:
  - name: netflix
    kind: clash
    sources:
      - url: SRV/netflix.yaml
        filter: { exclude: ["=GEOIP,CN"] }
    filter:
      exclude:
        - "=DOMAIN-KEYWORD,apiproxy-device-prod-nlb-"
        - "/^DOMAIN-KEYWORD,dualstack/"
        - "=ip-cidr,1.2.3.0/24"
    outputs:
      - {format: clash, path: netflix.yaml, timestamp: false}
`)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := "payload:\n  - DOMAIN-KEYWORD,netflixdnstest\n  - DOMAIN-SUFFIX,netflix.com\n"
	if got := e.read("netflix.yaml"); got != want {
		t.Errorf("netflix.yaml =\n%q\nwant\n%q", got, want)
	}
}

func TestMultipleS3Targets(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "a.com\n")
	one := `
s3: {bucket: bkt}
jobs:
  - name: ad
    kind: domain
    sources: [{url: SRV/list.txt}]
    outputs:
      - format: plain
        path: ad.txt
        s3:
          - key: ad.txt
          - {key: mirror/ad.txt, bucket: other}
`
	res := e.run(one)
	if res.Err != nil || !slices.Equal(res.Uploaded, []string{"s3://bkt/ad.txt", "s3://other/mirror/ad.txt"}) {
		t.Fatalf("first run: %+v", res)
	}
	if len(e.pub.Objects) != 2 {
		t.Fatalf("objects = %v", e.pub.Objects)
	}
	// unchanged: nothing uploaded again
	if res = e.run(one); res.Err != nil || len(res.Uploaded) != 0 {
		t.Fatalf("unchanged run: %+v", res)
	}
	// a third target added later gets uploaded on its own, the others are left alone
	three := strings.Replace(one, "          - {key: mirror/ad.txt, bucket: other}\n", "          - {key: mirror/ad.txt, bucket: other}\n          - key: extra/ad.txt\n", 1)
	res = e.run(three)
	if res.Err != nil || res.Changed || !slices.Equal(res.Uploaded, []string{"s3://bkt/extra/ad.txt"}) {
		t.Fatalf("added target run: %+v", res)
	}
	// one target failing does not block the others and is retried next run
	e.pub.Fail = os.ErrPermission
	e.set("/list.txt", "a.com\nb.com\n")
	if res = e.run(three); res.Err == nil || len(res.Uploaded) != 0 {
		t.Fatalf("failing run: %+v", res)
	}
	e.pub.Fail = nil
	if res = e.run(three); res.Err != nil || res.Changed || len(res.Uploaded) != 3 {
		t.Fatalf("retry run: %+v", res)
	}
}

func TestClashDomainBehaviorOutput(t *testing.T) {
	e := newEnv(t)
	e.set("/list.txt", "b.com\nsub.a.com\na.com\n")
	res := e.run(`
jobs:
  - name: ad
    kind: domain
    sources: [{url: SRV/list.txt}]
    outputs:
      - {format: clash, path: ad.yaml, behavior: domain, timestamp: false}
      - {format: clash, path: ad-classical.yaml, timestamp: false}
`)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := e.read("ad.yaml"); got != "payload:\n  - '+.a.com'\n  - '+.b.com'\n" {
		t.Errorf("domain behavior = %q", got)
	}
	if got := e.read("ad-classical.yaml"); got != "payload:\n  - DOMAIN-SUFFIX,a.com\n  - DOMAIN-SUFFIX,b.com\n" {
		t.Errorf("classical = %q", got)
	}
}

func TestClashKindDomainBehaviorOutput(t *testing.T) {
	e := newEnv(t)
	e.set("/google.yaml", "payload:\n  - DOMAIN-SUFFIX,google.com\n  - DOMAIN,www.gstatic.com\n  - DOMAIN-WILDCARD,*.googleapis.com\n")
	e.set("/private.txt", "payload:\n  - 'router.asus.com'\n  - '+.internal'\n")
	cfg := `
jobs:
  - name: google
    kind: clash
    sources:
      - url: SRV/google.yaml
      - inline: "gvt1.com\n"
    outputs:
      - {format: clash, path: google.yaml, behavior: domain, timestamp: false}
  - name: private
    kind: clash
    mode: mirror
    sources: [{url: SRV/private.txt}]
    outputs:
      - {format: clash, path: private.yaml, behavior: domain, timestamp: false}
`
	res := e.runner(cfg).Run(context.Background(), nil)
	if res[0].Err != nil || res[1].Err != nil {
		t.Fatalf("results: %+v", res)
	}
	if got := e.read("google.yaml"); got != "payload:\n  - 'www.gstatic.com'\n  - '+.google.com'\n  - '+.gvt1.com'\n  - '*.googleapis.com'\n" {
		t.Errorf("google.yaml = %q", got)
	}
	if got := e.read("private.yaml"); got != "payload:\n  - 'router.asus.com'\n  - '+.internal'\n" {
		t.Errorf("private.yaml = %q", got)
	}
	// a rule that cannot be expressed as a domain wildcard fails the job loudly
	e.set("/google.yaml", "payload:\n  - DOMAIN-SUFFIX,google.com\n  - DOMAIN-KEYWORD,google\n")
	res = e.runner(cfg).Run(context.Background(), []string{"google"})
	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "DOMAIN-KEYWORD,google") {
		t.Fatalf("expected error naming the keyword rule, got %v", res[0].Err)
	}
}
