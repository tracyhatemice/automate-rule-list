package clashrule

import (
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Rule
		wantErr bool
	}{
		{in: "DOMAIN-SUFFIX,example.com", want: Rule{Type: "DOMAIN-SUFFIX", Value: "example.com", Raw: "DOMAIN-SUFFIX,example.com"}},
		{in: "IP-CIDR,10.0.0.0/8,no-resolve", want: Rule{Type: "IP-CIDR", Value: "10.0.0.0/8", Options: []string{"no-resolve"}, Raw: "IP-CIDR,10.0.0.0/8,no-resolve"}},
		{in: "ip-cidr6,2001:db8::/32", want: Rule{Type: "IP-CIDR6", Value: "2001:db8::/32", Raw: "ip-cidr6,2001:db8::/32"}},
		{in: "  DOMAIN-KEYWORD,telegram \r", want: Rule{Type: "DOMAIN-KEYWORD", Value: "telegram", Raw: "DOMAIN-KEYWORD,telegram"}},
		{in: "+.example.com", want: Rule{Scalar: true, Value: "+.example.com", Raw: "+.example.com"}},
		{in: "'+.example.com'", want: Rule{Scalar: true, Value: "+.example.com", Raw: "+.example.com"}},
		{in: "example.com", want: Rule{Scalar: true, Value: "example.com", Raw: "example.com"}},
		{in: "*.example.com", want: Rule{Scalar: true, Value: "*.example.com", Raw: "*.example.com"}},
		{in: "10.0.0.0/8", want: Rule{Scalar: true, Value: "10.0.0.0/8", Raw: "10.0.0.0/8"}},
		{in: "MATCH", want: Rule{Type: "MATCH", Raw: "MATCH"}},
		{in: "FOO,bar", wantErr: true},
		{in: "DOMAIN,", wantErr: true},
		{in: "IP-CIDR,notip", wantErr: true},
		{in: "DOMAIN-SUFFIX,bad_host", wantErr: true},
		{in: "", wantErr: true},
		{in: "not a domain!", wantErr: true},
	}
	for _, tc := range tests {
		got, err := Parse(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) = %+v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got.Type != tc.want.Type || got.Value != tc.want.Value || got.Scalar != tc.want.Scalar || got.Raw != tc.want.Raw || !slices.Equal(got.Options, tc.want.Options) {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestCanonical(t *testing.T) {
	tests := map[string]string{
		"+.Example.com":                     "DOMAIN-SUFFIX,example.com",
		"example.com":                       "DOMAIN,example.com",
		"*.x.com":                           "DOMAIN-WILDCARD,*.x.com",
		".x.com":                            "DOMAIN-SUFFIX,x.com",
		"10.0.0.1/8":                        "IP-CIDR,10.0.0.0/8",
		"2001:db8::1":                       "IP-CIDR,2001:db8::1/128",
		"IP-CIDR6,2001:DB8::/32,no-resolve": "IP-CIDR,2001:db8::/32,no-resolve",
		"domain-suffix,goog":                "DOMAIN-SUFFIX,goog",
		"DOMAIN,WWW.Example.COM":            "DOMAIN,www.example.com",
		"DOMAIN,谷歌.com":                     "DOMAIN,xn--flw351e.com",
		"IP-CIDR,1.2.3.4":                   "IP-CIDR,1.2.3.4/32",
		"DOMAIN-KEYWORD,Telegram":           "DOMAIN-KEYWORD,Telegram",
	}
	for in, want := range tests {
		r, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		c, err := Canonical(r)
		if err != nil {
			t.Errorf("Canonical(%q): %v", in, err)
			continue
		}
		if got := c.String(); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsDomainIsIP(t *testing.T) {
	r, _ := Parse("DOMAIN-SUFFIX,a.com")
	if !r.IsDomain() || r.IsIP() {
		t.Fatal("DOMAIN-SUFFIX should be domain")
	}
	r, _ = Parse("IP-CIDR,10.0.0.0/8")
	if r.IsDomain() || !r.IsIP() {
		t.Fatal("IP-CIDR should be ip")
	}
	r, _ = Parse("DOMAIN-KEYWORD,x")
	if r.IsDomain() || r.IsIP() {
		t.Fatal("DOMAIN-KEYWORD is neither")
	}
}

func parseAll(t *testing.T, items ...string) []Rule {
	t.Helper()
	var out []Rule
	for _, it := range items {
		r, err := Parse(it)
		if err != nil {
			t.Fatalf("Parse(%q): %v", it, err)
		}
		c, err := Canonical(r)
		if err != nil {
			t.Fatalf("Canonical(%q): %v", it, err)
		}
		out = append(out, c)
	}
	return out
}

func strs(rules []Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.String()
	}
	return out
}

func TestCollapse(t *testing.T) {
	in := parseAll(t,
		"DOMAIN-SUFFIX,example.com",
		"DOMAIN-SUFFIX,sub.example.com",
		"DOMAIN,www.example.com",
		"DOMAIN,other.org",
		"DOMAIN-KEYWORD,example",
		"IP-CIDR,10.0.0.0/8,no-resolve",
		"IP-CIDR,10.1.0.0/16,no-resolve",
		"IP-CIDR,10.0.0.0/8",
		"DOMAIN-SUFFIX,example.com",
		"IP-CIDR6,2001:db8::/32,no-resolve",
		"IP-CIDR,2001:db8:1::/48,no-resolve",
		"IP-CIDR,2001:db8::/32",
	)
	want := []string{
		"DOMAIN-SUFFIX,example.com",
		"DOMAIN,other.org",
		"DOMAIN-KEYWORD,example",
		"IP-CIDR,10.0.0.0/8,no-resolve",
		"IP-CIDR,2001:db8::/32,no-resolve",
	}
	if got := strs(Collapse(in)); !slices.Equal(got, want) {
		t.Fatalf("Collapse =\n%v\nwant\n%v", got, want)
	}
}

func TestOrder(t *testing.T) {
	in := parseAll(t,
		"IP-CIDR,10.0.0.0/8,no-resolve",
		"DOMAIN-SUFFIX,b.com",
		"PROCESS-NAME,foo",
		"DOMAIN,a.com",
		"DOMAIN-KEYWORD,k",
		"DOMAIN-SUFFIX,a.com",
		"IP-CIDR6,2001:db8::/32",
		"GEOIP,CN",
	)
	want := []string{
		"DOMAIN-KEYWORD,k",
		"DOMAIN,a.com",
		"DOMAIN-SUFFIX,b.com",
		"DOMAIN-SUFFIX,a.com",
		"IP-CIDR,10.0.0.0/8,no-resolve",
		"IP-CIDR,2001:db8::/32",
		"GEOIP,CN",
		"PROCESS-NAME,foo",
	}
	if got := strs(Order(in)); !slices.Equal(got, want) {
		t.Fatalf("Order =\n%v\nwant\n%v", got, want)
	}
}

func TestQuoteYAML(t *testing.T) {
	tests := map[string]string{
		"DOMAIN,a.com":                  "DOMAIN,a.com",
		"IP-CIDR,10.0.0.0/8,no-resolve": "IP-CIDR,10.0.0.0/8,no-resolve",
		"+.a.com":                       "'+.a.com'",
		"a.com":                         "'a.com'",
		"'a.com'":                       "'a.com'",
		"*.a.com":                       "'*.a.com'",
		"10.0.0.0/8":                    "'10.0.0.0/8'",
	}
	for in, want := range tests {
		if got := QuoteYAML(in); got != want {
			t.Errorf("QuoteYAML(%q) = %q, want %q", in, got, want)
		}
	}
}
