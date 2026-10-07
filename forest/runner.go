package forest

import "fmt"

// OpKind identifies the type of an offline operation.
type OpKind int

const (
	OpAdd OpKind = iota
	OpRemove
	OpQuery
)

// Op is a single offline operation on the forest.
type Op struct {
	Kind OpKind
	U, V int
}

// Result is the outcome of one operation.
type Result struct {
	Op  Op
	OK  bool   // query: connected; add/remove: applied
	Err error  // nil on success
}

// String renders the per-operation output line.
func (r Result) String() string {
	switch r.Op.Kind {
	case OpQuery:
		if r.OK {
			return fmt.Sprintf("query %d %d: connected", r.Op.U, r.Op.V)
		}
		return fmt.Sprintf("query %d %d: disconnected", r.Op.U, r.Op.V)
	case OpAdd:
		if r.Err != nil {
			return fmt.Sprintf("add %d %d: rejected (%v)", r.Op.U, r.Op.V, r.Err)
		}
		return fmt.Sprintf("add %d %d: ok", r.Op.U, r.Op.V)
	case OpRemove:
		if r.Err != nil {
			return fmt.Sprintf("remove %d %d: rejected (%v)", r.Op.U, r.Op.V, r.Err)
		}
		return fmt.Sprintf("remove %d %d: ok", r.Op.U, r.Op.V)
	}
	return "unknown op"
}

// Failure records a rejected operation together with its 1-based position
// in the input sequence.
type Failure struct {
	Pos int
	Op  Op
	Err error
}

// Report is the outcome of running a full offline operation sequence.
type Report struct {
	Results  []Result  // one per input operation, in order
	Failures []Failure // rejected add/remove ops; positions of repeated
	// failures are preserved (one entry per failed operation)
}

// Run applies the complete operation sequence offline to a fresh forest and
// returns per-operation results plus the list of failures with positions.
func Run(ops []Op) *Report {
	return RunOn(New(), ops)
}

// RunOn applies the sequence to an existing forest.
func RunOn(f *Forest, ops []Op) *Report {
	rep := &Report{Results: make([]Result, 0, len(ops))}
	for i, op := range ops {
		res := Result{Op: op}
		switch op.Kind {
		case OpAdd:
			res.Err = f.AddEdge(op.U, op.V)
			res.OK = res.Err == nil
		case OpRemove:
			res.Err = f.RemoveEdge(op.U, op.V)
			res.OK = res.Err == nil
		case OpQuery:
			res.OK = f.Connected(op.U, op.V)
		default:
			res.Err = fmt.Errorf("forest: unknown op kind %d", op.Kind)
		}
		if res.Err != nil {
			rep.Failures = append(rep.Failures, Failure{Pos: i + 1, Op: op, Err: res.Err})
		}
		rep.Results = append(rep.Results, res)
	}
	return rep
}
