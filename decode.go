package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// ID is a graph object identifier. IDs are signed 32-bit integers so that
// invalid wire values can be rejected without relying on the host's native
// integer width.
type ID int64

const (
	// MaxNodes is the largest graph accepted by Decode.
	MaxNodes = 1000

	// MinID and MaxID bound the accepted signed wire representation.
	MinID ID = -1 << 31
	MaxID ID = 1<<31 - 1
)

// MaxNodeCount is an alias for MaxNodes.
const MaxNodeCount = MaxNodes

// EdgeKind describes who owns a child object.
type EdgeKind uint8

const (
	// Own transfers destruction responsibility to the parent.
	Own EdgeKind = iota
	// Borrow does not transfer destruction responsibility.
	Borrow

	// Aliases kept for callers that prefer explicit edge names.
	EdgeOwn    = Own
	EdgeBorrow = Borrow
	OwnEdge    = Own
	BorrowEdge = Borrow
)

func (k EdgeKind) String() string {
	switch k {
	case Own:
		return "own"
	case Borrow:
		return "borrow"
	default:
		return "unknown"
	}
}

// MarshalText implements encoding.TextMarshaler.
func (k EdgeKind) MarshalText() ([]byte, error) {
	switch k {
	case Own, Borrow:
		return []byte(k.String()), nil
	default:
		return nil, fmt.Errorf("%w: edge kind %d", ErrUnknownEdgeKind, k)
	}
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (k *EdgeKind) UnmarshalText(text []byte) error {
	parsed, err := ParseEdgeKind(string(text))
	if err != nil {
		return err
	}
	*k = parsed
	return nil
}

// ParseEdgeKind parses the wire names "own" and "borrow".
func ParseEdgeKind(text string) (EdgeKind, error) {
	switch text {
	case "own":
		return Own, nil
	case "borrow":
		return Borrow, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnknownEdgeKind, text)
	}
}

// Record is one input object record. Children are positional: the type table
// supplies one edge kind for each position.
type Record struct {
	ID       ID
	Type     string
	Children []ID
}

// TypeTable maps a type name to the edge kind of every child position.
type TypeTable map[string][]EdgeKind

// Child is a child reference presented to Constructor. Own children already
// have constructed values. Borrow children carry only their stable ID.
type Child struct {
	ID    ID
	Kind  EdgeKind
	Value any
}

// Construction is the immutable input supplied to Constructor.
type Construction struct {
	ID       ID
	Type     string
	Children []Child
}

// Constructor builds one object. For a borrow child, only Child.ID is
// meaningful; the borrow target may not have been constructed yet.
type Constructor func(Construction) (any, error)

// Destructor releases exactly one object built by Constructor.
type Destructor func(id ID, typeName string, value any)

var (
	ErrInvalidGraph         = errors.New("netsec: invalid graph")
	ErrTooManyNodes         = fmt.Errorf("%w: too many nodes", ErrInvalidGraph)
	ErrDuplicateID          = fmt.Errorf("%w: duplicate object ID", ErrInvalidGraph)
	ErrIDOutOfRange         = fmt.Errorf("%w: object ID out of range", ErrInvalidGraph)
	ErrRootNotFound         = fmt.Errorf("%w: root ID not found", ErrInvalidGraph)
	ErrUnknownType          = fmt.Errorf("%w: unknown type", ErrInvalidGraph)
	ErrEdgeCountMismatch    = fmt.Errorf("%w: child count does not match type table", ErrInvalidGraph)
	ErrUnknownEdgeKind      = fmt.Errorf("%w: unknown edge kind", ErrInvalidGraph)
	ErrUnknownNode          = fmt.Errorf("%w: child ID not found", ErrInvalidGraph)
	ErrOwnCycle             = fmt.Errorf("%w: own-edge cycle", ErrInvalidGraph)
	ErrSharedOwn            = fmt.Errorf("%w: object has multiple own parents", ErrInvalidGraph)
	ErrOwnInDegree          = fmt.Errorf("%w: non-root object has no own parent", ErrInvalidGraph)
	ErrInvalidBorrowTarget  = fmt.Errorf("%w: borrow target is not own-reachable from root", ErrInvalidGraph)
	ErrNilConstructor       = errors.New("netsec: nil constructor")
	ErrNilDestructor        = errors.New("netsec: nil destructor")
	ErrConstructionFailed   = errors.New("netsec: construction failed")
	ErrConstructionPanicked = errors.New("netsec: constructor panicked")
)

