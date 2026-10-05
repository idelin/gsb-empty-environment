package selector

import "sort"

// Selection is one selected test with its shortest impact path.
type Selection struct {
	Test     string   `json:"test"`
	Distance int      `json:"distance"` // edges from the changed root to the test
	Root     string   `json:"root"`     // originating changed symbol
	Path     []string `json:"path"`     // root -> ... -> test
}

// Report is the full selection result.
type Report struct {
	Changes   []Change    `json:"changes"`
	Selected  []Selection `json:"selected"`
	Uncovered []string    `json:"uncovered"` // changed symbols reaching no test
}

// reverseAdjacency unions the expanded edges of both graphs, callee -> callers.
// The union is deliberate: impact travels along edges that exist now OR that
// existed before (a removed edge still identifies previously dependent code).
func reverseAdjacency(graphs ...*Graph) map[string][]string {
	adj := map[string]map[string]bool{}
	for _, g := range graphs {
		u := g.Universe()
		for _, e := range g.Edges {
			for _, callee := range ExpandEdge(e, u) {
				if adj[callee] == nil {
					adj[callee] = map[string]bool{}
				}
				adj[callee][e.Caller] = true
			}
		}
	}
	out := map[string][]string{}
	for callee, callers := range adj {
		list := make([]string, 0, len(callers))
		for c := range callers {
			list = append(list, c)
		}
		sort.Strings(list)
		out[callee] = list
	}
	return out
}

// Select runs multi-source BFS from the changed set over the union reverse
// graph and returns the shortest impact path to every reachable test in the
// new graph. Cycles (recursive calls) are handled by visited marking.
func Select(oldG, newG *Graph, changes []Change) *Report {
	adj := reverseAdjacency(oldG, newG)

	dist := map[string]int{}
	parent := map[string]string{}
	rootOf := map[string]string{}
	var queue []string
	for _, c := range changes {
		if _, ok := dist[c.Symbol]; !ok {
			dist[c.Symbol] = 0
			rootOf[c.Symbol] = c.Symbol
			queue = append(queue, c.Symbol)
		}
	}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, caller := range adj[node] {
			if _, seen := dist[caller]; seen {
				continue
			}
			dist[caller] = dist[node] + 1
			parent[caller] = node
			rootOf[caller] = rootOf[node]
			queue = append(queue, caller)
		}
	}

	newU := newG.Universe()
	var tests []string
	for id, s := range newU {
		if IsTest(s) {
			tests = append(tests, id)
		}
	}
	sort.Strings(tests)

	rep := &Report{Changes: changes}
	reached := map[string]bool{}
	for _, t := range tests {
		d, ok := dist[t]
		if !ok {
			continue
		}
		path := []string{t}
		for cur := t; cur != rootOf[cur]; cur = parent[cur] {
			path = append(path, parent[cur])
		}
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
		rep.Selected = append(rep.Selected, Selection{
			Test:     t,
			Distance: d,
			Root:     rootOf[t],
			Path:     path,
		})
		reached[rootOf[t]] = true
	}
	for _, c := range changes {
		if !reached[c.Symbol] {
			rep.Uncovered = append(rep.Uncovered, c.Symbol)
		}
	}
	sort.Strings(rep.Uncovered)
	return rep
}
