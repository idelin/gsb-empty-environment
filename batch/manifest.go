package batch

import "fmt"

// Item is one explicit manifest entry. DependsOn lists paths of other
// manifest items whose same-batch results must be confirmed before this
// item is processed.
type Item struct {
	Path      string
	DependsOn []string
}

// validateManifest checks the manifest and returns a deterministic
// topological processing order (manifest order among ready items).
func validateManifest(manifest []Item) ([]string, error) {
	seen := make(map[string]bool, len(manifest))
	for _, it := range manifest {
		if it.Path == "" {
			return nil, fmt.Errorf("batch: manifest item with empty path")
		}
		if seen[it.Path] {
			return nil, fmt.Errorf("batch: duplicate manifest path %q", it.Path)
		}
		seen[it.Path] = true
	}
	for _, it := range manifest {
		for _, dep := range it.DependsOn {
			if !seen[dep] {
				return nil, fmt.Errorf("batch: item %q depends on unknown path %q", it.Path, dep)
			}
			if dep == it.Path {
				return nil, fmt.Errorf("batch: item %q depends on itself", it.Path)
			}
		}
	}

	// Kahn's algorithm, scanning in manifest order for determinism.
	remaining := make(map[string]Item, len(manifest))
	indegree := make(map[string]int, len(manifest))
	for _, it := range manifest {
		remaining[it.Path] = it
		indegree[it.Path] = len(it.DependsOn)
	}
	var order []string
	for len(remaining) > 0 {
		progressed := false
		for _, it := range manifest {
			if _, ok := remaining[it.Path]; !ok || indegree[it.Path] > 0 {
				continue
			}
			order = append(order, it.Path)
			delete(remaining, it.Path)
			for _, other := range remaining {
				for _, dep := range other.DependsOn {
					if dep == it.Path {
						indegree[other.Path]--
					}
				}
			}
			progressed = true
		}
		if !progressed {
			return nil, fmt.Errorf("batch: dependency cycle detected in manifest")
		}
	}
	return order, nil
}
