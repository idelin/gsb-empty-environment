package systemd

import (
	"sort"
	"strings"
)

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedUnits(g *Graph) []*Unit {
	if g == nil {
		return nil
	}
	names := g.Names()
	units := make([]*Unit, 0, len(names))
	for _, name := range names {
		if u := g.Units[name]; u != nil {
			units = append(units, u)
		}
	}
	return units
}

func containsName(values []string, name string) bool {
	for _, value := range values {
		if value == name {
			return true
		}
	}
	return false
}
