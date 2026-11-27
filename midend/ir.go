// Package midend implements an importable compiler middle-end core:
// a typed SSA-like IR, a context-insensitive interprocedural escape
// analysis with parameter escape summaries and fixpoint iteration, a
// scalar-replacement-of-aggregates (SROA) pass, and a reference
// interpreter that executes the IR over heap/stack/global storage.
package midend

import (
	"fmt"
	"strings"
)

// Type is the type of an SSA value.
type Type interface {
	String() string
	size() int
	Equal(Type) bool
}

// IntType is an integer type of the given bit width.
type IntType struct{ Bits int }

func (t *IntType) String() string { return fmt.Sprintf("i%d", t.Bits) }
func (t *IntType) size() int      { return t.Bits / 8 }
func (t *IntType) Equal(o Type) bool {
	x, ok := o.(*IntType)
	return ok && x.Bits == t.Bits
}

// PtrType points to Elem.
type PtrType struct{ Elem Type }

func (t *PtrType) String() string { return "*" + t.Elem.String() }
func (t *PtrType) size() int      { return ptrSize }
func (t *PtrType) Equal(o Type) bool {
	x, ok := o.(*PtrType)
	return ok && t.Elem.Equal(x.Elem)
}

// StructField is one field of an aggregate type.
type StructField struct {
	Name string
	Ty   Type
}

// StructType is an aggregate laid out without padding.
type StructType struct {
	Name   string
	Fields []StructField
	off    []int
}

func (t *StructType) String() string {
	parts := make([]string, len(t.Fields))
	for i, f := range t.Fields {
		parts[i] = fmt.Sprintf("%s:%s", f.Name, f.Ty)
	}
	return "struct{" + strings.Join(parts, ", ") + "}"
}

func (t *StructType) size() int {
	if len(t.off) == 0 {
		t.layout()
	}
	last := len(t.off) - 1
	return t.off[last] + t.Fields[last].Ty.size()
}

func (t *StructType) layout() {
	t.off = make([]int, len(t.Fields))
	off := 0
	for i, f := range t.Fields {
		t.off[i] = off
		off += f.Ty.size()
	}
}

// Equal reports structural equality of two struct types.
func (t *StructType) Equal(o Type) bool {
	x, ok := o.(*StructType)
	if !ok || len(t.Fields) != len(x.Fields) {
		return false
	}
	for i := range t.Fields {
		if t.Fields[i].Name != x.Fields[i].Name || !t.Fields[i].Ty.Equal(x.Fields[i].Ty) {
			return false
		}
	}
	return true
}

// Offset returns the byte offset of field i.
func (t *StructType) Offset(i int) int {
	if len(t.off) == 0 {
		t.layout()
	}
	return t.off[i]
}

// FieldIndex maps a field name to its index.
func (t *StructType) FieldIndex(name string) int {
	for i, f := range t.Fields {
		if f.Name == name {
			return i
		}
	}
	panic("midend: unknown field " + name + " in " + t.Name)
}

// Convenience type constructors.
var (
	I64 = &IntType{Bits: 64}
	I32 = &IntType{Bits: 32}
)

// PtrT builds a pointer type.
func PtrT(elem Type) *PtrType { return &PtrType{Elem: elem} }

// StructT builds a struct type.
func StructT(name string, fields ...StructField) *StructType {
	return &StructType{Name: name, Fields: fields}
}

// F builds a struct field.
func F(name string, ty Type) StructField { return StructField{Name: name, Ty: ty} }

type opKind int

const (
	opReg opKind = iota
	opConst
)

// Operand is either a virtual register reference (Reg) or a constant.
type Operand struct {
	Kind opKind
	Reg  string
	Int  int64
	Ty   Type
}

// R builds a register operand.
func R(name string) Operand { return Operand{Kind: opReg, Reg: name, Ty: nil} }

// C builds an integer constant operand.
func C(v int64, ty Type) Operand { return Operand{Kind: opConst, Int: v, Ty: ty} }

// Nil builds the null pointer constant with the given pointer type.
func Nil(ty *PtrType) Operand { return Operand{Kind: opConst, Int: 0, Ty: ty} }

