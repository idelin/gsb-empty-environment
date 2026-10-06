package migrate

import (
	"fmt"
	"sort"
	"strings"
)

// Rename describes one planned base-name change.
type Rename struct {
	Orig  string // absolute path before migration
	Temp  string // temporary base name used by the two-phase rename
	Final string // final base name
	node  *Node
}

// Plan is the full, deterministic old-to-new name mapping for a tree.
type Plan struct {
	Renames []Rename // parents always precede their descendants
}

// ComputePlan maps every node in the tree through norm and resolves
// collisions without ever overwriting an existing path:
//
//   - The mapping is computed for the whole tree up front; renaming a
//     directory rewrites the final paths of all its descendants.
//   - Nodes whose final paths collide form a conflict group. Within a
//     group, nodes are ordered by original path; the first keeps the
//     normalized name and the rest receive stable numeric suffixes
//     ("~2", "~3", ...), inserted before the extension for files.
//   - A node that does not move still occupies its path, so a node moving
//     onto it is suffixed (unless the occupant itself moves away).
func ComputePlan(t *Tree, norm Normalizer) (*Plan, error) {
	// Group nodes by depth so parents are decided before children.
	var levels [][]*Node
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		for _, name := range sortedKeys(n.Children) {
			c := n.Children[name]
			if n == t.Root && name == journalName {
				continue
			}
			for len(levels) <= depth {
				levels = append(levels, nil)
			}
			levels[depth] = append(levels[depth], c)
			walk(c, depth+1)
		}
	}
	walk(t.Root, 0)

	finalPath := map[*Node]string{t.Root: ""}
	taken := map[string]bool{}
	var renames []Rename

	for _, level := range levels {
		type want struct {
			desired string
			nodes   []*Node
		}
		groups := map[string]*want{}
		var order []string
		for _, n := range level {
			desired := norm.Normalize(n.Name)
			if desired == "" || strings.Contains(desired, "/") {
				return nil, fmt.Errorf("migrate: %q normalizes to invalid name %q", n.Path(), desired)
			}
			p := finalPath[n.Parent] + "/" + desired
			g, ok := groups[p]
			if !ok {
				g = &want{desired: desired}
				groups[p] = g
				order = append(order, p)
			}
			g.nodes = append(g.nodes, n)
		}
		sort.Strings(order)
		for _, p := range order {
			g := groups[p]
			members := g.nodes
			sort.Slice(members, func(i, j int) bool { return members[i].Path() < members[j].Path() })
			parentPath := finalPath[members[0].Parent]
			idx := 0
			for _, n := range members {
				var candidate string
				for {
					if idx == 0 {
						candidate = g.desired
					} else {
						candidate = suffixed(g.desired, idx+1, n.Dir)
					}
					idx++
					if !taken[parentPath+"/"+candidate] {
						break
					}
				}
				fp := parentPath + "/" + candidate
				finalPath[n] = fp
				taken[fp] = true
				if candidate != n.Name {
					renames = append(renames, Rename{Orig: n.Path(), Final: candidate, node: n})
				}
			}
		}
	}

	// Assign per-parent unique temporary names for the two-phase rename.
	used := map[*Node]map[string]bool{}
	for i := range renames {
		parent := renames[i].node.Parent
		names, ok := used[parent]
		if !ok {
			names = map[string]bool{}
			for name := range parent.Children {
				names[name] = true
			}
			used[parent] = names
		}
		tmp := fmt.Sprintf(".migrate-%d", i)
		for names[tmp] {
			tmp += "_"
		}
		names[tmp] = true
		renames[i].Temp = tmp
	}
	return &Plan{Renames: renames}, nil
}

// suffixed inserts a stable numeric suffix before the extension of a file
// name ("report.txt" -> "report~2.txt") or appends it to a directory name.
func suffixed(name string, k int, isDir bool) string {
	suffix := fmt.Sprintf("~%d", k)
	if !isDir {
		if dot := strings.LastIndex(name, "."); dot > 0 {
			return name[:dot] + suffix + name[dot:]
		}
	}
	return name + suffix
}
