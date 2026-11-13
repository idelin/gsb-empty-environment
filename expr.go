package netsec

import "fmt"

const (
	// MaxVictimLoads is the maximum number of loads a victim program may
	// perform in one experiment.
	MaxVictimLoads = 16
	// NumSecrets is the number of possible four-bit secrets.
	NumSecrets = 16
	// MaxExprDepth bounds expression evaluation to reject cyclic or
	// pathologically deep expression trees.
	MaxExprDepth = 4096
)

// ExprKind identifies the operation at an expression node.
type ExprKind uint8

const (
	// ExprConst is a constant in [0,AddressSpace).
	ExprConst ExprKind = iota
	// ExprPublic reads the public experiment input.
	ExprPublic
	// ExprSecret reads the four-bit secret.
	ExprSecret
	// ExprAdd adds two operands modulo AddressSpace.
	ExprAdd
	// ExprXor XORs two operands. Both operands are already in
	// [0,AddressSpace), so the result is too.
	ExprXor
	// ExprLookup reads Table at the index produced by Left. The index must be
	// in [0,len(Table)) and every table value must be in [0,AddressSpace).
	ExprLookup
)

func (k ExprKind) String() string {
	switch k {
	case ExprConst:
		return "const"
	case ExprPublic:
		return "public"
	case ExprSecret:
		return "secret"
	case ExprAdd:
		return "add"
	case ExprXor:
		return "xor"
	case ExprLookup:
		return "lookup"
	default:
		return fmt.Sprintf("exprkind(%d)", uint8(k))
	}
}

// Expr is a secret/public-dependent victim address expression.
//
// Leaves have Kind ExprConst, ExprPublic, or ExprSecret. ExprAdd and ExprXor
// use Left and Right. ExprLookup uses Left as the index and Table for the
// explicit index-to-value map.
type Expr struct {
	Kind  ExprKind
	Value int
	Left  *Expr
	Right *Expr
	Table []int
}

// ConstExpr returns a constant expression.
func ConstExpr(value int) *Expr {
	return &Expr{Kind: ExprConst, Value: value}
}

// PublicExpr returns an expression that reads the public input.
func PublicExpr() *Expr {
	return &Expr{Kind: ExprPublic}
}

// SecretExpr returns an expression that reads the four-bit secret.
func SecretExpr() *Expr {
	return &Expr{Kind: ExprSecret}
}

// AddExpr returns a mod-4096 addition expression.
func AddExpr(left, right *Expr) *Expr {
	return &Expr{Kind: ExprAdd, Left: left, Right: right}
}

// XorExpr returns a bitwise-XOR expression.
func XorExpr(left, right *Expr) *Expr {
	return &Expr{Kind: ExprXor, Left: left, Right: right}
}

// LookupExpr returns a table lookup. table must be non-empty and all of its
// values must be in [0,AddressSpace).
func LookupExpr(index *Expr, table []int) *Expr {
	tableCopy := append([]int(nil), table...)
	return &Expr{Kind: ExprLookup, Left: index, Table: tableCopy}
}

// Validate checks the expression tree without evaluating a public value or
// secret. Dynamic lookup indices are checked by Eval.
func (e *Expr) Validate() error {
	return e.validate(0)
}

func (e *Expr) validate(depth int) error {
	if e == nil {
		return fmt.Errorf("netsec: nil expression")
	}
	if depth > MaxExprDepth {
		return fmt.Errorf("netsec: expression exceeds maximum depth %d", MaxExprDepth)
	}

	switch e.Kind {
	case ExprConst:
		if e.Left != nil || e.Right != nil {
			return fmt.Errorf("netsec: const expression has child nodes")
		}
		if e.Value < 0 || e.Value >= AddressSpace {
			return fmt.Errorf("netsec: constant %d outside [0,%d)", e.Value, AddressSpace)
		}
	case ExprPublic, ExprSecret:
		if e.Left != nil || e.Right != nil {
			return fmt.Errorf("netsec: %s expression has child nodes", e.Kind)
		}
	case ExprAdd, ExprXor:
		if e.Left == nil || e.Right == nil {
			return fmt.Errorf("netsec: %s expression needs two operands", e.Kind)
		}
		if err := e.Left.validate(depth + 1); err != nil {
			return err
		}
		if err := e.Right.validate(depth + 1); err != nil {
			return err
		}
	case ExprLookup:
		if e.Left == nil {
			return fmt.Errorf("netsec: lookup expression needs an index")
		}
		if len(e.Table) == 0 {
			return fmt.Errorf("netsec: lookup table is empty")
		}
		for i, value := range e.Table {
			if value < 0 || value >= AddressSpace {
				return fmt.Errorf("netsec: lookup table[%d]=%d outside [0,%d)", i, value, AddressSpace)
			}
		}
		if err := e.Left.validate(depth + 1); err != nil {
			return err
		}
	default:
		return fmt.Errorf("netsec: unknown expression kind %d", uint8(e.Kind))
	}
	return nil
}

