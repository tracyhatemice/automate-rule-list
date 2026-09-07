package parse

import "strings"

// extractAutoproxy handles AutoProxy (gfwlist) rules: "||host",
// "|http://host/", ".host" and bare "host". Rules that carry a path,
// a wildcard, an exception marker or a regex are skipped, matching the
// behaviour of the shell pipeline this program replaces.
func extractAutoproxy(line string) []string {
	s := line
	switch {
	case s == "":
		return nil
	case s[0] == '!', s[0] == '[', s[0] == '/':
		return nil
	case strings.HasPrefix(s, "@@"):
		return nil
	}
	switch {
	case strings.HasPrefix(s, "||"):
		s = s[2:]
	case s[0] == '|':
		s = s[1:]
		if i := strings.Index(s, "://"); i >= 0 {
			s = s[i+3:]
		} else {
			return nil
		}
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimLeft(s, ".")
	if s == "" || strings.ContainsAny(s, "/*%:^|@ ") {
		return nil
	}
	return []string{s}
}
