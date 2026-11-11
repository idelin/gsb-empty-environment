package netsec

import "fmt"

// NodeID identifies a node within its procedure.
type NodeID int

// Kind is the operation performed by a Node.
type Kind int

const (
	KindAlloc  Kind = iota // Dst = new object; the node is the allocation site
	KindCopy               // Dst = Src
	KindLoad               // Dst = Base.Field
	KindStore              // Base.Field = Src
	KindFree               // free(Src)
	KindDeref              // use *Src
	KindBranch             // non-deterministic branch to Next[0] or Next[1]
	KindCall               // Dst = Callee(Args...)
	KindReturn             // return Proc.Result
)

// Node is a single statement in a procedure's control-flow graph.
type Node struct {
	ID     NodeID
	Kind   Kind
	Dst    string   // Alloc, Copy, Load, Call
	Src    string   // Copy, Store (value), Free, Deref
	Base   string   // Load, Store
	Field  string   // Load, Store
	Args   []string // Call
	Callee string   // Call
	Next   []NodeID // Branch: {then, else}; other non-return nodes: {next}
}

// Proc is a procedure: an entry node plus a node graph. Params and Result
// name local variables.
type Proc struct {
	Name   string
	Params []string
	Result string
	Entry  NodeID
	Nodes  map[NodeID]*Node
}

// Program is a set of procedures with a designated entry procedure.
type Program struct {
	Main  string
	Procs map[string]*Proc
}

// Validate checks structural well-formedness of the procedure graphs.
func (p *Program) Validate() error {
	if p == nil {
		return fmt.Errorf("netsec: nil program")
	}
	if _, ok := p.Procs[p.Main]; !ok {
		return fmt.Errorf("netsec: main procedure %q not defined", p.Main)
	}
	for _, proc := range p.Procs {
		if _, ok := proc.Nodes[proc.Entry]; !ok {
			return fmt.Errorf("netsec: proc %s: entry node %d missing", proc.Name, proc.Entry)
		}
		for _, n := range proc.Nodes {
			switch n.Kind {
			case KindBranch:
				if len(n.Next) != 2 {
					return fmt.Errorf("netsec: proc %s node %d: branch needs 2 successors", proc.Name, n.ID)
				}
			case KindReturn:
				if len(n.Next) != 0 {
					return fmt.Errorf("netsec: proc %s node %d: return has successors", proc.Name, n.ID)
				}
			default:
				if len(n.Next) != 1 {
					return fmt.Errorf("netsec: proc %s node %d: need exactly 1 successor", proc.Name, n.ID)
				}
			}
			if n.Kind == KindCall {
				if _, ok := p.Procs[n.Callee]; !ok {
					return fmt.Errorf("netsec: proc %s node %d: callee %q undefined", proc.Name, n.ID, n.Callee)
				}
			}
			for _, succ := range n.Next {
				if _, ok := proc.Nodes[succ]; !ok {
					return fmt.Errorf("netsec: proc %s node %d: successor %d missing", proc.Name, n.ID, succ)
				}
			}
		}
	}
	return nil
}
