package gdict

import (
	"fmt"
	"math"
	"sort"
)

type groupInfo struct {
	id        int
	members   []Key
	canonical Key
	ranks     []int64 // one per normalized version
	active    bool
	fixed     *uint64
}

type entryInfo struct {
	oldCode uint64
	group   int
	key     Key
}

type partitionInfo struct {
	name    string
	version string
	entries []entryInfo
	cuts    []Cut
}

type normalized struct {
	groups      []groupInfo
	activeIDs   []int // group ids, sorted by canonical key
	activePos   map[int]int
	parts       []partitionInfo
	versions    []string
	preorders   []Preorder
	holes       []Interval
	fixed       []Fixed
	maxStates   int
	globalLimit uint64
}

func normalize(cfg Config) (*normalized, error) {
	holes, fixed, err := cfg.Space.validate()
	if err != nil {
		return nil, err
	}
	if len(cfg.Preorders) == 0 {
		return nil, fmt.Errorf("gdict: at least one collation preorder is required")
	}

	// Version names and rank coverage.
	versions := make([]string, 0, len(cfg.Preorders))
	preorders := make([]Preorder, 0, len(cfg.Preorders))
	seenVersion := make(map[string]bool)
	for _, p := range cfg.Preorders {
		if p.Version == "" {
			return nil, fmt.Errorf("gdict: preorder with empty version name")
		}
		if seenVersion[p.Version] {
			return nil, fmt.Errorf("gdict: duplicate preorder version %q", p.Version)
		}
		seenVersion[p.Version] = true
		versions = append(versions, p.Version)
		preorders = append(preorders, p)
	}
	versionIndex := make(map[string]int, len(versions))
	for i, v := range versions {
		versionIndex[v] = i
	}

	// Collect every key mentioned anywhere. This is the finite universe over
	// which every preorder must be total.
	universe := make(map[Key]bool)
	addKey := func(k Key) {
		universe[k] = true
	}
	for _, p := range cfg.Partitions {
		for _, e := range p.Entries {
			addKey(e.Key)
		}
	}
	for _, a := range cfg.Aliases {
		for _, k := range a.Members {
			addKey(k)
		}
	}
	for _, f := range fixed {
		addKey(f.Key)
	}
	for _, l := range cfg.Lookups {
		addKey(l.Key)
	}

	// Union-find for explicit aliases. Distinct spellings remain distinct
	// groups; universal collation equivalence is handled later as an optional
	// code-sharing relation, not as key identity.
	keys := make([]Key, 0, len(universe))
	for k := range universe {
		keys = append(keys, k)
	}
	sortKeys(keys)
	keyIndex := make(map[Key]int, len(keys))
	for i, k := range keys {
		keyIndex[k] = i
	}
	uf := newUnionFind(len(keys))
	for _, a := range cfg.Aliases {
		if len(a.Members) == 0 {
			return nil, fmt.Errorf("gdict: alias group %q has no members", a.Name)
		}
		first := keyIndex[a.Members[0]]
		for _, k := range a.Members[1:] {
			uf.union(first, keyIndex[k])
		}
	}
	aliasMembers := make(map[int][]Key)
	for _, k := range keys {
		r := uf.find(keyIndex[k])
		aliasMembers[r] = append(aliasMembers[r], k)
	}
	// Sort members deterministically.
	for r := range aliasMembers {
		sortKeys(aliasMembers[r])
	}

	// Build alias groups in deterministic order of their canonical key.
	roots := make([]int, 0, len(aliasMembers))
	for r := range aliasMembers {
		roots = append(roots, r)
	}
	sort.Slice(roots, func(i, j int) bool {
		return aliasMembers[roots[i]][0].Less(aliasMembers[roots[j]][0])
	})
	rootToGroup := make(map[int]int, len(roots))
	groups := make([]groupInfo, 0, len(roots))
	for gid, r := range roots {
		rootToGroup[r] = gid
		members := aliasMembers[r]
		groups = append(groups, groupInfo{id: gid, members: members, canonical: members[0]})
	}
	keyToGroup := make(map[Key]int, len(keys))
	for _, k := range keys {
		keyToGroup[k] = rootToGroup[uf.find(keyIndex[k])]
	}

	// Rank coverage and alias consistency.
	for _, p := range preorders {
		for _, k := range keys {
			if _, ok := p.Ranks[k]; !ok {
				return nil, fmt.Errorf("gdict: preorder %q does not rank key %s", p.Version, k)
			}
		}
	}
	for _, g := range groups {
		g.ranks = make([]int64, len(versions))
		r0 := preorders[0].Ranks[g.members[0]]
		for _, k := range g.members[1:] {
			if preorders[0].Ranks[k] != r0 {
				return nil, aliasOrderConflict(g, versions[0], preorders[0], k)
			}
		}
		for vi, p := range preorders {
			g.ranks[vi] = p.Ranks[g.members[0]]
			for _, k := range g.members[1:] {
				if p.Ranks[k] != g.ranks[vi] {
					return nil, aliasOrderConflict(g, p.Version, p, k)
				}
			}
		}
		groups[g.id] = g
	}

	// Partitions and entries.
	parts := make([]partitionInfo, 0, len(cfg.Partitions))
	seenPart := make(map[string]bool)
	for _, p := range cfg.Partitions {
		if p.Name == "" {
			return nil, fmt.Errorf("gdict: partition with empty name")
		}
		if seenPart[p.Name] {
			return nil, fmt.Errorf("gdict: duplicate partition %q", p.Name)
		}
		seenPart[p.Name] = true
		if _, ok := versionIndex[p.Version]; !ok {
			return nil, fmt.Errorf("gdict: partition %q uses unknown collation version %q", p.Name, p.Version)
		}
		entries := make([]entryInfo, 0, len(p.Entries))
		seenCode := make(map[uint64]bool)
		for _, e := range p.Entries {
			if e.OldCode > uint64(math.MaxInt64) {
				return nil, fmt.Errorf("gdict: partition %q old code %d is too large for affine translation", p.Name, e.OldCode)
			}
			if seenCode[e.OldCode] {
				return nil, fmt.Errorf("gdict: partition %q has duplicate old code %d", p.Name, e.OldCode)
			}
			seenCode[e.OldCode] = true
			gid := keyToGroup[e.Key]
			entries = append(entries, entryInfo{oldCode: e.OldCode, group: gid, key: e.Key})
			groups[gid].active = true
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].oldCode < entries[j].oldCode })
		cuts := append([]Cut(nil), p.Cuts...)
		if err := validateCuts(p.Name, cuts); err != nil {
			return nil, err
		}
		sort.SliceStable(cuts, func(i, j int) bool {
			if cuts[i].OldCode != cuts[j].OldCode {
				return cuts[i].OldCode < cuts[j].OldCode
			}
			if cuts[i].Bound != cuts[j].Bound {
				return cuts[i].Bound < cuts[j].Bound
			}
			return cuts[i].Name < cuts[j].Name
		})
		parts = append(parts, partitionInfo{name: p.Name, version: p.Version, entries: entries, cuts: cuts})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].name < parts[j].name })

	// Fixed codes.
	fixedByGroup := make(map[int]uint64)
	for _, f := range fixed {
		gid := keyToGroup[f.Key]
		if old, ok := fixedByGroup[gid]; ok && old != f.Code {
			return nil, &IncompatibleError{Cert: Certificate{
				Kind:        "fixed-code-split",
				Message:     fmt.Sprintf("logical key %s has two fixed codes %d and %d", groups[gid].canonical, old, f.Code),
				Fixed:       dedupFixed([]Fixed{{Code: old, Key: groups[gid].canonical}, {Code: f.Code, Key: groups[gid].canonical}}),
				Requirement: "one logical key must keep one immovable code",
			}}
		}
		fixedByGroup[gid] = f.Code
		groups[gid].active = true
	}
	for gid, code := range fixedByGroup {
		c := code
		groups[gid].fixed = &c
	}

	// Active groups in canonical order.
	active := make([]int, 0)
	for _, g := range groups {
		if g.active {
			active = append(active, g.id)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return groups[active[i]].canonical.Less(groups[active[j]].canonical)
	})
	activePos := make(map[int]int, len(active))
	for i, gid := range active {
		activePos[gid] = i
	}

	maxStates := cfg.MaxStates
	if maxStates <= 0 {
		maxStates = DefaultMaxStates
	}
	return &normalized{
		groups:      groups,
		activeIDs:   active,
		activePos:   activePos,
		parts:       parts,
		versions:    versions,
		preorders:   preorders,
		holes:       holes,
		fixed:       fixed,
		maxStates:   maxStates,
		globalLimit: cfg.Space.Limit,
	}, nil
}

