// Package ipset parses, normalizes, deduplicates and aggregates IPv4 and
// IPv6 addresses and CIDR blocks.
package ipset

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// ErrInvalid is wrapped by every parse error.
var ErrInvalid = errors.New("invalid ip or cidr")

// Parse accepts a bare address ("10.0.0.1", "2001:db8::1") or a CIDR block
// and returns the masked prefix. Bare addresses become host prefixes
// (/32, /128). IPv4-mapped IPv6 addresses are unmapped; zones are rejected.
func Parse(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Prefix{}, fmt.Errorf("%w: empty", ErrInvalid)
	}
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%w: %q: %w", ErrInvalid, s, err)
		}
		if p.Addr().Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%w: %q: zone not allowed", ErrInvalid, s)
		}
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return netip.Prefix{}, fmt.Errorf("%w: %q: mapped prefix shorter than /96", ErrInvalid, s)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%w: %q: %w", ErrInvalid, s, err)
	}
	if a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%w: %q: zone not allowed", ErrInvalid, s)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// Format renders host prefixes as bare addresses and everything else in
// CIDR notation.
func Format(p netip.Prefix) string {
	if p.Bits() == p.Addr().BitLen() {
		return p.Addr().String()
	}
	return p.String()
}

// compare orders prefixes by family (IPv4 first), address, then length.
func compare(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	return a.Bits() - b.Bits()
}

// Dedupe sorts the prefixes (IPv4 before IPv6, ascending) and removes
// duplicates and prefixes fully contained in another prefix.
func Dedupe(ps []netip.Prefix) []netip.Prefix {
	if len(ps) == 0 {
		return nil
	}
	sorted := slices.Clone(ps)
	slices.SortFunc(sorted, compare)
	out := make([]netip.Prefix, 0, len(sorted))
	for _, p := range sorted {
		if len(out) > 0 {
			last := out[len(out)-1]
			if last.Bits() <= p.Bits() && last.Contains(p.Addr()) {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// Aggregate deduplicates and then merges adjacent sibling prefixes
// ("10.0.0.0/25" + "10.0.0.128/25" → "10.0.0.0/24") until no more merges
// are possible.
func Aggregate(ps []netip.Prefix) []netip.Prefix {
	cur := Dedupe(ps)
	for {
		merged, changed := mergeSiblings(cur)
		if !changed {
			return merged
		}
		cur = Dedupe(merged)
	}
}

func mergeSiblings(ps []netip.Prefix) ([]netip.Prefix, bool) {
	out := make([]netip.Prefix, 0, len(ps))
	changed := false
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		if i+1 < len(ps) && p.Bits() > 0 {
			q := ps[i+1]
			if q.Bits() == p.Bits() && q.Addr().BitLen() == p.Addr().BitLen() {
				parent := netip.PrefixFrom(p.Addr(), p.Bits()-1).Masked()
				if parent.Addr() == p.Addr() && parent.Contains(q.Addr()) {
					out = append(out, parent)
					changed = true
					i++
					continue
				}
			}
		}
		out = append(out, p)
	}
	return out, changed
}
