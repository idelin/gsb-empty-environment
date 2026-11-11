// Package sanitsum 为条件清洗库函数推导摘要，供 Go 分析宿主调用。
//
// 输入是无循环函数图（DAG 形式的 CFG）和有限污染类别，输出每个返回值下
// 参数、结果与副作用字段的“必然净化集合”：
//   - 同一返回值的多条路径取交集（不确定分支只保留共同保证）；
//   - 副作用字段单独建模，不与参数/结果混淆；
//   - 另提供完整路径枚举实现，用于与 DAG 摘要互相校验。
//
// 摘要只描述“必然”成立的净化，调用方据此判断污染是否可安全汇入 sink。
package sanitsum

import (
	"fmt"
)

// Category 是有限污染类别之一，例如 SQL、HTML、Shell。
type Category string

// RetVal 标识库函数的一个具体返回值。Go 的多返回值用 "0"、"1" 等索引；
// 布尔守卫函数通常只有 "true" 和 "false"。
type RetVal string

// 约定的布尔返回值标签。
const (
	RetTrue  RetVal = "true"
	RetFalse RetVal = "false"
)

// Ref 是对变量及其可选副作用字段的引用。Field 为空时表示整个变量，
// 非空时表示结构体/对象上的一个副作用字段（例如 u.Email）。
type Ref struct {
	Var   string
	Field string // 仅描述字段名；与 Var 组合定位副作用槽位
}

// String 返回 Var.Field 或 Var 形式的可读表示。
func (r Ref) String() string {
	if r.Field == "" {
		return r.Var
	}
	return r.Var + "." + r.Field
}

// Stmt 是函数图中的一条语句；具体类型见各 *Stmt 定义。
type Stmt interface {
	stmtTag()
}

// SanitizeStmt 表示对 Target 执行针对指定类别的净化操作。
type SanitizeStmt struct {
	Target Ref
	Cat    Category
}

// AssignStmt 将 RHS 复制到 Dst；常量 RHS 会清空污染，引用 RHS 传递污染。
type AssignStmt struct {
	Dst Ref
	RHS RefOrConst
}

// StoreStmt 把 RHS 写入对象字段，用于显式副作用字段建模。
type StoreStmt struct {
	Dst Ref // 必须带 Field
	RHS RefOrConst
}

// ReturnStmt 终止当前路径，按 RetVal 记录返回点。
type ReturnStmt struct {
	Ret RetVal
}

// BranchStmt 是不携带谓词语义的分支占位语句：具体分支已由 Block.Succs 编码，
// 分析器不对其做数据流动作，仅用于让函数图显式表达不确定分支点。
type BranchStmt struct{}

func (SanitizeStmt) stmtTag() {}
func (AssignStmt) stmtTag()   {}
func (StoreStmt) stmtTag()    {}
func (ReturnStmt) stmtTag()   {}
func (BranchStmt) stmtTag()   {}

// RefOrConst 要么是一个 Ref，要么是一个无污常量。
type RefOrConst struct {
	Ref   Ref
	Const string // 非空表示常量，Ref 字段此时忽略
}

// IsConst 报告该操作数是否为常量。
func (o RefOrConst) IsConst() bool { return o.Const != "" }

// Block 是函数图中的基本块；带返回语句的块必须把 ReturnStmt 放在末尾。
type Block struct {
	ID    string
	Stmts []Stmt
	Succs []string // 后继块 ID；返回块必须为空，普通块至少一个后继
}

// FuncGraph 是无循环的函数控制流图。
type FuncGraph struct {
	Name       string
	Categories []Category // 有限污染类别全集
	Params     []string   // 入参变量名
	Results    []string   // 命名结果变量名，可与 Params 重叠
	Entry      string
	Blocks     []*Block
}

// CatSet 是污染类别集合的紧凑位集表示。同一分析/摘要内所有 CatSet 的
// 类别顺序由 FuncGraph.Categories 决定，因而可以直接按位比较。
// 位集最多支持 64 个类别。
type CatSet struct {
	bits uint64
}

// EmptyCatSet 返回空类别集合。
func EmptyCatSet() CatSet { return CatSet{} }

// Contains 报告类别 c 是否在集合中。
func (s CatSet) Contains(c Category, order []Category) bool {
	for i, k := range order {
		if k == c {
			return s.bits&(1<<uint(i)) != 0
		}
	}
	return false
}

// List 按 order 的顺序枚举集合中的类别。
func (s CatSet) List(order []Category) []Category {
	var out []Category
	for i, c := range order {
		if s.bits&(1<<uint(i)) != 0 {
			out = append(out, c)
		}
	}
	return out
}

// Union 返回两个集合的并集。
func (s CatSet) Union(o CatSet) CatSet { return CatSet{bits: s.bits | o.bits} }

// Intersect 返回两个集合的交集。
func (s CatSet) Intersect(o CatSet) CatSet { return CatSet{bits: s.bits & o.bits} }

// Equals 报告两个位集是否相同。
func (s CatSet) Equals(o CatSet) bool { return s.bits == o.bits }

// IsEmpty 报告集合是否为空。
func (s CatSet) IsEmpty() bool { return s.bits == 0 }

func catBit(order []Category, c Category) (uint64, error) {
	for i, k := range order {
		if k == c {
			if i >= 64 {
				return 0, fmt.Errorf("sanitsum: 类别 %q 超出 64 类上限", c)
			}
			return 1 << uint(i), nil
		}
	}
	return 0, fmt.Errorf("sanitsum: 未知污染类别 %q", c)
}

func allBits(n int) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return 1<<uint(n) - 1
}
