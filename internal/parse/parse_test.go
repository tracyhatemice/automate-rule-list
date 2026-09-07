package parse

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func values(t *testing.T, items []Item, kind Kind) []string {
	t.Helper()
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.Kind != kind {
			t.Errorf("item %q has kind %v, want %v", it.Value, it.Kind, kind)
		}
		out = append(out, it.Value)
	}
	return out
}

func TestParsers(t *testing.T) {
	tests := []struct {
		format  string
		fixture string
		kind    Kind
		want    []string
	}{
		{"adblock", "adguard.txt", KindDomain, []string{"dtipvw.com", "b1ll3r11.xyz", "tracker.example.com", "plain-domain.com", "hosts-style.com", "nocaret.example.com"}},
		{"domain", "justdomains.txt", KindDomain, []string{"-access-logs.net.daraz.com", "0-01-car21.net.daraz.com", "Tracker.Example.COM", "indented.example.com", "two.example.com"}},
		{"hosts", "hosts.txt", KindDomain, []string{"ads.example.com", "tracker.example.com", "a.example.com", "b.example.com"}},
		{"autoproxy", "autoproxy.txt", KindDomain, []string{"fxnetworks.com", "85.17.73.31", "secure.example.com", "blogspot.com", "www.aolnews.com"}},
		{"dnsmasq", "dnsmasq.conf", KindDomain, []string{"0rz.tw", "0rz.tw", "0.xn--czrs0t", "a.example.com", "b.example.com", "ads.example.com", "lan", "nft.example.com"}},
		{"smartdns", "smartdns.conf", KindDomain, []string{"ads.example.com", "Block.Example.com", "cn.example.com", "rules.example.com", "set.example.com", "nft.example.com", "bare.example.com"}},
		{"v2ray", "v2ray.txt", KindDomain, []string{"000webhost.com", "exact.example.com", "suffix.example.com", "attr.example.com", "attr2.example.com"}},
		{"iplist", "iplist.txt", KindIP, []string{"1.0.164.165", "103.81.182.0/24", "111.201.200.0/24", "2001:a18:a:c5::e", "2a0a:c00::/29", "8.8.8.8"}},
		{"clash", "clash-classical.yaml", KindRule, []string{"DOMAIN-KEYWORD,apiproxy-device-prod-nlb-", "DOMAIN,netflix.com.edgesuite.net", "DOMAIN-SUFFIX,fast.com", "IP-CIDR,23.246.0.0/18,no-resolve", "IP-CIDR6,2607:fb10::/32,no-resolve"}},
		{"clash", "clash-domain.txt", KindRule, []string{"instant.arubanetworks.com", "+.internal", "+.localdomain"}},
	}
	for _, tc := range tests {
		f, ok := Lookup(tc.format)
		if !ok {
			t.Errorf("format %q not registered", tc.format)
			continue
		}
		items, err := f.Parse(fixture(t, tc.fixture))
		if err != nil {
			t.Errorf("%s(%s): %v", tc.format, tc.fixture, err)
			continue
		}
		if got := values(t, items, tc.kind); !slices.Equal(got, tc.want) {
			t.Errorf("%s(%s) =\n%q\nwant\n%q", tc.format, tc.fixture, got, tc.want)
		}
	}
}

func TestClashParserErrors(t *testing.T) {
	f, _ := Lookup("clash")
	if _, err := f.Parse([]byte("rules:\n  - a\n")); err == nil {
		t.Error("expected error for missing payload")
	}
	if _, err := f.Parse([]byte("payload: [\n")); err == nil {
		t.Error("expected error for invalid yaml")
	}
	items, err := f.Parse([]byte("payload: ['DOMAIN-SUFFIX,a.com', '+.b.com']\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := values(t, items, KindRule); !slices.Equal(got, []string{"DOMAIN-SUFFIX,a.com", "+.b.com"}) {
		t.Errorf("flow style = %q", got)
	}
	items, err = f.Parse([]byte("payload: []\n"))
	if err != nil || len(items) != 0 {
		t.Errorf("empty payload: items=%v err=%v", items, err)
	}
}

func TestNames(t *testing.T) {
	want := []string{"adblock", "autoproxy", "clash", "dnsmasq", "domain", "hosts", "iplist", "smartdns", "v2ray"}
	if got := Names(); !slices.Equal(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("Lookup(nope) should fail")
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		fixture string
		want    string
	}{
		{"adguard.txt", "adblock"},
		{"justdomains.txt", "domain"},
		{"hosts.txt", "hosts"},
		{"autoproxy.txt", "autoproxy"},
		{"dnsmasq.conf", "dnsmasq"},
		{"smartdns.conf", "smartdns"},
		{"v2ray.txt", "v2ray"},
		{"iplist.txt", "iplist"},
		{"clash-classical.yaml", "clash"},
		{"clash-domain.txt", "clash"},
	}
	for _, tc := range tests {
		if got := Detect(fixture(t, tc.fixture)); got != tc.want {
			t.Errorf("Detect(%s) = %q, want %q", tc.fixture, got, tc.want)
		}
	}
	inline := map[string]string{
		"a.com\nb.com\n":                     "domain",
		"# only a comment\n":                 "domain",
		"":                                   "domain",
		"1.2.3.4\n5.6.7.0/24\n":              "iplist",
		"\ufeff# bom\npayload:\n  - a.com\n": "clash",
		"||a.com\n||b.com\n":                 "autoproxy",
		"||a.com^\n||b.com^\n":               "adblock",
		"||a.com\n|http://b.com/\n.c.com\n":  "autoproxy",
	}
	for in, want := range inline {
		if got := Detect([]byte(in)); got != want {
			t.Errorf("Detect(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinesAndStripComment(t *testing.T) {
	var got []string
	for l := range Lines([]byte("\ufeffa\r\n\n  b  \n\tc\r\n")) {
		got = append(got, l)
	}
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("Lines = %q", got)
	}
	tests := map[string]string{
		"a.com":         "a.com",
		"a.com # x":     "a.com",
		"a.com\t#x":     "a.com",
		"# a.com":       "",
		"! a.com":       "",
		"; a.com":       "",
		"// a.com":      "",
		"a.com//x":      "a.com//x",
		"1.1.1.1 # AS1": "1.1.1.1",
		"":              "",
	}
	for in, want := range tests {
		if got := StripComment(in); got != want {
			t.Errorf("StripComment(%q) = %q, want %q", in, got, want)
		}
	}
}
