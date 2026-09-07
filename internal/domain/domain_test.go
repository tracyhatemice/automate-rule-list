package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		in       string
		allowTLD bool
		want     string
		wantErr  bool
	}{
		{in: "Example.COM", want: "example.com"},
		{in: "  foo.bar.\r", want: "foo.bar"},
		{in: "*.x.com", want: "x.com"},
		{in: "+.x.com", want: "x.com"},
		{in: ".x.com", want: "x.com"},
		{in: "https://a.b.com/path?q=1", want: "a.b.com"},
		{in: "http://a.b.com", want: "a.b.com"},
		{in: "a.b.com:8080", want: "a.b.com"},
		{in: "a.com/", want: "a.com"},
		{in: "'a.b.com'", want: "a.b.com"},
		{in: "\"a.b.com\"", want: "a.b.com"},
		{in: "bücher.example", want: "xn--bcher-kva.example"},
		{in: "谷歌.com", want: "xn--flw351e.com"},
		{in: "xn--flw351e", allowTLD: true, want: "xn--flw351e"},
		{in: "google", allowTLD: false, wantErr: true},
		{in: "google", allowTLD: true, want: "google"},
		{in: "1.2.3.4", wantErr: true},
		{in: "a_b.com", wantErr: true},
		{in: "-a.com", wantErr: true},
		{in: "a-.com", wantErr: true},
		{in: "a..com", wantErr: true},
		{in: "a b.com", wantErr: true},
		{in: "cdn*.x.com", wantErr: true},
		{in: "a.b.com/*-*/x", want: "a.b.com"},
		{in: strings.Repeat("a", 64) + ".com", wantErr: true},
		{in: strings.Repeat("a", 63) + ".com", want: strings.Repeat("a", 63) + ".com"},
		{in: strings.Repeat("abcdefghij.", 25) + "com", wantErr: true}, // 278 chars
		{in: "", wantErr: true},
		{in: "com.", wantErr: true},
		{in: "com.", allowTLD: true, want: "com"},
		{in: "a.123", wantErr: true},
		{in: "a.1x", want: "a.1x", wantErr: true},
		{in: "localhost", allowTLD: true, want: "localhost"},
		{in: "ex%41mple.com", wantErr: true},
	}
	for _, tc := range tests {
		got, err := Normalize(tc.in, tc.allowTLD)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Normalize(%q, %v) = %q, want error", tc.in, tc.allowTLD, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Normalize(%q, %v) unexpected error: %v", tc.in, tc.allowTLD, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Normalize(%q, %v) = %q, want %q", tc.in, tc.allowTLD, got, tc.want)
		}
	}
}

func TestCollapse(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"parent wins over chain", []string{"example.com", "sub1.example.com", "sub2.sub1.example.com"}, []string{"example.com"}},
		{"siblings kept", []string{"sub2.example.com", "sub2.sub1.example.com"}, []string{"sub2.example.com", "sub2.sub1.example.com"}},
		{"mid parent wins", []string{"sub1.example.com", "sub2.sub1.example.com"}, []string{"sub1.example.com"}},
		{"tld parent never collapses", []string{"google", "google.com", "www.google.com"}, []string{"google", "google.com"}},
		{"order preserved, child first", []string{"a.x.com", "b.y.com", "x.com"}, []string{"b.y.com", "x.com"}},
		{"empty", nil, nil},
	}
	for _, tc := range tests {
		got := Collapse(tc.in)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: Collapse(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestReverseLabels(t *testing.T) {
	if got := ReverseLabels("a.b.com"); got != "com.b.a" {
		t.Fatalf("got %q", got)
	}
	if got := ReverseLabels("com"); got != "com" {
		t.Fatalf("got %q", got)
	}
}

func TestSortReversed(t *testing.T) {
	in := []string{"a.org", "x.a.com", "b.com", "a.com"}
	SortReversed(in)
	want := []string{"a.com", "x.a.com", "b.com", "a.org"}
	if !slices.Equal(in, want) {
		t.Fatalf("got %v want %v", in, want)
	}
}

func TestMatcher(t *testing.T) {
	m, err := NewMatcher([]string{"example.com", "=exact.org", "/^ads?[0-9]*\\./"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Len() != 3 {
		t.Fatalf("Len = %d", m.Len())
	}
	tests := map[string]bool{
		"example.com":     true,
		"a.example.com":   true,
		"notexample.com":  false,
		"exact.org":       true,
		"www.exact.org":   false,
		"ad.foo.com":      true,
		"ads12.foo.com":   true,
		"adsense.foo.com": false,
	}
	for host, want := range tests {
		if got := m.Match(host); got != want {
			t.Errorf("Match(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestMatcherErrorsAndEmpty(t *testing.T) {
	if _, err := NewMatcher([]string{"/(/"}); err == nil {
		t.Fatal("expected regex error")
	}
	if _, err := NewMatcher([]string{"not a domain!"}); err == nil {
		t.Fatal("expected invalid pattern error")
	}
	m, err := NewMatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Match("anything.com") || m.Len() != 0 {
		t.Fatal("empty matcher must never match")
	}
}
