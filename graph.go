package netsec

import "fmt"

// Instance identifies one concrete dependency instance. Instances are
// distinguished by the full tuple: two listings that agree on name and
// version but differ in source identity or digest are different instances.
type Instance struct {
	Source  string // identity of the source the listing came from
	Name    string
	Version string
	Digest  string
}

func (i Instance) String() string {
	return fmt.Sprintf("%s|%s@%s|%s", i.Source, i.Name, i.Version, i.Digest)
}

// ConflictsWith reports whether i and o are conflicting instances of the
// same module version: identical name and version but different source
// identity or digest, so they must never be mixed into one build.
func (i Instance) ConflictsWith(o Instance) bool {
	return i.Name == o.Name && i.Version == o.Version &&
		(i.Source != o.Source || i.Digest != o.Digest)
}

// Edge is a dependency from one instance to another.
type Edge struct {
	To Instance
	// Optional edges may be excluded from a build; a feasible build exists
	// that includes them, so they are traversable when searching for
	// conflict witnesses.
	Optional bool
	// When gates the edge on the build configuration; nil means the edge
	// is unconditional.
	When Cond
}

func (e Edge) enabled(assign uint32) bool {
	return e.When == nil || e.When.Eval(assign)
}

// Graph is a merged dependency graph built from several dependency
// listings, together with the configuration space the build service may
// choose from.
type Graph struct {
	// NumVars is the number of boolean configuration variables in use,
	// 0..MaxVars.
	NumVars int
	// Mutex lists groups of mutually exclusive variables: within each
	// group at most one variable may be true in a feasible assignment.
	Mutex [][]int
	Roots []Instance
	Edges map[Instance][]Edge
}

// feasible reports whether assign respects all mutex groups.
func (g *Graph) feasible(assign uint32) bool {
	for _, group := range g.Mutex {
		seen := false
		for _, v := range group {
			if assign&(1<<uint(v)) != 0 {
				if seen {
					return false
				}
				seen = true
			}
		}
	}
	return true
}
