package parse

// extractIPList handles "one address or CIDR block per line" lists with
// comments and trailing annotations.
func extractIPList(line string) []string {
	s := StripComment(line)
	if s == "" {
		return nil
	}
	return []string{firstField(s)}
}