// Eval computes the expression for a public input and a four-bit secret.
func (e *Expr) Eval(public, secret int) (int, error) {
	if public < 0 || public >= AddressSpace {
		return 0, fmt.Errorf("netsec: public value %d outside [0,%d)", public, AddressSpace)
	}
	if secret < 0 || secret >= NumSecrets {
		return 0, fmt.Errorf("netsec: secret %d outside [0,%d)", secret, NumSecrets)
	}
	return e.eval(public, secret, 0)
}

func (e *Expr) eval(public, secret, depth int) (int, error) {
	if e == nil {
		return 0, fmt.Errorf("netsec: nil expression")
	}
	if depth > MaxExprDepth {
		return 0, fmt.Errorf("netsec: expression exceeds maximum depth %d", MaxExprDepth)
	}

	switch e.Kind {
	case ExprConst:
		if e.Value < 0 || e.Value >= AddressSpace {
			return 0, fmt.Errorf("netsec: constant %d outside [0,%d)", e.Value, AddressSpace)
		}
		return e.Value, nil
	case ExprPublic:
		return public, nil
	case ExprSecret:
		return secret, nil
	case ExprAdd, ExprXor:
		left, err := e.Left.eval(public, secret, depth+1)
		if err != nil {
			return 0, err
		}
		right, err := e.Right.eval(public, secret, depth+1)
		if err != nil {
			return 0, err
		}
		if e.Kind == ExprAdd {
			return (left + right) % AddressSpace, nil
		}
		return left ^ right, nil
	case ExprLookup:
		index, err := e.Left.eval(public, secret, depth+1)
		if err != nil {
			return 0, err
		}
		if len(e.Table) == 0 {
			return 0, fmt.Errorf("netsec: lookup table is empty")
		}
		if index < 0 || index >= len(e.Table) {
			return 0, fmt.Errorf("netsec: lookup index %d outside [0,%d)", index, len(e.Table))
		}
		value := e.Table[index]
		if value < 0 || value >= AddressSpace {
			return 0, fmt.Errorf("netsec: lookup table[%d]=%d outside [0,%d)", index, value, AddressSpace)
		}
		return value, nil
	default:
		return 0, fmt.Errorf("netsec: unknown expression kind %d", uint8(e.Kind))
	}
}

// Program is a victim with at most MaxVictimLoads address expressions.
type Program struct {
	Loads []*Expr
}

// VictimProgram is an alias for Program.
type VictimProgram = Program

// NewProgram validates and copies a victim load program.
func NewProgram(loads ...*Expr) (*Program, error) {
	if len(loads) > MaxVictimLoads {
		return nil, fmt.Errorf("netsec: victim has %d loads, maximum is %d", len(loads), MaxVictimLoads)
	}
	program := &Program{Loads: append([]*Expr(nil), loads...)}
	if err := program.Validate(); err != nil {
		return nil, err
	}
	return program, nil
}

// Validate checks the load count and every expression.
func (p *Program) Validate() error {
	if p == nil {
		return fmt.Errorf("netsec: nil victim program")
	}
	if len(p.Loads) > MaxVictimLoads {
		return fmt.Errorf("netsec: victim has %d loads, maximum is %d", len(p.Loads), MaxVictimLoads)
	}
	for i, load := range p.Loads {
		if load == nil {
			return fmt.Errorf("netsec: victim load %d is nil", i)
		}
		if err := load.Validate(); err != nil {
			return fmt.Errorf("netsec: victim load %d: %w", i, err)
		}
	}
	return nil
}

// LoadAddresses evaluates all victim loads for one public value and secret.
func (p *Program) LoadAddresses(public, secret int) ([]int, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	addresses := make([]int, len(p.Loads))
	for i, load := range p.Loads {
		address, err := load.Eval(public, secret)
		if err != nil {
			return nil, fmt.Errorf("netsec: victim load %d: %w", i, err)
		}
		if err := ValidateAddress(address); err != nil {
			return nil, fmt.Errorf("netsec: victim load %d: %w", i, err)
		}
		addresses[i] = address
	}
	return addresses, nil
}

// Victim supplies the addresses of up to MaxVictimLoads victim loads for a
// public input and four-bit secret.
type Victim interface {
	LoadAddresses(public, secret int) ([]int, error)
}

// VictimFunc adapts a function to the Victim interface.
type VictimFunc func(public, secret int) ([]int, error)

// LoadAddresses calls f(public, secret).
func (f VictimFunc) LoadAddresses(public, secret int) ([]int, error) {
	if f == nil {
		return nil, fmt.Errorf("netsec: nil victim function")
	}
	return f(public, secret)
}
