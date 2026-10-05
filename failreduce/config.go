package main

import (
	"fmt"
	"sort"
	"strings"
)

// normalize returns the canonical form of a configuration: deduplicated and
// sorted. All oracle cache keys are derived from this form.
func normalize(cfg []string) []string {
	seen := make(map[string]bool, len(cfg))
	out := make([]string, 0, len(cfg))
	for _, item := range cfg {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

// configKey is the normalized cache key for a configuration.
func configKey(cfg []string) string {
	return strings.Join(normalize(cfg), "\x1f")
}

// Deps describes required-presence constraints between configuration items:
// if item A requires B, any legal configuration containing A must also
// contain B.
type Deps struct {
	requires map[string][]string
}

func NewDeps() *Deps {
	return &Deps{requires: make(map[string][]string)}
}

func (d *Deps) Add(item string, requires ...string) {
	d.requires[item] = append(d.requires[item], requires...)
}

// CheckLegal verifies that every dependency of every item in cfg is itself
// present in cfg. It returns an error describing all violations.
func (d *Deps) CheckLegal(cfg []string) error {
	present := make(map[string]bool, len(cfg))
	for _, item := range cfg {
		present[item] = true
	}
	var missing []string
	for _, item := range normalize(cfg) {
		for _, req := range d.requires[item] {
			if !present[req] {
				missing = append(missing, fmt.Sprintf("%s requires %s", item, req))
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("illegal configuration, unsatisfied dependencies: %s", strings.Join(missing, "; "))
	}
	return nil
}

// dependents builds the reverse dependency graph over all known items plus
// the items present in cfg.
func (d *Deps) dependents(cfg []string) map[string][]string {
	rev := make(map[string][]string)
	addEdges := func(item string, reqs []string) {
		for _, req := range reqs {
			rev[req] = append(rev[req], item)
		}
	}
	for item, reqs := range d.requires {
		addEdges(item, reqs)
	}
	for _, item := range cfg {
		if _, ok := d.requires[item]; !ok {
			addEdges(item, nil) // ensure item exists in graph
		}
	}
	return rev
}

// RemovalClosure returns the legal configuration that results from removing
// the given items from cfg: everything transitively depending on a removed
// item is removed as well, so the result always satisfies the dependency
// constraints (assuming cfg did).
func (d *Deps) RemovalClosure(cfg []string, remove []string) []string {
	removed := make(map[string]bool)
	queue := make([]string, 0, len(remove))
	for _, item := range remove {
		if !removed[item] {
			removed[item] = true
			queue = append(queue, item)
		}
	}
	rev := d.dependents(cfg)
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		for _, dependent := range rev[item] {
			if !removed[dependent] {
				removed[dependent] = true
				queue = append(queue, dependent)
			}
		}
	}
	out := make([]string, 0, len(cfg))
	for _, item := range cfg {
		if !removed[item] {
			out = append(out, item)
		}
	}
	return out
}
