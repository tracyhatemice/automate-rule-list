package render

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func render(t *testing.T, name, kind string, hdr Header, lines []string) string {
	t.Helper()
	return renderOpt(t, name, kind, hdr, lines, Options{})
}

func renderOpt(t *testing.T, name, kind string, hdr Header, lines []string, opt Options) string {
	t.Helper()
	r, ok := Lookup(name)
	if !ok {
		t.Fatalf("renderer %q not registered", name)
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, kind, hdr, lines, opt); err != nil {
		t.Fatalf("%s/%s: %v", name, kind, err)
	}
	return buf.String()
}

func TestNames(t *testing.T) {
	if got := Names(); !slices.Equal(got, []string{"clash", "iplist", "plain", "smartdns"}) {
		t.Fatalf("Names = %v", got)
	}
}

func TestSupports(t *testing.T) {
	tests := map[string]map[string]bool{
		"plain":    {"domain": true, "ip": true, "clash": false},
		"iplist":   {"domain": false, "ip": true, "clash": false},
		"smartdns": {"domain": true, "ip": false, "clash": false},
		"clash":    {"domain": true, "ip": true, "clash": true},
	}
	for name, kinds := range tests {
		r, _ := Lookup(name)
		for kind, want := range kinds {
			if got := r.Supports(kind); got != want {
				t.Errorf("%s.Supports(%s) = %v, want %v", name, kind, got, want)
			}
		}
	}
}

func TestHeader(t *testing.T) {
	hdr := Header{Lines: []string{"mirror of https://x", "alt-src: https://y"}, Timestamp: "2026-09-07T00:00:00Z"}
	want := "# mirror of https://x\n# alt-src: https://y\n# last updated: 2026-09-07T00:00:00Z\na.com\n"
	if got := render(t, "plain", "domain", hdr, []string{"a.com"}); got != want {
		t.Errorf("plain =\n%q\nwant\n%q", got, want)
	}
	if got := render(t, "plain", "domain", Header{}, []string{"a.com"}); got != "a.com\n" {
		t.Errorf("no header = %q", got)
	}
	if got := render(t, "plain", "ip", Header{Timestamp: "t"}, nil); got != "# last updated: t\n" {
		t.Errorf("empty lines = %q", got)
	}
}

func TestSmartdns(t *testing.T) {
	want := "# last updated: t\naddress /a.com/#\naddress /b.a.com/#\n"
	if got := render(t, "smartdns", "domain", Header{Timestamp: "t"}, []string{"a.com", "b.a.com"}); got != want {
		t.Errorf("got %q", got)
	}
}

func TestClash(t *testing.T) {
	tests := []struct {
		kind  string
		lines []string
		want  string
	}{
		{"domain", []string{"a.com", "goog"}, "# last updated: t\npayload:\n  - DOMAIN-SUFFIX,a.com\n  - DOMAIN-SUFFIX,goog\n"},
		{"ip", []string{"1.2.3.4", "10.0.0.0/8", "2001:db8::1", "2001:db8::/32"}, "# last updated: t\npayload:\n  - IP-CIDR,1.2.3.4/32,no-resolve\n  - IP-CIDR,10.0.0.0/8,no-resolve\n  - IP-CIDR,2001:db8::1/128,no-resolve\n  - IP-CIDR,2001:db8::/32,no-resolve\n"},
		{"clash", []string{"DOMAIN,a.com", "+.b.com", "IP-CIDR,10.0.0.0/8,no-resolve", "'c.com'"}, "# last updated: t\npayload:\n  - DOMAIN,a.com\n  - '+.b.com'\n  - IP-CIDR,10.0.0.0/8,no-resolve\n  - 'c.com'\n"},
		{"clash", nil, "# last updated: t\npayload: []\n"},
	}
	for _, tc := range tests {
		if got := render(t, "clash", tc.kind, Header{Timestamp: "t"}, tc.lines); got != tc.want {
			t.Errorf("clash/%s =\n%q\nwant\n%q", tc.kind, got, tc.want)
		}
	}
}

func TestIPList(t *testing.T) {
	want := "# last updated: t\n1.2.3.4\n10.0.0.0/8\n"
	if got := render(t, "iplist", "ip", Header{Timestamp: "t"}, []string{"1.2.3.4", "10.0.0.0/8"}); got != want {
		t.Errorf("got %q", got)
	}
}

