package netsec

import "fmt"

// EdgeKind classifies how a dependency edge is followed during closure
// computation.
type EdgeKind int

const (
	// EdgeRequired edges are always followed; their target objects must be
	// readable or Build fails.
	EdgeRequired EdgeKind = iota
	// EdgeOptional edges are followed only when the target node name is
	// listed in Config.Optional for this build.
	EdgeOptional
	// EdgePlatform edges are followed only when the edge's GOOS/GOARCH
	// constraints match the build configuration. An empty constraint is a
	// wildcard.
	EdgePlatform
)

type edge struct {
	kind         EdgeKind
	to           string
	goos, goarch string
}

type node struct {
	name  string
	files []string
}

// Graph describes the release package as nodes owning files, connected by
// typed dependency edges. Construct it with the zero value.
type Graph struct {
	nodes map[string]*node
	edges map[string][]edge
}

// Add registers a node owning the given canonical relative file paths.
// A node may own zero files and act purely as a dependency hub.
func (g *Graph) Add(name string, files ...string) error {
	if name == "" {
		return fmt.Errorf("netsec: empty node name")
	}
	if g.nodes == nil {
		g.nodes = make(map[string]*node)
		g.edges = make(map[string][]edge)
	}
	if _, dup := g.nodes[name]; dup {
		return fmt.Errorf("netsec: duplicate node %q", name)
	}
	g.nodes[name] = &node{name: name, files: append([]string(nil), files...)}
	return nil
}

func (g *Graph) link(from string, e edge) error {
	if g.nodes == nil {
		return fmt.Errorf("netsec: unknown node %q", from)
	}
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("netsec: unknown node %q", from)
	}
	if _, ok := g.nodes[e.to]; !ok {
		return fmt.Errorf("netsec: unknown node %q", e.to)
	}
	g.edges[from] = append(g.edges[from], e)
	return nil
}

// Require declares that from always depends on to.
func (g *Graph) Require(from, to string) error {
	return g.link(from, edge{kind: EdgeRequired, to: to})
}

// Optional declares that from depends on to only when to is activated via
// Config.Optional at build time.
func (g *Graph) Optional(from, to string) error {
	return g.link(from, edge{kind: EdgeOptional, to: to})
}

// Platform declares that from depends on to only for configurations whose
// GOOS and GOARCH match the given constraints; an empty constraint matches
// every value.
func (g *Graph) Platform(from, to, goos, goarch string) error {
	return g.link(from, edge{kind: EdgePlatform, to: to, goos: goos, goarch: goarch})
}
