package ipset

import (
	"net/netip"
	"slices"
	"testing"
)

func mustPrefixes(t *testing.T, ss ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "10.0.0.1", want: "10.0.0.1/32"},
		{in: "10.0.0.1/24", want: "10.0.0.0/24"},
		{in: " 10.0.0.0/8\r", want: "10.0.0.0/8"},
		{in: "2001:db8::1", want: "2001:db8::1/128"},
		{in: "2001:DB8::/32", want: "2001:db8::/32"},
		{in: "::ffff:1.2.3.4", want: "1.2.3.4/32"},
		{in: "1.2.3", wantErr: true},
		{in: "1.2.3.4/33", wantErr: true},
		{in: "fe80::1%eth0", wantErr: true},
		{in: "example.com", wantErr: true},
		{in: "", wantErr: true},
		{in: "256.1.1.1", wantErr: true},
	}
	for _, tc := range tests {
		got, err := Parse(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFormat(t *testing.T) {
	tests := map[string]string{
		"10.0.0.1/32":     "10.0.0.1",
		"10.0.0.0/24":     "10.0.0.0/24",
		"2001:db8::1/128": "2001:db8::1",
		"2001:db8::/32":   "2001:db8::/32",
	}
	for in, want := range tests {
		if got := Format(netip.MustParsePrefix(in)); got != want {
			t.Errorf("Format(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestDedupe(t *testing.T) {
	in := mustPrefixes(t, "2001:db8::/32", "10.1.0.0/16", "10.0.0.0/8", "10.0.0.0/8", "192.168.1.1/32", "2001:db8:1::/48", "1.1.1.1/32")
	want := mustPrefixes(t, "1.1.1.1/32", "10.0.0.0/8", "192.168.1.1/32", "2001:db8::/32")
	if got := Dedupe(in); !slices.Equal(got, want) {
		t.Fatalf("Dedupe = %v, want %v", got, want)
	}
}

func TestAggregate(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"siblings merge", []string{"10.0.0.0/25", "10.0.0.128/25"}, []string{"10.0.0.0/24"}},
		{"four quarters merge", []string{"10.0.0.192/26", "10.0.0.0/26", "10.0.0.128/26", "10.0.0.64/26"}, []string{"10.0.0.0/24"}},
		{"non-adjacent stay", []string{"10.0.0.0/24", "10.0.2.0/24"}, []string{"10.0.0.0/24", "10.0.2.0/24"}},
		{"misaligned neighbours stay", []string{"10.0.1.0/24", "10.0.2.0/24"}, []string{"10.0.1.0/24", "10.0.2.0/24"}},
		{"hosts merge", []string{"1.1.1.0/32", "1.1.1.1/32"}, []string{"1.1.1.0/31"}},
		{"v6 siblings merge", []string{"2001:db8::/33", "2001:db8:8000::/33"}, []string{"2001:db8::/32"}},
		{"covered removed then merged", []string{"10.0.0.0/25", "10.0.0.130/32", "10.0.0.128/25"}, []string{"10.0.0.0/24"}},
		{"empty", nil, nil},
	}
	for _, tc := range tests {
		got := Aggregate(mustPrefixes(t, tc.in...))
		want := mustPrefixes(t, tc.want...)
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: Aggregate = %v, want %v", tc.name, got, want)
		}
	}
}

func TestMatcher(t *testing.T) {
	m, err := NewMatcher([]string{"10.0.0.0/8", "192.168.1.1", "/^172\\.16\\./"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 3 {
		t.Fatalf("Len = %d", m.Len())
	}
	tests := map[string]bool{
		"10.1.2.3/32":    true,
		"10.0.0.0/16":    true,
		"10.0.0.0/7":     false,
		"11.0.0.0/8":     false,
		"192.168.1.1/32": true,
		"192.168.1.0/24": false,
		"172.16.5.0/24":  true,
		"172.17.0.0/16":  false,
	}
	for in, want := range tests {
		if got := m.Match(netip.MustParsePrefix(in)); got != want {
			t.Errorf("Match(%s) = %v, want %v", in, got, want)
		}
	}
	if _, err := NewMatcher([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected error for bad pattern")
	}
	if _, err := NewMatcher([]string{"/(/"}); err == nil {
		t.Fatal("expected error for bad regex")
	}
	empty, _ := NewMatcher(nil)
	if empty.Match(netip.MustParsePrefix("10.0.0.0/8")) {
		t.Fatal("empty matcher must not match")
	}
}
