package forest

import "fmt"

// OpKind identifies the kind of an offline operation.
type OpKind int

const (
	// OpLink adds an edge.
	OpLink OpKind = iota
	// OpCut removes an edge.
	OpCut
	// OpConn asks whether two vertices are connected.
	OpConn
)

// Op is a single offline operation.
type Op struct {
	Kind OpKind
	U, V int
}

// LinkOp builds an edge-insertion operation.
func LinkOp(u, v int) Op { return Op{Kind: OpLink, U: u, V: v} }

// CutOp builds an edge-removal operation.
func CutOp(u, v int) Op { return Op{Kind: OpCut, U: u, V: v} }

// ConnOp builds a connectivity query.
func ConnOp(u, v int) Op { return Op{Kind: OpConn, U: u, V: v} }

// Result is the outcome of one operation. Exactly one Result is produced
// per input Op, in input order, so repeated failing operations each keep
// their position in the output.
type Result struct {
	Op Op
	// Connected is meaningful only for OpConn.
	Connected bool
	// Err is nil on success, ErrCycle or ErrNoEdge on failure.
	Err error
}

// OK reports whether the operation succeeded. For OpConn it reports the
// connectivity answer (queries never fail).
func (r Result) OK() bool {
	if r.Op.Kind == OpConn {
		return r.Connected
	}
	return r.Err == nil
}

func (r Result) String() string {
	switch r.Op.Kind {
	case OpLink:
		if r.Err != nil {
			return fmt.Sprintf("link %d %d: rejected (%v)", r.Op.U, r.Op.V, r.Err)
		}
		return fmt.Sprintf("link %d %d: ok", r.Op.U, r.Op.V)
	case OpCut:
		if r.Err != nil {
			return fmt.Sprintf("cut %d %d: rejected (%v)", r.Op.U, r.Op.V, r.Err)
		}
		return fmt.Sprintf("cut %d %d: ok", r.Op.U, r.Op.V)
	default:
		return fmt.Sprintf("conn %d %d: %t", r.Op.U, r.Op.V, r.Connected)
	}
}

// Process applies the full operation sequence to f offline and returns one
// Result per operation, in order. Failed operations do not mutate the
// forest, and duplicates are applied (and reported) individually at their
// own positions.
func Process(f *Forest, ops []Op) []Result {
	results := make([]Result, len(ops))
	for i, op := range ops {
		r := Result{Op: op}
		switch op.Kind {
		case OpLink:
			r.Err = f.Link(op.U, op.V)
		case OpCut:
			r.Err = f.Cut(op.U, op.V)
		case OpConn:
			r.Connected = f.Connected(op.U, op.V)
		}
		results[i] = r
	}
	return results
}
