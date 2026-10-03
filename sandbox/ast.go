package sandbox

// Node 表达式 AST 节点。
type Node interface{ node() }

type NumberLit struct{ Value float64 }
type StringLit struct{ Value string }
type BoolLit struct{ Value bool }
type Ident struct{ Name string }

type UnaryExpr struct {
	Op string // "-" 或 "!"
	X  Node
}

type BinaryExpr struct {
	Op   string
	L, R Node
}

type CallExpr struct {
	Name string
	Args []Node
}

func (*NumberLit) node()  {}
func (*StringLit) node()  {}
func (*BoolLit) node()    {}
func (*Ident) node()      {}
func (*UnaryExpr) node()  {}
func (*BinaryExpr) node() {}
func (*CallExpr) node()   {}
