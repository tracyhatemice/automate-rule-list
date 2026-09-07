package render

import (
	"bytes"
	"slices"
	"testing"
)

func render(t *testing.T, name, kind string, hdr Header, lines []string) string {
	t.Helper()
	r, ok := Lookup(name)
	if !ok {
		t.Fatalf("renderer %q not registered", name)
	}
	var buf bytes.Buffer
	if err := r.Render(&buf, kind, hdr, lines); err != nil {
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
	if err := r.Render(&bytes.Buffer{}, "ip", Header{}, nil); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}
