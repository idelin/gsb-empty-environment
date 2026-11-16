package kustomization

// detectCycles builds a graph of kustomization-directory references and
// rejects any graph that contains a cycle, including a file that references
// its own directory.
func detectCycles(nodes map[string]string, configs map[string]*Kustomization, idx *fileIndex, hosts map[string]bool) []Violation {
	dirs := make(map[string]bool, len(nodes))
	configInDir := make(map[string][]string)
	for configPath, dir := range nodes {
		dirs[dir] = true
		configInDir[dir] = append(configInDir[dir], configPath)
	}

	edges := make(map[string]map[string]bool)
	for configPath, kc := range configs {
		from := nodes[configPath]
		targets := localReferenceTargets(kc, from, idx, hosts)
		for _, target := range targets {
			to, ok := referencedDir(target, nodes, dirs)
			if !ok {
				continue
			}
			if edges[from] == nil {
				edges[from] = map[string]bool{}
			}
			edges[from][to] = true
		}
	}

	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	color := make(map[string]int, len(dirs))
	var stack []string
	var cycleFound bool

	var visit func(dir string)
	visit = func(dir string) {
		if cycleFound {
			return
		}
		color[dir] = active
		stack = append(stack, dir)
		for next := range edges[dir] {
			if cycleFound {
				return
			}
			switch color[next] {
			case active:
				cycleFound = true
				return
			case unvisited:
				visit(next)
			}
		}
		stack = stack[:len(stack)-1]
		color[dir] = done
	}

	for _, dir := range sortedKeys(dirs) {
		if color[dir] == unvisited {
			visit(dir)
		}
	}
	if cycleFound {
		return []Violation{{Field: "references", Reason: "cyclic kustomization reference detected: " + joinStrings(stack, " -> ")}}
	}
	return nil
}

// referencedDir maps a resolved local reference to the kustomization
// directory it points at, but only when the reference names a participating
// directory or an exact kustomization file path. Falling back to the parent
// directory would falsely mark ordinary data files as self references.
func referencedDir(resolved string, nodes map[string]string, dirs map[string]bool) (string, bool) {
	if dir, ok := nodes[resolved]; ok {
		return dir, true
	}
	if dirs[resolved] {
		return resolved, true
	}
	return "", false
}

// localReferenceTargets resolves every local reference that may point at
// another kustomization directory. Remote references are not part of the
// local graph.
func localReferenceTargets(kc *Kustomization, baseDir string, idx *fileIndex, hosts map[string]bool) []string {
	var targets []string
	collect := func(entries []any) {
		for _, raw := range entries {
			ref := ""
			switch entry := raw.(type) {
			case string:
				ref = entry
			case map[string]any, map[any]any:
				m, _ := asMap(entry)
				if p, ok := pathOf(m); ok {
					ref = p
				}
			}
			if ref == "" || isRemoteReference(ref) {
				continue
			}
			if resolved, violations := idx.resolveLocal(baseDir, ref, "references"); violations == nil {
				targets = append(targets, resolved)
			}
		}
	}
	collect(kc.Resources)
	collect(kc.Bases)
	collect(kc.Patches)
	collect(kc.Generators)
	collect(kc.Transformers)
	return targets
}

// sortedKeys returns map keys in deterministic order without pulling in sort
// at every call site.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}