type preparedNode struct {
	record Record
	own    []ID
	borrow []ID
	kinds  []EdgeKind
	depth  int
}

type validatedGraph struct {
	rootID ID
	order  []*preparedNode
}

// Decode validates the complete graph before invoking construct. Only
// own-reachable objects are constructed. Construction proceeds by own-edge
// depth, deepest first, with IDs ascending inside each depth. Destructors run
// exactly once per successfully constructed object in the reverse order.
func Decode(records []Record, types TypeTable, rootID ID, construct Constructor, destroy Destructor) (*Graph, error) {
	if construct == nil {
		return nil, ErrNilConstructor
	}
	if destroy == nil {
		return nil, ErrNilDestructor
	}

	validated, err := validateGraph(records, types, rootID)
	if err != nil {
		return nil, err
	}

	built := make([]builtNode, 0, len(validated.order))
	values := make(map[ID]any, len(validated.order))
	for _, node := range validated.order {
		input := makeConstruction(node, values)
		value, err := invokeConstructor(construct, input)
		if err != nil {
			destroyBuilt(built, destroy)
			return nil, fmt.Errorf("%w: object %d type %q: %w", ErrConstructionFailed, node.record.ID, node.record.Type, err)
		}

		built = append(built, builtNode{
			id:       node.record.ID,
			typeName: node.record.Type,
			value:    value,
		})
		values[node.record.ID] = value
	}

	rootValue, ok := values[validated.rootID]
	if !ok {
		destroyBuilt(built, destroy)
		return nil, fmt.Errorf("%w: root %d was not constructed", ErrInvalidGraph, validated.rootID)
	}

	return &Graph{
		rootID:    validated.rootID,
		rootValue: rootValue,
		built:     built,
		destroy:   destroy,
	}, nil
}

func validateGraph(records []Record, types TypeTable, rootID ID) (*validatedGraph, error) {
	if len(records) > MaxNodes {
		return nil, fmt.Errorf("%w: got %d, limit %d", ErrTooManyNodes, len(records), MaxNodes)
	}
	if rootID < MinID || rootID > MaxID {
		return nil, fmt.Errorf("%w: root %d", ErrIDOutOfRange, rootID)
	}

	nodes := make(map[ID]*preparedNode, len(records))
	for _, record := range records {
		if record.ID < MinID || record.ID > MaxID {
			return nil, fmt.Errorf("%w: %d", ErrIDOutOfRange, record.ID)
		}
		if _, exists := nodes[record.ID]; exists {
			return nil, fmt.Errorf("%w: %d", ErrDuplicateID, record.ID)
		}
		nodes[record.ID] = &preparedNode{record: record}
	}

	if _, ok := nodes[rootID]; !ok {
		return nil, fmt.Errorf("%w: %d", ErrRootNotFound, rootID)
	}

	// Validate every record's schema and child references before looking at
	// graph reachability. Map iteration is avoided so errors are deterministic.
	for _, record := range records {
		edges, ok := types[record.Type]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownType, record.Type)
		}
		if len(edges) != len(record.Children) {
			return nil, fmt.Errorf("%w: type %q has %d edges for %d children",
				ErrEdgeCountMismatch, record.Type, len(edges), len(record.Children))
		}

		node := nodes[record.ID]
		node.own = make([]ID, 0, len(record.Children))
		node.borrow = make([]ID, 0, len(record.Children))
		node.kinds = make([]EdgeKind, len(record.Children))
		for i, childID := range record.Children {
			if childID < MinID || childID > MaxID {
				return nil, fmt.Errorf("%w: child %d of object %d", ErrIDOutOfRange, childID, record.ID)
			}
			if _, exists := nodes[childID]; !exists {
				return nil, fmt.Errorf("%w: child %d of object %d", ErrUnknownNode, childID, record.ID)
			}

			switch edges[i] {
			case Own:
				node.own = append(node.own, childID)
			case Borrow:
				node.borrow = append(node.borrow, childID)
			default:
				return nil, fmt.Errorf("%w: %d at type %q position %d",
					ErrUnknownEdgeKind, edges[i], record.Type, i)
			}
			node.kinds[i] = edges[i]
		}
	}

	reachable := make(map[ID]bool, len(nodes))
	reachable[rootID] = true
	stack := []ID{rootID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, childID := range nodes[id].own {
			if !reachable[childID] {
				reachable[childID] = true
				stack = append(stack, childID)
			}
		}
	}

	ownInDegree := make(map[ID]int, len(reachable))
	for id := range reachable {
		for _, childID := range nodes[id].own {
			ownInDegree[childID]++
		}
	}

	if hasOwnCycle(reachable, nodes, ownInDegree) {
		return nil, ErrOwnCycle
	}
	for _, record := range records {
		id := record.ID
		if !reachable[id] {
			continue
		}
		degree := ownInDegree[id]
		if id == rootID {
			if degree != 0 {
				return nil, fmt.Errorf("%w: root %d has an own parent", ErrOwnCycle, id)
			}
			continue
		}
		switch {
		case degree == 0:
			return nil, fmt.Errorf("%w: object %d", ErrOwnInDegree, id)
		case degree > 1:
			return nil, fmt.Errorf("%w: object %d has %d own parents", ErrSharedOwn, id, degree)
		}
	}

	// A borrow edge never owns its target, but the target must still be an
	// object in the root's own-reachable set. Borrow cycles are permitted.
	for _, record := range records {
		for _, target := range nodes[record.ID].borrow {
			if !reachable[target] {
				return nil, fmt.Errorf("%w: object %d borrows %d", ErrInvalidBorrowTarget, record.ID, target)
			}
		}
	}

	if err := assignDepths(rootID, reachable, nodes); err != nil {
		return nil, err
	}

	order := constructionOrder(reachable, nodes)
	return &validatedGraph{rootID: rootID, order: order}, nil
}

