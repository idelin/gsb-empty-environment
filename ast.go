package relalg

// SelectStmt 是 SQL 查询核心 AST 的根节点：
// SELECT ... FROM t [alias] [JOIN ... ON ...]* [WHERE ...]
type SelectStmt struct {
	Projection []Expr
	From       *TableSource
	Where      Expr
}

// TableSource 表示 FROM 子句中的主表及其级联 JOIN。
type TableSource struct {
	Name  string
	Alias string
	Joins []*JoinClause
}

// Ref 返回表引用名（别名优先，否则为表名）。
func (t *TableSource) Ref() string {
	if t.Alias != "" {
		return t.Alias
	}
	return t.Name
}

// JoinClause 表示一条 JOIN ... ON ... 子句。
type JoinClause struct {
	Kind  string // INNER / LEFT / RIGHT
	Table *TableSource
	On    Expr
}
