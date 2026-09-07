package parse

import (
	"bytes"
	"iter"
	"strings"
)

// Lines yields every non-empty line of data with surrounding whitespace,
// carriage returns and a leading byte-order mark removed.
func Lines(data []byte) iter.Seq[string] {
	return func(yield func(string) bool) {
		data = bytes.TrimPrefix(data, []byte("\ufeff"))
		for len(data) > 0 {
			var line []byte
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				line, data = data[:i], data[i+1:]
			} else {
				line, data = data, nil
			}
			s := strings.TrimSpace(string(line))
			if s == "" {
				continue
			}
			if !yield(s) {
				return
			}
		}
	}
}

// StripComment removes a whole-line comment ("#", "!", ";", "//") and an
// inline comment introduced by whitespace followed by "#", then trims the
// result.
func StripComment(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return ""
	}
	switch {
	case s[0] == '#', s[0] == '!', s[0] == ';':
		return ""
	case strings.HasPrefix(s, "//"):
		return ""
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			s = s[:i]
			break
		}
	}
	return strings.TrimSpace(s)
}

// firstField returns the first whitespace-delimited token of s.
func firstField(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// localNames are hosts-file entries that never belong in a block list.
var localNames = map[string]bool{
	"localhost": true, "localhost.localdomain": true, "local": true, "broadcasthost": true,
	"ip6-localhost": true, "ip6-loopback": true, "ip6-localnet": true, "ip6-mcastprefix": true,
	"ip6-allnodes": true, "ip6-allrouters": true, "ip6-allhosts": true, "0.0.0.0": true,
}
