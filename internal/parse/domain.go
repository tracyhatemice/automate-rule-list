package parse

import (
	"net/netip"
	"strings"
)

// extractDomain handles plain "one host per line" lists with optional
// comments and trailing junk.
func extractDomain(line string) []string {
	s := StripComment(line)
	if s == "" {
		return nil
	}
	return []string{firstField(s)}
}

// extractHosts handles /etc/hosts style lines: "<ip> host [host…]".
func extractHosts(line string) []string {
	s := StripComment(line)
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return nil
	}
	if _, err := netip.ParseAddr(fields[0]); err != nil {
		return nil
	}
	var out []string
	for _, h := range fields[1:] {
		if !localNames[strings.ToLower(h)] {
			out = append(out, h)
		}
	}
	return out
}

// extractV2ray handles v2fly domain-list-community files: "full:host",
// "domain:host" or bare hosts, optionally followed by " @attribute".
func extractV2ray(line string) []string {
	s := StripComment(line)
	if s == "" {
		return nil
	}
	s = firstField(s)
	switch {
	case strings.HasPrefix(s, "regexp:"), strings.HasPrefix(s, "keyword:"), strings.HasPrefix(s, "include:"):
		return nil
	case strings.HasPrefix(s, "full:"):
		s = s[len("full:"):]
	case strings.HasPrefix(s, "domain:"):
		s = s[len("domain:"):]
	}
	return []string{s}
}

// extractDnsmasq handles "server=/a/b/…", "address=/a/…", "ipset=/a/…",
// "nftset=/a/…" and "local=/a/" directives; every path component except
// the last (the target) is a domain.
func extractDnsmasq(line string) []string {
	s := StripComment(line)
	i := strings.Index(s, "=/")
	if i < 0 {
		return nil
	}
	parts := strings.Split(s[i+1:], "/")
	if len(parts) < 3 { // "", domain…, target
		return nil
	}
	var out []string
	for _, d := range parts[1 : len(parts)-1] {
		d = strings.TrimLeft(d, ".")
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

var smartdnsDirectives = map[string]bool{
	"address": true, "nameserver": true, "domain-rules": true, "ipset": true, "nftset": true, "cname": true,
	"https-record": true, "srv-record": true, "ddns": true,
}

// extractSmartdns handles "<directive> /domain/…" lines and bare domains
// (domain-set files).
func extractSmartdns(line string) []string {
	s := StripComment(line)
	if s == "" {
		return nil
	}
	fields := strings.Fields(s)
	if len(fields) == 1 {
		if strings.ContainsAny(fields[0], "/:") {
			return nil
		}
		return []string{fields[0]}
	}
	if !smartdnsDirectives[fields[0]] || !strings.HasPrefix(fields[1], "/") {
		return nil
	}
	rest := fields[1][1:]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return nil
	}
	return []string{rest}
}
