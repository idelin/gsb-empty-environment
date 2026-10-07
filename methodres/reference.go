package methodres

import "fmt"

// This file holds the reference implementation used to cross-check the
// Resolver. It is deliberately naive: it enumerates every inheritance path
// explicitly instead of memoizing ancestor sets, and decides specificity by
// testing pairwise path existence. Correctness first, performance never.

// enumeratePaths returns every path that starts at iface and follows parent
// edges, including the trivial path [iface]. The graph is acyclic, so this
// terminates. The same interface may appear in many paths (diamonds); the
// caller dedups.
func enumeratePaths(g *Graph, iface string) [][]string {
	var out [][]string
	var walk func(path []string)
	walk = func(path []string) {
		out = append(out, append([]string(nil), path...))
		cur := path[len(path)-1]
		for _, p := range g.Interfaces[cur].Parents {
			walk(append(path, p))
		}
	}
	walk([]string{iface})
	return out
}

// reachableByPath reports whether target appears on any path from start.
func reachableByPath(g *Graph, start, target string) bool {
	for _, path := range enumeratePaths(g, start) {
		for _, n := range path {
			if n == target {
				return true
			}
		}
	}
	return false
}

// ReferenceResolve resolves method m for class className by explicit path
// enumeration. It mirrors Resolver.Resolve and is used as the test oracle.
func ReferenceResolve(g *Graph, className, m string) (Resolution, error) {
	c, ok := g.Classes[className]
	if !ok {
		return Resolution{}, fmt.Errorf("unknown class %q", className)
	}
	res := Resolution{Class: className, Method: m, Kind: None}

	if c.Overrides[m] {
		res.Kind, res.From = ClassOverride, className
		return res, nil
	}

	if pick, ok := c.Picks[m]; ok {
		in, exists := g.Interfaces[pick]
		if !exists {
			return Resolution{}, fmt.Errorf("class %q picks %q for %q: not an interface", className, pick, m)
		}
		reachable := false
		for _, root := range c.Interfaces {
			if reachableByPath(g, root, pick) {
				reachable = true
				break
			}
		}
		if !reachable {
			return Resolution{}, fmt.Errorf("class %q picks %q for %q: not reachable through inheritance", className, pick, m)
		}
		if !in.Defaults[m] {
			return Resolution{}, fmt.Errorf("class %q picks %q for %q: no default defined there", className, pick, m)
		}
		res.Kind, res.From = ExplicitPick, pick
		return res, nil
	}

	// Collect every defining interface seen on any path from any root.
	// Dedup by interface name: a diamond ancestor counts once.
	candidates := map[string]bool{}
	for _, root := range c.Interfaces {
		for _, path := range enumeratePaths(g, root) {
			for _, n := range path {
				if g.Interfaces[n].Defaults[m] {
					candidates[n] = true
				}
			}
		}
	}

	// Keep only definitions not reachable from another candidate: those
	// are the most specific. Pairwise path existence decides.
	var maximalList []string
	for cand := range candidates {
		dominated := false
		for other := range candidates {
			if other != cand && reachableByPath(g, other, cand) {
				dominated = true
				break
			}
		}
		if !dominated {
			maximalList = append(maximalList, cand)
		}
	}
	sortStrings(maximalList)

	switch len(maximalList) {
	case 0:
		return res, nil
	case 1:
		res.Kind, res.From = Inherited, maximalList[0]
		return res, nil
	default:
		res.Kind = Conflict
		for i := 0; i < len(maximalList); i++ {
			for j := i + 1; j < len(maximalList); j++ {
				res.Pairs = append(res.Pairs, [2]string{maximalList[i], maximalList[j]})
			}
		}
		return res, nil
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
