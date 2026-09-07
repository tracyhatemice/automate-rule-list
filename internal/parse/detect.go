package parse

import (
	"net/netip"
	"strings"
)

// detectSample is how many non-comment lines Detect inspects.
const detectSample = 64

// Detect guesses the format name of data by inspecting its first lines.
// Explicit markers ("payload:", "[Adblock", "[AutoProxy") decide
// immediately; otherwise the syntax seen on most lines wins, and a plain
// "domain" list is the fallback.
func Detect(data []byte) string {
	counts := map[string]int{}
	seen := 0
	for line := range Lines(data) {
		switch {
		case strings.HasPrefix(line, "payload:"):
			return "clash"
		case strings.HasPrefix(line, "[Adblock"):
			return "adblock"
		case strings.HasPrefix(line, "[AutoProxy"):
			return "autoproxy"
		case line[0] == '#', line[0] == '!', line[0] == ';', strings.HasPrefix(line, "//"):
			continue
		}
		if name := classify(line); name != "" {
			counts[name]++
		}
		if seen++; seen >= detectSample {
			break
		}
	}
	best, bestN := "domain", 0
	for _, name := range []string{"clash", "adblock", "autoproxy", "dnsmasq", "smartdns", "hosts", "v2ray", "iplist"} {
		if n := counts[name]; n > bestN {
			best, bestN = name, n
		}
	}
	return best
}

func classify(line string) string {
	switch {
	case strings.HasPrefix(line, "||"), strings.HasPrefix(line, "|http"), strings.HasPrefix(line, "@@"):
		if strings.Contains(line, "^") || strings.Contains(line, "$") {
			return "adblock"
		}
		return "autoproxy"
	case strings.HasPrefix(line, "server=/"), strings.HasPrefix(line, "ipset=/"), strings.HasPrefix(line, "address=/"), strings.HasPrefix(line, "nftset=/"):
		return "dnsmasq"
	case strings.HasPrefix(line, "address /"), strings.HasPrefix(line, "nameserver /"), strings.HasPrefix(line, "domain-rules /"), strings.HasPrefix(line, "ipset /"), strings.HasPrefix(line, "nftset /"):
		return "smartdns"
	case strings.HasPrefix(line, "full:"), strings.HasPrefix(line, "regexp:"), strings.HasPrefix(line, "domain:"), strings.HasPrefix(line, "keyword:"), strings.HasPrefix(line, "include:"):
		return "v2ray"
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "-\t"):
		return "clash"
	case line[0] == '.':
		return "autoproxy"
	}
	fields := strings.Fields(StripComment(line))
	if len(fields) == 0 {
		return ""
	}
	if _, err := netip.ParseAddr(fields[0]); err == nil {
		if len(fields) >= 2 {
			return "hosts"
		}
		return "iplist"
	}
	if _, err := netip.ParsePrefix(fields[0]); err == nil {
		return "iplist"
	}
	return "domain"
}
