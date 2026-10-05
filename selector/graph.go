// Package selector implements change-based test selection over symbol call graphs.
package selector

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
	"strings"
)

// Symbol is a node in the call graph (function, method, test, ...).
type Symbol struct {
	ID   string `json:"id"`   // fully qualified, e.g. "pkg.Foo"
	Kind string `json:"kind"` // "func", "method", "test", ...
}

// Edge is one call site. Exactly one of three forms:
//   - static:      Callee set
//   - dynamic:     Dynamic=true, Candidates holds the possible targets
//   - unresolved:  Unresolved=true, Name is the called (short) name; the
//     target set is expanded conservatively from the symbol universe
type Edge struct {
	Caller     string   `json:"caller"`
	Callee     string   `json:"callee,omitempty"`
	Dynamic    bool     `json:"dynamic,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Unresolved bool     `json:"unresolved,omitempty"`
	Name       string   `json:"name,omitempty"`
}

// Graph is a call graph snapshot.
type Graph struct {
	Symbols []Symbol `json:"symbols"`
	Edges   []Edge   `json:"edges"`
}

// LoadGraph reads a graph from a JSON file. An empty path yields an empty graph.
func LoadGraph(path string) (*Graph, error) {
	g := &Graph{}
	if path == "" {
		return g, nil
	}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, g); err != nil {
		return nil, fmt.Errorf("parse %s: %v", path, err)
	}
	return g, nil
}

// Universe indexes symbols by ID.
func (g *Graph) Universe() map[string]Symbol {
	u := make(map[string]Symbol, len(g.Symbols))
	for _, s := range g.Symbols {
		u[s.ID] = s
	}
	return u
}

// ShortName returns the part after the last ".".
func ShortName(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

// PackageOf returns the part before the last ".".
func PackageOf(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[:i]
	}
	return ""
}

// IsTest reports whether a symbol is a test entry point.
func IsTest(s Symbol) bool {
	return s.Kind == "test" || strings.HasPrefix(ShortName(s.ID), "Test")
}

// ExpandEdge resolves an edge to a sorted, de-duplicated set of concrete
// callee IDs. Unresolved calls are expanded by explicit conservative rules:
//
//  1. all symbols whose short name equals the unresolved name;
//  2. if none match, all symbols in the caller's package;
//  3. if the package is empty, every symbol in the universe.
func ExpandEdge(e Edge, universe map[string]Symbol) []string {
	set := map[string]bool{}
	switch {
	case e.Unresolved:
		for _, id := range expandUnresolved(e, universe) {
			set[id] = true
		}
	case e.Dynamic:
		for _, c := range e.Candidates {
			set[c] = true
		}
	case e.Callee != "":
		set[e.Callee] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func expandUnresolved(e Edge, universe map[string]Symbol) []string {
	var out []string
	for id := range universe {
		if ShortName(id) == e.Name {
			out = append(out, id)
		}
	}
	if len(out) > 0 {
		return out
	}
	pkg := PackageOf(e.Caller)
	for id := range universe {
		if PackageOf(id) == pkg && id != e.Caller {
			out = append(out, id)
		}
	}
	if len(out) > 0 {
		return out
	}
	for id := range universe {
		if id != e.Caller {
			out = append(out, id)
		}
	}
	return out
}