func validateCuts(part string, cuts []Cut) error {
	seen := make(map[string]bool)
	for _, c := range cuts {
		if c.OldCode > uint64(math.MaxInt64) {
			return fmt.Errorf("gdict: partition %q cut %q old code %d is too large for affine translation", part, c.Name, c.OldCode)
		}
		if c.Name != "" && seen[c.Name] {
			return fmt.Errorf("gdict: partition %q has duplicate cut name %q", part, c.Name)
		}
		seen[c.Name] = true
		if c.Bound != LT && c.Bound != LE {
			return fmt.Errorf("gdict: partition %q cut %q has invalid bound", part, c.Name)
		}
	}
	return nil
}

func aliasOrderConflict(g groupInfo, version string, p Preorder, other Key) error {
	r1, r2 := p.Ranks[g.canonical], p.Ranks[other]
	return &IncompatibleError{Cert: Certificate{
		Kind:    "alias-collation-conflict",
		Message: fmt.Sprintf("alias members %s and %s are strictly ordered by collation %s", g.canonical, other, version),
		Observations: []Observation{
			{Kind: "alias", LeftKey: g.canonical, RightKey: other, Detail: "alias declaration requires one logical value"},
			{Kind: "collation-order", Version: version, LeftKey: g.canonical, RightKey: other, Detail: fmt.Sprintf("ranks are %d and %d", r1, r2)},
		},
		Requirement: "alias members must be equivalent in every supplied collation",
	}}
}

// unionFind is a small deterministic union-find.
type unionFind struct{ parent []int }

func newUnionFind(n int) *unionFind {
	u := &unionFind{parent: make([]int, n)}
	for i := range u.parent {
		u.parent[i] = i
	}
	return u
}
func (u *unionFind) find(x int) int {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}
func (u *unionFind) union(a, b int) {
	a, b = u.find(a), u.find(b)
	if a == b {
		return
	}
	if a < b {
		u.parent[b] = a
	} else {
		u.parent[a] = b
	}
}
