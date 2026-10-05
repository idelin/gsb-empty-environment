package main

import "sort"

// Selection is one selected test together with its shortest impact path.
// Path starts at the test and ends at a changed symbol.
type Selection struct {
	Test string   `json:"test"`
	Path []string `json:"path"`
}

// Select finds every test in the new graph that can reach a changed
// symbol, following call edges in reverse (callee -> caller). Edges from
// both graph versions are unioned so that callers of deleted symbols are
// still impacted. Dynamic candidate edges contribute one reverse edge per
// candidate, and unresolved calls contribute their conservatively
// expanded callee sets. Cycles (including direct recursion) terminate via
// the visited set. BFS layering guarantees each returned path is a
// shortest impact path.
func Select(oldG, newG *Graph, changes *ChangeSet) []Selection {
	rev := make(map[string][]string)
	addReverse := func(g *Graph) {
		for from, edges := range g.Edges {
			for _, e := range edges {
				for _, to := range g.effectiveCallees(e) {
					rev[to] = append(rev[to], from)
				}
			}
		}
	}
	addReverse(oldG)
	addReverse(newG)
	for k := range rev {
		rev[k] = dedup(rev[k])
	}

	visited := make(map[string]bool)
	parent := make(map[string]string)
	var queue []string
	for _, r := range changes.Roots() {
		if !visited[r] {
			visited[r] = true
			queue = append(queue, r)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, caller := range rev[cur] {
			if !visited[caller] {
				visited[caller] = true
				parent[caller] = cur
				queue = append(queue, caller)
			}
		}
	}

	var tests []string
	for name, sym := range newG.Symbols {
		if sym.Test && visited[name] {
			tests = append(tests, name)
		}
	}
	sort.Strings(tests)

	var out []Selection
	for _, t := range tests {
		out = append(out, Selection{Test: t, Path: impactPath(t, parent)})
	}
	return out
}

// impactPath walks parent pointers from the test back to the changed root.
func impactPath(test string, parent map[string]string) []string {
	path := []string{test}
	for {
		p, ok := parent[path[len(path)-1]]
		if !ok {
			return path
		}
		path = append(path, p)
	}
}