func TestUnsupportedKind(t *testing.T) {
	r, _ := Lookup("smartdns")
	if err := r.Render(&bytes.Buffer{}, "ip", Header{}, nil, Options{}); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestClashBehavior(t *testing.T) {
	tests := []struct {
		kind, behavior string
		lines          []string
		want           string
	}{
		{"domain", "domain", []string{"a.com", "goog", "xn--flw351e.com"}, "payload:\n  - '+.a.com'\n  - '+.goog'\n  - '+.xn--flw351e.com'\n"},
		{"domain", "classical", []string{"a.com"}, "payload:\n  - DOMAIN-SUFFIX,a.com\n"},
		{"domain", "", []string{"a.com"}, "payload:\n  - DOMAIN-SUFFIX,a.com\n"},
		{"ip", "ipcidr", []string{"1.2.3.4", "10.0.0.0/8", "2001:db8::/32"}, "payload:\n  - '1.2.3.4/32'\n  - '10.0.0.0/8'\n  - '2001:db8::/32'\n"},
		{"ip", "classical", []string{"1.2.3.4"}, "payload:\n  - IP-CIDR,1.2.3.4/32,no-resolve\n"},
		{"domain", "domain", nil, "payload: []\n"},
	}
	for _, tc := range tests {
		got := renderOpt(t, "clash", tc.kind, Header{}, tc.lines, Options{Behavior: tc.behavior})
		if got != tc.want {
			t.Errorf("clash/%s behavior=%q =\n%q\nwant\n%q", tc.kind, tc.behavior, got, tc.want)
		}
	}
	r, _ := Lookup("clash")
	for _, bad := range []struct{ kind, behavior string }{{"domain", "ipcidr"}, {"ip", "domain"}, {"domain", "bogus"}, {"clash", "bogus"}} {
		if err := r.Render(&bytes.Buffer{}, bad.kind, Header{}, []string{"x"}, Options{Behavior: bad.behavior}); err == nil {
			t.Errorf("kind %s behavior %q should be rejected", bad.kind, bad.behavior)
		}
	}
}

func TestClashKindDomainBehavior(t *testing.T) {
	lines := []string{
		"DOMAIN,a.com",
		"DOMAIN-SUFFIX,b.com",
		"DOMAIN-WILDCARD,*.c.com",
		"DOMAIN-WILDCARD,sub.*.d.com",
		"+.e.com",
		".f.com",
		"'g.com'",
		"DOMAIN-SUFFIX,goog",
	}
	want := "payload:\n  - 'a.com'\n  - '+.b.com'\n  - '*.c.com'\n  - 'sub.*.d.com'\n  - '+.e.com'\n  - '.f.com'\n  - 'g.com'\n  - '+.goog'\n"
	if got := renderOpt(t, "clash", "clash", Header{}, lines, Options{Behavior: "domain"}); got != want {
		t.Errorf("clash/clash domain =\n%q\nwant\n%q", got, want)
	}
	r, _ := Lookup("clash")
	for _, bad := range []string{
		"DOMAIN-KEYWORD,telegram",
		"IP-CIDR,1.2.3.0/24,no-resolve",
		"DOMAIN-WILDCARD,cdn*.x.com",
		"DOMAIN-WILDCARD,a.+.b.com",
		"DOMAIN-WILDCARD,+",
		"GEOSITE,cn",
		"DOMAIN-REGEX,^ads",
		"MATCH",
	} {
		err := r.Render(&bytes.Buffer{}, "clash", Header{}, []string{"DOMAIN,ok.com", bad}, Options{Behavior: "domain"})
		if err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: expected an error naming the rule, got %v", bad, err)
		}
	}
}

func TestClashKindIPCIDRBehavior(t *testing.T) {
	lines := []string{"IP-CIDR,1.2.3.0/24,no-resolve", "IP-CIDR6,2001:db8::/32", "10.0.0.0/8", "IP-CIDR,9.9.9.9"}
	want := "payload:\n  - '1.2.3.0/24'\n  - '2001:db8::/32'\n  - '10.0.0.0/8'\n  - '9.9.9.9/32'\n"
	if got := renderOpt(t, "clash", "clash", Header{}, lines, Options{Behavior: "ipcidr"}); got != want {
		t.Errorf("clash/clash ipcidr =\n%q\nwant\n%q", got, want)
	}
	r, _ := Lookup("clash")
	if err := r.Render(&bytes.Buffer{}, "clash", Header{}, []string{"DOMAIN-SUFFIX,a.com"}, Options{Behavior: "ipcidr"}); err == nil || !strings.Contains(err.Error(), "DOMAIN-SUFFIX,a.com") {
		t.Errorf("expected error naming the domain rule, got %v", err)
	}
}
