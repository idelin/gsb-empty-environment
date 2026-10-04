package main

import (
	"sort"
	"strings"
)

// Entry is one path from the archive manifest. Dir marks directories.
type Entry struct {
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
}

// Group is a minimal conflict group: a set of original entries that collide
// on (or under) one normalized path, for one reason.
type Group struct {
	Norm    string  `json:"norm"`   // normalized path where the conflict sits
	Reason  string  `json:"reason"` // name | type | reserved | invalid
	Origins []Entry `json:"origins"`
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func depth(p string) int { return len(splitPath(p)) }

func dedupEntries(ents []Entry) []Entry {
	seen := map[Entry]bool{}
	out := make([]Entry, 0, len(ents))
	for _, e := range ents {
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return !out[i].Dir && out[j].Dir
	})
	return out
}

func distinctPaths(ents []Entry) map[string]bool {
	seen := map[string]bool{}
	for _, e := range ents {
		seen[e.Path] = true
	}
	return seen
}

// FindConflicts normalizes every entry segment-by-segment and reports
// minimal conflict groups:
//
//   - name:     distinct originals fold to the same normalized path
//   - type:     a file's normalized path is a proper prefix of another
//     entry's normalized path (file used as a parent directory)
//   - reserved: a segment folds to a reserved device name
//   - invalid:  a segment folds to nothing (e.g. was only spaces/dots)
func FindConflicts(entries []Entry, t *FoldTable) []Group {
	byNorm := map[string][]Entry{}
	reserved := map[string][]Entry{}
	invalid := map[string][]Entry{}

	for _, e := range entries {
		var normParts []string
		for _, s := range splitPath(e.Path) {
			ns := NormalizeSegment(t, s)
			normParts = append(normParts, ns.Folded)
			key := strings.Join(normParts, "/")
			switch {
			case ns.Folded == "":
				invalid[key] = append(invalid[key], e)
			case ns.Reserved:
				reserved[key] = append(reserved[key], e)
			}
		}
		norm := strings.Join(normParts, "/")
		byNorm[norm] = append(byNorm[norm], e)
	}

	var groups []Group

	// Name conflicts: same normalized full path, distinct originals.
	for norm, ents := range byNorm {
		if len(distinctPaths(ents)) > 1 {
			groups = append(groups, Group{Norm: norm, Reason: "name", Origins: dedupEntries(ents)})
		}
	}

	// Type conflicts: a file whose normalized path is a proper prefix of
	// another entry's normalized path. This is the parent-prefix check —
	// comparing only full path strings would miss it. Only the shallowest
	// descendants are included to keep the group minimal.
	for norm, ents := range byNorm {
		var files []Entry
		for _, e := range ents {
			if !e.Dir {
				files = append(files, e)
			}
		}
		if len(files) == 0 {
			continue
		}
		minDepth := int(^uint(0) >> 1)
		var desc []Entry
		for other, ents2 := range byNorm {
			if !strings.HasPrefix(other, norm+"/") {
				continue
			}
			d := depth(other)
			switch {
			case d < minDepth:
				minDepth = d
				desc = append([]Entry{}, ents2...)
			case d == minDepth:
				desc = append(desc, ents2...)
			}
		}
		if len(desc) > 0 {
			origins := append(append([]Entry{}, files...), desc...)
			groups = append(groups, Group{Norm: norm, Reason: "type", Origins: dedupEntries(origins)})
		}
	}

	for key, ents := range reserved {
		groups = append(groups, Group{Norm: key, Reason: "reserved", Origins: dedupEntries(ents)})
	}
	for key, ents := range invalid {
		groups = append(groups, Group{Norm: key, Reason: "invalid", Origins: dedupEntries(ents)})
	}

	return minimize(groups)
}

// minimize drops groups that are fully implied by an already-reported group
// at an ancestor path: if every origin of a group sits at or under an origin
// of a previously kept group of the same class, the group is redundant.
// name/type share one coverage class (both are path collisions); reserved
// and invalid are tracked independently so they are never hidden by a name
// collision at the same path.
func minimize(groups []Group) []Group {
	sort.Slice(groups, func(i, j int) bool {
		di, dj := depth(groups[i].Norm), depth(groups[j].Norm)
		if di != dj {
			return di < dj
		}
		if groups[i].Norm != groups[j].Norm {
			return groups[i].Norm < groups[j].Norm
		}
		return groups[i].Reason < groups[j].Reason
	})

	classOf := func(reason string) string {
		if reason == "name" || reason == "type" {
			return "path"
		}
		return reason
	}
	covered := map[string]map[string]bool{}
	isCovered := func(class, p string) bool {
		set := covered[class]
		for {
			if set[p] {
				return true
			}
			i := strings.LastIndexByte(p, '/')
			if i < 0 {
				return false
			}
			p = p[:i]
		}
	}

	var out []Group
	for _, g := range groups {
		class := classOf(g.Reason)
		all := len(g.Origins) > 0
		for _, o := range g.Origins {
			if !isCovered(class, o.Path) {
				all = false
				break
			}
		}
		if all {
			continue
		}
		out = append(out, g)
		if covered[class] == nil {
			covered[class] = map[string]bool{}
		}
		for _, o := range g.Origins {
			covered[class][o.Path] = true
		}
	}
	return out
}
