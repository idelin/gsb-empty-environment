package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// AddedCapability describes an install-time capability that can become newly
// reachable after a lock update, together with a witness platform
// configuration and a shortest dependency path.
type AddedCapability struct {
	// Capability is the install-time effect, for example "postinstall".
	Capability string
	// Node is the capability-bearing node at the end of Path.
	Node string
	// Path is a shortest root-to-Node path active under Config.
	Path []string
	// Config is a witness assignment under which the capability is active in
	// the new graph but was not active in the old graph.
	Config Config
}

// PathString renders Path as "root -> ... -> node".
func (a AddedCapability) PathString() string {
	return strings.Join(a.Path, " -> ")
}

// Report is the result of comparing two locked dependency graphs.
type Report struct {
	Added []AddedCapability
}

// Capabilities returns the names of all newly added capabilities, sorted
// lexicographically.
func (r Report) Capabilities() []string {
	names := make([]string, len(r.Added))
	for i, addition := range r.Added {
		names[i] = addition.Capability
	}
	sort.Strings(names)
	return names
}

// Find returns the addition for capability, if present.
func (r Report) Find(capability string) (AddedCapability, bool) {
	for _, addition := range r.Added {
		if addition.Capability == capability {
			return addition, true
		}
	}
	return AddedCapability{}, false
}

// Compare finds every install-time capability that is active in newGraph but
// not in oldGraph under at least one platform configuration. Platform flags
// from both graphs are enumerated exhaustively; both graphs are evaluated
// under the same configuration.
//
// For each capability, the reported path is a shortest path from the new
// graph's root to any active node providing that capability. Ties between
// equally short paths are broken lexicographically, then by the witness
// configuration's string form, so the result is deterministic.
func Compare(oldGraph, newGraph Graph) (Report, error) {
	oldCompiled, err := compileGraph(oldGraph)
	if err != nil {
		return Report{}, fmt.Errorf("old graph: %w", err)
	}
	newCompiled, err := compileGraph(newGraph)
	if err != nil {
		return Report{}, fmt.Errorf("new graph: %w", err)
	}

	variableSet := make(map[string]struct{}, len(oldCompiled.variables)+len(newCompiled.variables))
	for _, name := range oldCompiled.variables {
		variableSet[name] = struct{}{}
	}
	for _, name := range newCompiled.variables {
		variableSet[name] = struct{}{}
	}
	variables := sortedNames(variableSet)
	if len(variables) > MaxPlatforms {
		return Report{}, fmt.Errorf(
			"netsec: graphs use %d distinct platform conditions, maximum is %d",
			len(variables), MaxPlatforms,
		)
	}

	bitIndex := bitIndexFor(variables)
	configurationCount := 1 << uint(len(variables))
	candidates := make(map[string]AddedCapability)

	for mask := 0; mask < configurationCount; mask++ {
		oldReach := oldCompiled.reach(uint32(mask), bitIndex)
		oldCapabilities := make(map[string]struct{})
		for node, active := range oldReach.active {
			if !active {
				continue
			}
			for _, capability := range oldCompiled.caps[node] {
				oldCapabilities[capability] = struct{}{}
			}
		}

		newReach := newCompiled.reach(uint32(mask), bitIndex)
		for node, active := range newReach.active {
			if !active {
				continue
			}
			for _, capability := range newCompiled.caps[node] {
				if _, existed := oldCapabilities[capability]; existed {
					continue
				}
				candidate := AddedCapability{
					Capability: capability,
					Node:       newCompiled.ids[node],
					Path:       newCompiled.path(newReach, node),
					Config:     configFromMask(variables, uint32(mask)),
				}
				if current, found := candidates[capability]; !found || betterCandidate(candidate, current) {
					candidates[capability] = candidate
				}
			}
		}
	}

	report := Report{Added: make([]AddedCapability, 0, len(candidates))}
	for _, addition := range candidates {
		report.Added = append(report.Added, addition)
	}
	sort.Slice(report.Added, func(i, j int) bool {
		return report.Added[i].Capability < report.Added[j].Capability
	})
	return report, nil
}

// Diff is an alias for Compare.
func Diff(oldGraph, newGraph Graph) (Report, error) {
	return Compare(oldGraph, newGraph)
}

func configFromMask(variables []string, mask uint32) Config {
	cfg := make(Config, len(variables))
	for bit, name := range variables {
		cfg[name] = mask&(1<<uint(bit)) != 0
	}
	return cfg
}

func betterCandidate(a, b AddedCapability) bool {
	if len(a.Path) != len(b.Path) {
		return len(a.Path) < len(b.Path)
	}
	if comparison := comparePaths(a.Path, b.Path); comparison != 0 {
		return comparison < 0
	}
	return a.Config.String() < b.Config.String()
}

func comparePaths(a, b []string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
