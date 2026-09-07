package parse

import (
	"errors"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// clashFormat parses a clash rule-provider document: a YAML mapping with
// a "payload" list of strings.
type clashFormat struct{}

func (clashFormat) Name() string { return "clash" }

func (clashFormat) Parse(data []byte) ([]Item, error) {
	var doc struct {
		Payload *[]string `yaml:"payload"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("clash yaml: %w", err)
	}
	if doc.Payload == nil {
		return nil, errors.New("clash yaml: no payload list")
	}
	items := make([]Item, 0, len(*doc.Payload))
	for _, v := range *doc.Payload {
		v = strings.TrimSpace(v)
		if v != "" {
			items = append(items, Item{Kind: KindRule, Value: v})
		}
	}
	return items, nil
}
