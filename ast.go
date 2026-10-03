package decor

// Expr is any scalar or boolean expression node.
type Expr interface{ exprNode() }

// ColumnRef references a column, optionally qualified by a table alias.
type ColumnRef struct {
	Table string
	Name  string
}

// LitKind distinguishes literal types.
type LitKind int

const (
	LitNumber LitKind = iota
	LitString
)

// Literal is a constant value.
type Literal struct {
	Kind  LitKind
	Value string
}

// BinaryExpr is a binary operation: comparison (=, <, >, <=, >=, <>, !=)
// or boolean connective (AND, OR).
type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
}

// NotExpr is a boolean negation.
type NotExpr struct {
	X Expr
}

// ExistsExpr is EXISTS (subquery).
type ExistsExpr struct {
	Subquery *SelectStmt
}

// InExpr is x IN (subquery) or x IN (literal list).
type InExpr struct {
	Left     Expr
	Subquery *SelectStmt // nil when List is used
	List     []Expr
	Negated  bool
}

func (*ColumnRef) exprNode()  {}
func (*Literal) exprNode()    {}
func (*BinaryExpr) exprNode() {}
func (*NotExpr) exprNode()    {}
func (*ExistsExpr) exprNode() {}
func (*InExpr) exprNode()     {}

// SelectItem is one entry of the SELECT list.
type SelectItem struct {
	Star      bool   // SELECT *
	TableStar string // SELECT t.*  (table alias when non-empty)
	Expr      Expr
	Alias     string
}

// TableRef is a table in the FROM clause with an optional alias.
type TableRef struct {
	Name  string
	Alias string
}

// EffectiveName returns the alias if present, otherwise the table name.
func (t TableRef) EffectiveName() string {
	if t.Alias != "" {
		return t.Alias
	}
	return t.Name
}

// SelectStmt is a single SELECT ... FROM ... [WHERE ...] [LIMIT n] block.
type SelectStmt struct {
	Distinct bool
	Items    []SelectItem
	From     []TableRef
	Where    Expr
	Limit    *int
}
