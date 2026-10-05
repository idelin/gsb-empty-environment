package main

import (
	"fmt"
	"sort"
	"strings"
)

// Change describes one forced difference between the lock and the resolution.
type Change struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"` // "changed", "added", "removed"
	From    string   `json:"from,omitempty"`
	To      string   `json:"to,omitempty"`
	Reasons []string `json:"reasons"`
	Path    []string `json:"path,omitempty"` // root-to-package chain in the final graph
}

// Plan is the full result of replanning the lock file.
type Plan struct {
	Resolved map[string]LockEntry `json:"resolved"`
	Changes  []Change             `json:"changes"`
	Changed  int                  `json:"changedPins"`
	Download int64                `json:"downloadBytes"`
}

func pin(c *Candidate) string {
	return fmt.Sprintf("%s@%s (%s)", c.Name, c.Version, c.Source)
}

// shortestPaths computes, for every resolved package, a shortest dependency
// path from a root in the final resolution graph.
func shortestPaths(resolved map[string]*Candidate, roots map[string]Constraint) map[string][]string {
	prev := map[string]string{}
	var queue []string
	names := make([]string, 0, len(roots))
	for n := range roots {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if resolved[n] != nil {
			prev[n] = ""
			queue = append(queue, n)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range resolved[cur].deps {
			if resolved[d.name] != nil {
				if _, seen := prev[d.name]; !seen {
					prev[d.name] = cur
					queue = append(queue, d.name)
				}
			}
		}
	}
	paths := map[string][]string{}
	for n := range resolved {
		var rev []string
		for at := n; ; {
			rev = append(rev, at)
			p, ok := prev[at]
			if !ok || p == "" {
				break
			}
			at = p
		}
		path := make([]string, 0, len(rev))
		for i := len(rev) - 1; i >= 0; i-- {
			at := rev[i]
			label := pin(resolved[at])
			if i == len(rev)-1 && prev[at] == "" {
				if _, isRoot := roots[at]; isRoot {
					label += " [root]"
				}
			}
			path = append(path, label)
		}
		paths[n] = path
	}
	return paths
}

// Explain builds the plan diff: every added/changed/removed package with the
// reason chain that forced it.
func Explain(reg *Registry, lock map[string]LockEntry, rootReqs map[string]string, resolved map[string]*Candidate, obj objective) *Plan {
	roots := map[string]Constraint{}
	for n, r := range rootReqs {
		if c, err := ParseConstraint(r); err == nil {
			roots[n] = c
		}
	}
	paths := shortestPaths(resolved, roots)
	plan := &Plan{Resolved: map[string]LockEntry{}, Changed: obj.changes, Download: obj.bytes}
	for n, c := range resolved {
		plan.Resolved[n] = LockEntry{Version: c.Version, Source: c.Source}
	}

	names := make([]string, 0, len(resolved))
	for n := range resolved {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := resolved[n]
		locked, wasLocked := lock[n]
		switch {
		case !wasLocked:
			plan.Changes = append(plan.Changes, Change{
				Name:    n,
				Kind:    "added",
				To:      pin(c),
				Reasons: addReasons(n, resolved, roots),
				Path:    paths[n],
			})
		case locked.Version != c.Version || locked.Source != c.Source:
			plan.Changes = append(plan.Changes, Change{
				Name:    n,
				Kind:    "changed",
				From:    fmt.Sprintf("%s@%s (%s)", n, locked.Version, locked.Source),
				To:      pin(c),
				Reasons: changeReasons(reg, n, locked, resolved, roots),
				Path:    paths[n],
			})
		}
	}

	removed := make([]string, 0)
	for n := range lock {
		if resolved[n] == nil {
			removed = append(removed, n)
		}
	}
	sort.Strings(removed)
	for _, n := range removed {
		plan.Changes = append(plan.Changes, Change{
			Name:    n,
			Kind:    "removed",
			From:    fmt.Sprintf("%s@%s (%s)", n, lock[n].Version, lock[n].Source),
			Reasons: []string{"no longer reachable from any root dependency"},
		})
	}
	return plan
}

// addReasons explains why a new package entered the resolution.
func addReasons(name string, resolved map[string]*Candidate, roots map[string]Constraint) []string {
	if con, ok := roots[name]; ok {
		return []string{fmt.Sprintf("new root requirement %s %q", name, con.Raw)}
	}
	var reasons []string
	for _, src := range sortedNames(resolved) {
		for _, d := range resolved[src].deps {
			if d.name == name {
				reasons = append(reasons, fmt.Sprintf("required by %s with constraint %q", pin(resolved[src]), d.con.Raw))
			}
		}
	}
	return reasons
}

// changeReasons explains why the locked pin of name could not be kept.
func changeReasons(reg *Registry, name string, locked LockEntry, resolved map[string]*Candidate, roots map[string]Constraint) []string {
	lv, err := ParseVersion(locked.Version)
	if err != nil {
		return []string{"locked version is unparseable"}
	}
	var reasons []string
	// 1) Constraints imposed on the package that the locked version violates.
	if con, ok := roots[name]; ok && !con.Allows(lv) {
		reasons = append(reasons, fmt.Sprintf("locked %s violates root constraint %q", locked.Version, con.Raw))
	}
	for _, src := range sortedNames(resolved) {
		for _, d := range resolved[src].deps {
			if d.name == name && !d.con.Allows(lv) {
				reasons = append(reasons, fmt.Sprintf("locked %s violates constraint %q from %s", locked.Version, d.con.Raw, pin(resolved[src])))
			}
		}
	}
	if len(reasons) > 0 {
		return reasons
	}
	// 2) The locked version itself fits, but its own dependencies conflict
	// with the rest of the resolution (cascading change).
	lc := findCandidate(reg, name, locked)
	if lc != nil {
		for _, d := range lc.deps {
			if other := resolved[d.name]; other != nil && !d.con.Allows(other.parsed) {
				reasons = append(reasons, fmt.Sprintf("locked %s requires %s %q, but %s resolved to %s", locked.Version, d.name, d.con.Raw, d.name, pin(other)))
			}
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "locked pin is no longer available in the registry")
	}
	return reasons
}

func findCandidate(reg *Registry, name string, locked LockEntry) *Candidate {
	for _, c := range reg.byName[name] {
		if c.Version == locked.Version && c.Source == locked.Source {
			return c
		}
	}
	return nil
}

func sortedNames(m map[string]*Candidate) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Render formats the plan for humans.
func (p *Plan) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "changed pins: %d, download: %d bytes\n", p.Changed, p.Download)
	for _, ch := range p.Changes {
		switch ch.Kind {
		case "changed":
			fmt.Fprintf(&b, "~ %s: %s -> %s\n", ch.Name, ch.From, ch.To)
		case "added":
			fmt.Fprintf(&b, "+ %s: %s\n", ch.Name, ch.To)
		case "removed":
			fmt.Fprintf(&b, "- %s: %s\n", ch.Name, ch.From)
		}
		for _, r := range ch.Reasons {
			fmt.Fprintf(&b, "    reason: %s\n", r)
		}
		if len(ch.Path) > 0 {
			fmt.Fprintf(&b, "    path:   %s\n", strings.Join(ch.Path, " -> "))
		}
	}
	return b.String()
}