// IsReg reports whether the operand names a virtual register.
func (o Operand) IsReg() bool { return o.Kind == opReg }

// IsConst reports whether the operand is a constant.
func (o Operand) IsConst() bool { return o.Kind == opConst }

func (o Operand) String() string {
	if o.Kind == opConst {
		return fmt.Sprintf("%d", o.Int)
	}
	return "%" + o.Reg
}

// PhiEdge connects a predecessor block label to an incoming value.
type PhiEdge struct {
	Block string
	Val   Operand
}

// Inst is a non-terminator instruction producing (or performing) a value.
type Inst interface {
	GetName() string
	GetType() Type
	String() string
}

// Alloc allocates an aggregate object on the heap (Heap==true) or stack.
type Alloc struct {
	Name string
	Heap bool
	Ty   *StructType
}

func (a *Alloc) GetName() string { return a.Name }
func (a *Alloc) GetType() Type   { return PtrT(a.Ty) }
func (a *Alloc) String() string {
	where := "stack"
	if a.Heap {
		where = "heap"
	}
	return fmt.Sprintf("%%%s = alloc %s %s", a.Name, where, a.Ty)
}

// GlobalAddr takes the address of a global object.
type GlobalAddr struct {
	Name string
	G    *Global
}

func (g *GlobalAddr) GetName() string { return g.Name }
func (g *GlobalAddr) GetType() Type   { return PtrT(g.G.Ty) }
func (g *GlobalAddr) String() string {
	return fmt.Sprintf("%%%s = globaladdr @%s", g.Name, g.G.Name)
}

// FieldAddr computes the address of a field at a static byte offset.
type FieldAddr struct {
	Name  string
	Ty    Type
	Ptr   Operand
	Field int
}

func (f *FieldAddr) GetName() string { return f.Name }
func (f *FieldAddr) GetType() Type   { return f.Ty }
func (f *FieldAddr) String() string {
	return fmt.Sprintf("%%%s = field %s, %d", f.Name, f.Ptr, f.Field)
}

// AddrAdd computes Ptr + dynamic offset Off (in bytes).
type AddrAdd struct {
	Name string
	Ptr  Operand
	Off  Operand
	Ty   Type
}

func (a *AddrAdd) GetName() string { return a.Name }
func (a *AddrAdd) GetType() Type   { return a.Ty }
func (a *AddrAdd) String() string {
	return fmt.Sprintf("%%%s = addradd %s, %s", a.Name, a.Ptr, a.Off)
}

// Load reads through Ptr.
type Load struct {
	Name string
	Ty   Type
	Ptr  Operand
}

func (l *Load) GetName() string { return l.Name }
func (l *Load) GetType() Type   { return l.Ty }
func (l *Load) String() string {
	return fmt.Sprintf("%%%s = load %s", l.Name, l.Ptr)
}

// Store writes Val through Ptr.
type Store struct {
	Ptr Operand
	Val Operand
}

func (s *Store) GetName() string { return "" }
func (s *Store) GetType() Type   { return nil }
func (s *Store) String() string {
	return fmt.Sprintf("store %s -> %s", s.Val, s.Ptr)
}

// Phi is an SSA phi node.
type Phi struct {
	Name     string
	Ty       Type
	Incoming []PhiEdge
}

func (p *Phi) GetName() string { return p.Name }
func (p *Phi) GetType() Type   { return p.Ty }
func (p *Phi) String() string {
	parts := make([]string, len(p.Incoming))
	for i, e := range p.Incoming {
		parts[i] = fmt.Sprintf("[%s, %s]", e.Val, e.Block)
	}
	return fmt.Sprintf("%%%s = phi %s %s", p.Name, p.Ty, strings.Join(parts, ", "))
}

// Call invokes a known internal function by name.
type Call struct {
	Name   string
	Ty     Type
	Callee string
	Args   []Operand
}

func (c *Call) GetName() string { return c.Name }
func (c *Call) GetType() Type   { return c.Ty }
func (c *Call) String() string {
	return fmt.Sprintf("%%%s = call @%s(%s)", c.Name, c.Callee, joinOps(c.Args))
}

