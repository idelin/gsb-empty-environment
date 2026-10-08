package updater

import "sort"

// Requirement constrains the acceptable versions of a dependency package:
// Min <= v (inclusive) and, when Max is set, v <= Max (inclusive).
type Requirement struct {
	Pkg string   `json:"pkg"`
	Min Version  `json:"min"`
	Max *Version `json:"max,omitempty"`
}

func (r Requirement) Allows(v Version) bool {
	if v.Compare(r.Min) < 0 {
		return false
	}
	if r.Max != nil && v.Compare(*r.Max) > 0 {
		return false
	}
	return true
}

// Problem describes one update run.
//
//   - Locked: packages pinned by the old lock file. They are the roots of the
//     dependency closure and must remain installed (possibly at a new version).
//   - Candidates: finite set of selectable versions per package.
//   - Banned: versions blocked by the security denylist.
//   - Deps: requirements imposed when pkg@version is selected.
type Problem struct {
	Locked     map[string]Version                   `json:"locked"`
	Candidates map[string][]Version                 `json:"candidates"`
	Banned     map[string][]Version                 `json:"banned,omitempty"`
	Deps       map[string]map[Version][]Requirement `json:"deps,omitempty"`
}

// universe returns every package that can ever be referenced: locked
// packages, packages with candidates, and dependency targets.
func (p *Problem) universe() []string {
	seen := map[string]bool{}
	for name := range p.Locked {
		seen[name] = true
	}
	for name := range p.Candidates {
		seen[name] = true
	}
	for _, byVersion := range p.Deps {
		for _, reqs := range byVersion {
			for _, r := range reqs {
				seen[r.Pkg] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p *Problem) isBanned(pkg string, v Version) bool {
	for _, b := range p.Banned[pkg] {
		if b == v {
			return true
		}
	}
	return false
}

func (p *Problem) requirements(pkg string, v Version) []Requirement {
	return p.Deps[pkg][v]
}

// lockedNames returns locked package names in sorted order. The objective
// version vector is defined over this fixed sequence.
func (p *Problem) lockedNames() []string {
	names := make([]string, 0, len(p.Locked))
	for name := range p.Locked {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
