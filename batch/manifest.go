package batch

import (
	"fmt"
	"sort"
)

// ManifestEntry is one frozen input: its content digest at freeze time and
// the explicit same-batch dependencies declared for it.
type ManifestEntry struct {
	Path   string   `json:"path"`
	Digest string   `json:"digest"`
	Deps   []string `json:"deps,omitempty"`
}

// Manifest is the frozen input list of a batch. Once frozen it never grows:
// files appearing later in the source directory are ignored until an
// explicit new batch is started.
type Manifest struct {
	Entries map[string]ManifestEntry `json:"entries"`
	// Order is a topological order (dependencies first) computed at freeze.
	Order []string `json:"order"`
}

// FreezeManifest scans dir on fs, digests every file, validates the
// explicitly declared dependencies and freezes the result.
func FreezeManifest(fs FS, dir string, deps map[string][]string) (*Manifest, error) {
	paths, err := fs.List(dir)
	if err != nil {
		return nil, fmt.Errorf("batch: list %q: %v", dir, err)
	}
	m := &Manifest{Entries: make(map[string]ManifestEntry, len(paths))}
	for _, p := range paths {
		data, err := fs.ReadFile(joinPath(dir, p))
		if err != nil {
			return nil, fmt.Errorf("batch: read %q: %v", p, err)
		}
		entry := ManifestEntry{Path: p, Digest: DigestOf(data)}
		if d, ok := deps[p]; ok {
			entry.Deps = append([]string(nil), d...)
		}
		m.Entries[p] = entry
	}
	for p, e := range m.Entries {
		for _, d := range e.Deps {
			if d == p {
				return nil, fmt.Errorf("batch: %q depends on itself", p)
			}
			if _, ok := m.Entries[d]; !ok {
				return nil, fmt.Errorf("batch: %q depends on %q which is not in the manifest", p, d)
			}
		}
	}
	order, err := topoOrder(m)
	if err != nil {
		return nil, err
	}
	m.Order = order
	return m, nil
}

// topoOrder returns a deterministic dependency-first order, or an error if
// the declared dependency graph has a cycle.
func topoOrder(m *Manifest) ([]string, error) {
	const (
		white = 0 // unvisited
		gray  = 1 // on stack
		black = 2 // done
	)
	color := make(map[string]int, len(m.Entries))
	var order []string
	var visit func(p string) error
	visit = func(p string) error {
		switch color[p] {
		case gray:
			return fmt.Errorf("batch: dependency cycle involving %q", p)
		case black:
			return nil
		}
		color[p] = gray
		deps := append([]string(nil), m.Entries[p].Deps...)
		sort.Strings(deps)
		for _, d := range deps {
			if err := visit(d); err != nil {
				return err
			}
		}
		color[p] = black
		order = append(order, p)
		return nil
	}
	paths := make([]string, 0, len(m.Entries))
	for p := range m.Entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := visit(p); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Dependents returns the transitive dependents of paths within the manifest
// (not including paths themselves).
func (m *Manifest) Dependents(paths ...string) map[string]bool {
	reverse := make(map[string][]string)
	for p, e := range m.Entries {
		for _, d := range e.Deps {
			reverse[d] = append(reverse[d], p)
		}
	}
	out := make(map[string]bool)
	stack := append([]string(nil), paths...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range reverse[cur] {
			if !out[next] {
				out[next] = true
				stack = append(stack, next)
			}
		}
	}
	return out
}

func joinPath(dir, p string) string {
	if dir == "" || dir == "." {
		return p
	}
	return dir + "/" + p
}