// ExtCall invokes an opaque external function: every pointer argument is
// assumed to escape to an unknown caller.
type ExtCall struct {
	Name   string
	Ty     Type
	Callee string
	Args   []Operand
}

func (c *ExtCall) GetName() string { return c.Name }
func (c *ExtCall) GetType() Type   { return c.Ty }
func (c *ExtCall) String() string {
	return fmt.Sprintf("%%%s = extcall @%s(%s)", c.Name, c.Callee, joinOps(c.Args))
}

// BinOp evaluates an integer binary operation.
type BinOp struct {
	Name string
	Ty   Type
	Op   string
	X    Operand
	Y    Operand
}

func (b *BinOp) GetName() string { return b.Name }
func (b *BinOp) GetType() Type   { return b.Ty }
func (b *BinOp) String() string {
	return fmt.Sprintf("%%%s = %s %s, %s", b.Name, b.Op, b.X, b.Y)
}

// PrintInst emits an integer to the observable output stream.
type PrintInst struct {
	Val Operand
}

func (p *PrintInst) GetName() string { return "" }
func (p *PrintInst) GetType() Type   { return nil }
func (p *PrintInst) String() string  { return fmt.Sprintf("print %s", p.Val) }

func joinOps(ops []Operand) string {
	parts := make([]string, len(ops))
	for i, o := range ops {
		parts[i] = o.String()
	}
	return strings.Join(parts, ", ")
}

// Term terminates a basic block.
type Term interface {
	term()
	String() string
}

// Jump transfers control unconditionally.
type Jump struct{ To string }

func (j *Jump) term()          {}
func (j *Jump) String() string { return "jump " + j.To }

// Branch transfers control conditionally.
type Branch struct {
	Cond Operand
	T    string
	F    string
}

func (b *Branch) term()          {}
func (b *Branch) String() string { return fmt.Sprintf("br %s, %s, %s", b.Cond, b.T, b.F) }

// Return yields a value back to the caller.
type Return struct{ Val Operand }

func (r *Return) term()          {}
func (r *Return) String() string { return "ret " + r.Val.String() }

// Param is a function parameter.
type Param struct {
	Name string
	Ty   Type
}

func (p *Param) String() string { return fmt.Sprintf("%%%s %s", p.Name, p.Ty) }

// Block is a basic block.
type Block struct {
	Name  string
	Insts []Inst
	Term  Term
}

// Func is an SSA function.
type Func struct {
	Name   string
	Params []*Param
	Ret    Type
	Blocks []*Block
}

// Global is a module-level aggregate object.
type Global struct {
	Name string
	Ty   *StructType
}

// Program is a whole module.
type Program struct {
	Globals []*Global
	Funcs   []*Func
	Entry   string

	global map[string]*Global
	fn     map[string]*Func
	block  map[*Func]map[string]*Block
}

// NewProgram creates an empty program.
func NewProgram() *Program {
	return &Program{
		global: map[string]*Global{},
		fn:     map[string]*Func{},
		block:  map[*Func]map[string]*Block{},
	}
}

// AddGlobal registers a global object.
func (p *Program) AddGlobal(g *Global) *Global {
	if _, dup := p.global[g.Name]; dup {
		panic("midend: duplicate global " + g.Name)
	}
	p.Globals = append(p.Globals, g)
	p.global[g.Name] = g
	return g
}

// AddFunc registers a function.
func (p *Program) AddFunc(f *Func) *Func {
	if _, dup := p.fn[f.Name]; dup {
		panic("midend: duplicate function " + f.Name)
	}
	p.Funcs = append(p.Funcs, f)
	p.fn[f.Name] = f
	return f
}

// Func looks up a function by name.
func (p *Program) Func(name string) *Func { return p.fn[name] }

// Global looks up a global by name.
func (p *Program) Global(name string) *Global { return p.global[name] }

// Block looks up a block in a function.
func (p *Program) Block(f *Func, name string) *Block {
	m := p.block[f]
	if m == nil {
		m = map[string]*Block{}
		for _, b := range f.Blocks {
			m[b.Name] = b
		}
		p.block[f] = m
	}
	return m[name]
}

// E builds a phi edge.
func E(block string, val Operand) PhiEdge { return PhiEdge{Block: block, Val: val} }
