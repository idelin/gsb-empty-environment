package admission

import (
	"sort"
)

// Subtree is a complete table replacement fragment used by OpReplace.
// Every terminal (scalar leaf) carries its own signed source, so a single
// replacement can merge values originating from different principals.
// Relative terminal paths must be non-empty and must not alias each other:
// ["a"] and ["a","b"] cannot both be terminals in the same fragment.
type Subtree struct {
	terminals map[string]subTerminal
}

type subTerminal struct {
	path   Path
	lit    Literal
	source Source
}

// NewSubtree builds a replacement fragment. It rejects empty paths and
// alias conflicts.
func NewSubtree(entries ...SubtreeEntry) (*Subtree, error) {
	sub := &Subtree{terminals: map[string]subTerminal{}}
	for _, entry := range entries {
		if len(entry.Path) == 0 {
			return nil, newError(ErrInvalidInput, -1, entry.Path,
				"subtree terminal path must be non-empty")
		}
		key := encodePath(entry.Path)
		if _, exists := sub.terminals[key]; exists {
			return nil, newError(ErrInvalidInput, -1, entry.Path,
				"duplicate subtree terminal")
		}
		sub.terminals[key] = subTerminal{
			path:   clonePath(entry.Path),
			lit:    entry.Literal,
			source: entry.Source,
		}
	}
	if err := sub.checkAliases(); err != nil {
		return nil, err
	}
	return sub, nil
}

// SubtreeEntry is one leaf in a replacement fragment.
type SubtreeEntry struct {
	Path    Path
	Literal Literal
	Source  Source
}

// checkAliases rejects pairs where one terminal is an ancestor of another,
// which would make the fragment an invalid table.
func (s *Subtree) checkAliases() error {
	paths := make([]Path, 0, len(s.terminals))
	for _, term := range s.terminals {
		paths = append(paths, term.path)
	}
	sort.Slice(paths, func(i, j int) bool {
		return comparePath(paths[i], paths[j]) < 0
	})
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if pathPrefix(paths[i], paths[j]) {
				return newError(ErrInvalidInput, -1, paths[j],
					"subtree terminal %v is nested under terminal %v",
					[]string(paths[j]), []string(paths[i]))
			}
		}
	}
	return nil
}

// terminalsInOrder returns the fragment leaves in path order.
func (s *Subtree) terminalsInOrder() []subTerminal {
	out := make([]subTerminal, 0, len(s.terminals))
	for _, term := range s.terminals {
		out = append(out, term)
	}
	sort.Slice(out, func(i, j int) bool {
		return comparePath(out[i].path, out[j].path) < 0
	})
	return out
}

// clone returns an independent deep copy.
func (s *Subtree) clone() *Subtree {
	if s == nil {
		return nil
	}
	cp := &Subtree{terminals: make(map[string]subTerminal, len(s.terminals))}
	for key, term := range s.terminals {
		cp.terminals[key] = subTerminal{
			path:   clonePath(term.path),
			lit:    term.lit,
			source: term.source,
		}
	}
	return cp
}
