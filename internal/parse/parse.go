// Package parse turns the text of an upstream list into items. Each
// Format knows one file syntax (hosts files, AdGuard filters, dnsmasq
// configuration, clash YAML, …) and extracts raw host names, addresses or
// clash rules; validation and normalization happen later in the
// processors. Formats register themselves by name so new syntaxes can be
// added without touching the pipeline.
package parse

import (
	"slices"
	"sync"
)

// Kind classifies what a parsed item is.
type Kind int

// Item kinds.
const (
	KindDomain Kind = iota + 1 // a host name, not yet normalized
	KindIP                     // an address or CIDR block, not yet validated
	KindRule                   // a clash rule-provider payload item
)

func (k Kind) String() string {
	switch k {
	case KindDomain:
		return "domain"
	case KindIP:
		return "ip"
	case KindRule:
		return "rule"
	}
	return "unknown"
}

// Item is one entry extracted from a source.
type Item struct {
	Kind  Kind
	Value string
}

// Format parses one list syntax.
type Format interface {
	Name() string
	Parse(data []byte) ([]Item, error)
}

var (
	mu       sync.RWMutex
	registry = map[string]Format{}
)

// Register adds a format to the registry, replacing any format with the
// same name.
func Register(f Format) {
	mu.Lock()
	defer mu.Unlock()
	registry[f.Name()] = f
}

// Lookup returns the format registered under name.
func Lookup(name string) (Format, bool) {
	mu.RLock()
	defer mu.RUnlock()
	f, ok := registry[name]
	return f, ok
}

// Names returns the registered format names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// lineFormat adapts a per-line extractor to the Format interface.
type lineFormat struct {
	name string
	kind Kind
	// extract returns the values found on one non-empty, trimmed line.
	extract func(line string) []string
}

func (f lineFormat) Name() string { return f.name }

func (f lineFormat) Parse(data []byte) ([]Item, error) {
	var items []Item
	for line := range Lines(data) {
		for _, v := range f.extract(line) {
			if v != "" {
				items = append(items, Item{Kind: f.kind, Value: v})
			}
		}
	}
	return items, nil
}

func init() {
	Register(lineFormat{name: "domain", kind: KindDomain, extract: extractDomain})
	Register(lineFormat{name: "hosts", kind: KindDomain, extract: extractHosts})
	Register(lineFormat{name: "adblock", kind: KindDomain, extract: extractAdblock})
	Register(lineFormat{name: "autoproxy", kind: KindDomain, extract: extractAutoproxy})
	Register(lineFormat{name: "dnsmasq", kind: KindDomain, extract: extractDnsmasq})
	Register(lineFormat{name: "smartdns", kind: KindDomain, extract: extractSmartdns})
	Register(lineFormat{name: "v2ray", kind: KindDomain, extract: extractV2ray})
	Register(lineFormat{name: "iplist", kind: KindIP, extract: extractIPList})
	Register(clashFormat{})
}
