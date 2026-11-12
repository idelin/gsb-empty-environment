// Package provenance implements a provenance algebra for a Go data-security
// host. Every derived tuple carries an exact dependency expression over
// source record IDs, so a privacy deletion request can be propagated to all
// derived objects that retain the deleted record's contribution.
package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Op is the node kind of a provenance expression.
type Op int

const (
	OpZero Op = iota // absorbing element: no contribution
	OpVar            // a single source record ID
	OpSum            // alternative contributions (union / aggregation)
	OpProd           // joint contribution (join)
)

// Expr is a hash-consed provenance expression node. Structurally identical
// expressions share a single node (compression), but repeated arguments are
// retained, so duplicate contributions are never lost: Sum(x, x) != x.
type Expr struct {
	id   uint64
	op   Op
	src  string
	args []*Expr // sorted by id, duplicates preserved
}

// Op returns the node kind.
func (e *Expr) Op() Op { return e.op }

// Source returns the source record ID for OpVar nodes.
func (e *Expr) Source() string { return e.src }

// Args returns the child nodes (duplicates preserved).
func (e *Expr) Args() []*Expr { return e.args }

// Store interns provenance expressions so shared subexpressions are stored
// exactly once. It is safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	nodes map[string]*Expr
	zero  *Expr
	next  uint64
}

// NewStore returns an empty expression store.
func NewStore() *Store {
	return &Store{nodes: make(map[string]*Expr), zero: &Expr{op: OpZero}}
}

// Zero returns the absorbing "no contribution" expression.
func (s *Store) Zero() *Expr { return s.zero }

// Size reports the number of interned nodes, a measure of compression.
func (s *Store) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.nodes)
}

func (s *Store) intern(key string, build func(id uint64) *Expr) *Expr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.nodes[key]; ok {
		return e
	}
	s.next++
	e := build(s.next)
	s.nodes[key] = e
	return e
}

// Var returns the expression denoting a single contribution of source src.
func (s *Store) Var(src string) *Expr {
	return s.intern("v:"+src, func(id uint64) *Expr {
		return &Expr{id: id, op: OpVar, src: src}
	})
}

func keyOf(op string, args []*Expr) string {
	var b strings.Builder
	b.WriteString(op)
	for _, a := range args {
		b.WriteString(strconv.FormatUint(a.id, 36))
		b.WriteByte(',')
	}
	return b.String()
}

func sortArgs(args []*Expr) {
	sort.Slice(args, func(i, j int) bool { return args[i].id < args[j].id })
}

// Sum builds the union of contributions. Nested sums are flattened and
// identical subexpressions are shared, but multiplicities are kept:
// Sum(x, x) denotes two contributions of x and is distinct from x.
func (s *Store) Sum(args ...*Expr) *Expr {
	flat := make([]*Expr, 0, len(args))
	for _, a := range args {
		if a == nil || a.op == OpZero {
			continue
		}
		if a.op == OpSum {
			flat = append(flat, a.args...)
		} else {
			flat = append(flat, a)
		}
	}
	switch len(flat) {
	case 0:
		return s.zero
	case 1:
		return flat[0]
	}
	sortArgs(flat)
	return s.intern(keyOf("s:", flat), func(id uint64) *Expr {
		return &Expr{id: id, op: OpSum, args: flat}
	})
}

// Prod builds the joint contribution of its arguments (a join match).
// Zero is absorbing: a product with a vanished factor vanishes.
func (s *Store) Prod(args ...*Expr) *Expr {
	flat := make([]*Expr, 0, len(args))
	for _, a := range args {
		if a == nil || a.op == OpZero {
			return s.zero
		}
		if a.op == OpProd {
			flat = append(flat, a.args...)
		} else {
			flat = append(flat, a)
		}
	}
	switch len(flat) {
	case 0:
		return s.zero
	case 1:
		return flat[0]
	}
	sortArgs(flat)
	return s.intern(keyOf("p:", flat), func(id uint64) *Expr {
		return &Expr{id: id, op: OpProd, args: flat}
	})
}

// String renders the canonical form of the expression. Shared subexpressions
// are expanded textually; the store keeps them compressed in memory.
func (e *Expr) String() string {
	switch e.op {
	case OpZero:
		return "0"
	case OpVar:
		return e.src
	}
	parts := make([]string, len(e.args))
	for i, a := range e.args {
		parts[i] = a.String()
	}
	if e.op == OpSum {
		return "(+ " + strings.Join(parts, " ") + ")"
	}
	return "(* " + strings.Join(parts, " ") + ")"
}

// Sources returns the exact, deduplicated, sorted set of source record IDs
// the expression depends on.
func (e *Expr) Sources() []string {
	set := make(map[string]struct{})
	var walk func(x *Expr)
	walk = func(x *Expr) {
		if x.op == OpVar {
			set[x.src] = struct{}{}
			return
		}
		for _, a := range x.args {
			walk(a)
		}
	}
	walk(e)
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DependsOn reports whether the expression references any source in deleted.
func DependsOn(e *Expr, deleted map[string]bool) bool {
	memo := make(map[*Expr]bool)
	var walk func(x *Expr) bool
	walk = func(x *Expr) bool {
		if b, ok := memo[x]; ok {
			return b
		}
		var r bool
		if x.op == OpVar {
			r = deleted[x.src]
		} else {
			for _, a := range x.args {
				if walk(a) {
					r = true
					break
				}
			}
		}
		memo[x] = r
		return r
	}
	return walk(e)
}

// Count evaluates the expression as the number of contribution paths:
// Var = 1, Sum adds, Prod multiplies. Duplicate arguments therefore count
// multiple times, which is what "not losing duplicate contributions" means.
func Count(e *Expr) uint64 {
	memo := make(map[*Expr]uint64)
	var walk func(x *Expr) uint64
	walk = func(x *Expr) uint64 {
		if v, ok := memo[x]; ok {
			return v
		}
		var r uint64
		switch x.op {
		case OpVar:
			r = 1
		case OpSum:
			for _, a := range x.args {
				r += walk(a)
			}
		case OpProd:
			r = 1
			for _, a := range x.args {
				r *= walk(a)
			}
		}
		memo[x] = r
		return r
	}
	return walk(e)
}

// Digest returns a content hash of the canonical expression, used in
// unaffected proofs.
func Digest(e *Expr) string {
	sum := sha256.Sum256([]byte(e.String()))
	return hex.EncodeToString(sum[:])
}