func hasOwnCycle(reachable map[ID]bool, nodes map[ID]*preparedNode, inDegree map[ID]int) bool {
	work := make(map[ID]int, len(inDegree))
	queue := make([]ID, 0, len(reachable))
	for id := range reachable {
		work[id] = inDegree[id]
		if work[id] == 0 {
			queue = append(queue, id)
		}
	}

	processed := 0
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		processed++
		for _, childID := range nodes[id].own {
			work[childID]--
			if work[childID] == 0 {
				queue = append(queue, childID)
			}
		}
	}
	return processed != len(reachable)
}

func assignDepths(rootID ID, reachable map[ID]bool, nodes map[ID]*preparedNode) error {
	for id := range reachable {
		nodes[id].depth = -1
	}
	nodes[rootID].depth = 0
	stack := []ID{rootID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := nodes[id]
		for _, childID := range node.own {
			child := nodes[childID]
			if child.depth >= 0 {
				return fmt.Errorf("%w: object %d reached twice", ErrOwnCycle, childID)
			}
			child.depth = node.depth + 1
			stack = append(stack, childID)
		}
	}
	return nil
}

func constructionOrder(reachable map[ID]bool, nodes map[ID]*preparedNode) []*preparedNode {
	byDepth := make([][]*preparedNode, 0)
	for id := range reachable {
		node := nodes[id]
		for len(byDepth) <= node.depth {
			byDepth = append(byDepth, nil)
		}
		byDepth[node.depth] = append(byDepth[node.depth], node)
	}
	for _, layer := range byDepth {
		sort.Slice(layer, func(i, j int) bool {
			return layer[i].record.ID < layer[j].record.ID
		})
	}

	order := make([]*preparedNode, 0, len(reachable))
	for depth := len(byDepth) - 1; depth >= 0; depth-- {
		order = append(order, byDepth[depth]...)
	}
	return order
}

func makeConstruction(node *preparedNode, values map[ID]any) Construction {
	children := make([]Child, 0, len(node.record.Children))
	for i, childID := range node.record.Children {
		kind := node.kinds[i]
		child := Child{ID: childID, Kind: kind}
		if kind == Own {
			child.Value = values[childID]
		}
		children = append(children, child)
	}
	return Construction{
		ID:       node.record.ID,
		Type:     node.record.Type,
		Children: children,
	}
}

func invokeConstructor(construct Constructor, input Construction) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = nil
			err = fmt.Errorf("%w: %v", ErrConstructionPanicked, recovered)
		}
	}()
	return construct(input)
}

type builtNode struct {
	id       ID
	typeName string
	value    any
}

func destroyBuilt(built []builtNode, destroy Destructor) {
	for i := len(built) - 1; i >= 0; i-- {
		destroy(built[i].id, built[i].typeName, built[i].value)
	}
}
