package mini

import "fmt"

// Node is an AST node. Each node defines its semantic (attribute) rules via
// the rules method; Children drives the rule-collection walk.
type Node interface {
	Pos() Pos
	Children() []Node
	String() string
	rules(c *compiler)
}

type node struct{ pos Pos }

func (n node) Pos() Pos { return n.pos }

// --- statements ---

type Program struct {
	node
	Stmts []Stmt
}

type Stmt interface{ Node }

type VarDecl struct {
	node
	Name     string
	HasType  bool
	DeclType Type
	Init     Expr
}

type ExprStmt struct {
	node
	X Expr
}

type PrintStmt struct {
	node
	X Expr
}

// --- expressions ---

type Expr interface{ Node }

type Literal struct {
	node
	Kind Type
	Text string
}

type Ident struct {
	node
	Name string
}

type Unary struct {
	node
	Op string
	X  Expr
}

type Binary struct {
	node
	Op string
	L  Expr
	R  Expr
}

func (p *Program) Children() []Node {
	ch := make([]Node, len(p.Stmts))
	for i, s := range p.Stmts {
		ch[i] = s
	}
	return ch
}
func (d *VarDecl) Children() []Node   { return []Node{d.Init} }
func (s *ExprStmt) Children() []Node  { return []Node{s.X} }
func (s *PrintStmt) Children() []Node { return []Node{s.X} }
func (l *Literal) Children() []Node   { return nil }
func (i *Ident) Children() []Node     { return nil }
func (u *Unary) Children() []Node     { return []Node{u.X} }
func (b *Binary) Children() []Node    { return []Node{b.L, b.R} }

func (p *Program) String() string   { return fmt.Sprintf("Program@%s", p.pos) }
func (d *VarDecl) String() string   { return fmt.Sprintf("VarDecl(%s)@%s", d.Name, d.pos) }
func (s *ExprStmt) String() string  { return fmt.Sprintf("ExprStmt@%s", s.pos) }
func (s *PrintStmt) String() string { return fmt.Sprintf("PrintStmt@%s", s.pos) }
func (l *Literal) String() string   { return fmt.Sprintf("Literal(%s)@%s", l.Text, l.pos) }
func (i *Ident) String() string     { return fmt.Sprintf("Ident(%s)@%s", i.Name, i.pos) }
func (u *Unary) String() string     { return fmt.Sprintf("Unary(%s)@%s", u.Op, u.pos) }
func (b *Binary) String() string    { return fmt.Sprintf("Binary(%s)@%s", b.Op, b.pos) }
