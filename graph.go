package netsec

// ObjectID uniquely identifies an object in the reference graph.
type ObjectID string

// Kind classifies an object.
type Kind string

const (
	// KindDirectory is a container object that may reference other objects.
	KindDirectory Kind = "directory"
	// KindSecret is a secret-bearing object. Restoring a directory that
	// references a secret restores the secret as well.
	KindSecret Kind = "secret"
)

// EdgeKind classifies a reference between two objects.
type EdgeKind string

const (
	// EdgeRequired marks a hard dependency. A required target must be present
	// and authorized, otherwise the whole recovery is rejected.
	EdgeRequired EdgeKind = "required"
	// EdgeOptional marks a soft dependency. Optional targets are not pulled in
	// unless the caller explicitly requests them.
	EdgeOptional EdgeKind = "optional"
)

// Ref is a directed reference from one object to another.
type Ref struct {
	From ObjectID
	To   ObjectID
	Kind EdgeKind
}

// Object is a node in the reference graph.
type Object struct {
	ID   ObjectID
	Kind Kind
}

// Graph is an object reference graph: a set of objects and the directed
// references between them.
type Graph struct {
	objects map[ObjectID]Object
	refs    map[ObjectID][]Ref
}

// NewGraph builds a Graph from the given objects and references. Later
// mutations of the input slices do not affect the graph.
func NewGraph(objects []Object, refs []Ref) *Graph {
	g := &Graph{
		objects: make(map[ObjectID]Object, len(objects)),
		refs:    make(map[ObjectID][]Ref),
	}
	for _, obj := range objects {
		g.objects[obj.ID] = obj
	}
	for _, ref := range refs {
		g.refs[ref.From] = append(g.refs[ref.From], ref)
	}
	return g
}

// Has reports whether the graph contains an object with the given ID.
func (g *Graph) Has(id ObjectID) bool {
	_, ok := g.objects[id]
	return ok
}

// ObjectKind returns the kind of the object and whether it exists.
func (g *Graph) ObjectKind(id ObjectID) (Kind, bool) {
	obj, ok := g.objects[id]
	return obj.Kind, ok
}

// refsFrom returns the references leaving the given object in their original
// order.
func (g *Graph) refsFrom(id ObjectID) []Ref {
	return g.refs[id]
}
