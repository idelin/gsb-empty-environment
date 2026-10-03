package noderouter

import (
	"fmt"
	"strings"
)

// Requirement is a single key=value predicate over node attributes.
// Reserved keys "zone", "role" and "healthy" match metadata fields;
// any other key matches user-defined labels.
type Requirement struct {
	Key   string
	Value string
}

// Selector is a conjunction of requirements (logical AND).
type Selector []Requirement

// ParseSelector parses a selector expression such as
// "zone=us-east,role=worker" into a Selector. Empty input matches everything.
func ParseSelector(expr string) (Selector, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, nil
	}
	var sel Selector
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 || kv[0] == "" || kv[1] == "" {
			return nil, fmt.Errorf("noderouter: invalid selector term %q (want key=value)", part)
		}
		sel = append(sel, Requirement{Key: strings.TrimSpace(kv[0]), Value: strings.TrimSpace(kv[1])})
	}
	return sel, nil
}

func (s Selector) String() string {
	parts := make([]string, len(s))
	for i, r := range s {
		parts[i] = r.Key + "=" + r.Value
	}
	return strings.Join(parts, ",")
}
