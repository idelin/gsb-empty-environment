package queryid

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// MaxAtoms 限制一次规范化涉及的基本集合数量。
// 规范化使用 2^n 位真值表，n 为原子数。
const MaxAtoms = 16

// SetKind 标识集合表达式节点种类。
type SetKind uint8

const (
	EmptySet SetKind = iota // ∅，空集
	FullSet                 // U，全集
	BaseSet                 // 命名基本集合
	UnionSet                // 并
	InterSet                // 交
	DiffSet                 // 差
	CompSet                 // 补（相对全集 U）
)

// SetExpr 是有限集合表达式。请用 Base/Union/Inter/Diff/Comp 等
// 构造函数或 ParseSetExpr 创建；零值不是合法表达式。
type SetExpr struct {
	Kind SetKind
	Name string   // BaseSet 的名称
	L, R *SetExpr // 二元运算的操作数
	X    *SetExpr // CompSet 的操作数
}

// Empty 返回空集表达式 ∅。
func Empty() *SetExpr { return &SetExpr{Kind: EmptySet} }

// Full 返回全集表达式 U。
func Full() *SetExpr { return &SetExpr{Kind: FullSet} }

// Base 返回命名基本集合。
func Base(name string) *SetExpr { return &SetExpr{Kind: BaseSet, Name: name} }

// Union 返回 a ∪ b。
func Union(a, b *SetExpr) *SetExpr { return &SetExpr{Kind: UnionSet, L: a, R: b} }

// Inter 返回 a ∩ b。
func Inter(a, b *SetExpr) *SetExpr { return &SetExpr{Kind: InterSet, L: a, R: b} }

// Diff 返回 a ∖ b。
func Diff(a, b *SetExpr) *SetExpr { return &SetExpr{Kind: DiffSet, L: a, R: b} }

// Comp 返回 a 相对全集的补集。
func Comp(a *SetExpr) *SetExpr { return &SetExpr{Kind: CompSet, X: a} }

func (e *SetExpr) validate() error {
	if e == nil {
		return fmt.Errorf("queryid: nil set expression")
	}
	switch e.Kind {
	case EmptySet, FullSet:
		return nil
	case BaseSet:
		if e.Name == "" {
			return fmt.Errorf("queryid: base set with empty name")
		}
		return nil
	case UnionSet, InterSet, DiffSet:
		if err := e.L.validate(); err != nil {
			return err
		}
		return e.R.validate()
	case CompSet:
		return e.X.validate()
	default:
		return fmt.Errorf("queryid: unknown set kind %d", e.Kind)
	}
}

func (e *SetExpr) collectAtoms(into map[string]struct{}) {
	switch e.Kind {
	case BaseSet:
		into[e.Name] = struct{}{}
	case UnionSet, InterSet, DiffSet:
		e.L.collectAtoms(into)
		e.R.collectAtoms(into)
	case CompSet:
		e.X.collectAtoms(into)
	}
}

// Eval 在给定基本集合成员判定下对表达式求值，
// 供宿主在具体全集上枚举元素时使用。
func (e *SetExpr) Eval(member func(name string) bool) (bool, error) {
	if err := e.validate(); err != nil {
		return false, err
	}
	return e.evalWith(member), nil
}

func (e *SetExpr) evalWith(member func(name string) bool) bool {
	switch e.Kind {
	case EmptySet:
		return false
	case FullSet:
		return true
	case BaseSet:
		return member(e.Name)
	case UnionSet:
		return e.L.evalWith(member) || e.R.evalWith(member)
	case InterSet:
		return e.L.evalWith(member) && e.R.evalWith(member)
	case DiffSet:
		return e.L.evalWith(member) && !e.R.evalWith(member)
	case CompSet:
		return !e.X.evalWith(member)
	}
	return false
}

// CanonSet 是集合表达式的规范化形式：按字典序排列的有效原子
// （删去不影响结果的原子）加上其上的真值表。两个表达式语义
// 等价——即对基本集合的一切解释都指向同一个集合——当且仅当
// 它们的 CanonSet 相同。
type CanonSet struct {
	Atoms []string // 有效原子，字典序
	Table []uint64 // 2^len(Atoms) 位真值表，第 m 位对应赋值 m
}

// Canonical 把表达式规范化为 CanonSet。
//
// 先在全部原子（不超过 MaxAtoms）上枚举真值表，再删去翻转后
// 不改变结果的原子并重建真值表，因此 a 与 a∪(a∩b) 等吸收形式
// 得到同一个键，而 a 与 a∪b 的键不同——扩大范围必然换键。
func (e *SetExpr) Canonical() (CanonSet, error) {
	if err := e.validate(); err != nil {
		return CanonSet{}, err
	}
	set := map[string]struct{}{}
	e.collectAtoms(set)
	atoms := make([]string, 0, len(set))
	for a := range set {
		atoms = append(atoms, a)
	}
	sort.Strings(atoms)
	if len(atoms) > MaxAtoms {
		return CanonSet{}, fmt.Errorf("queryid: %d base sets exceed MaxAtoms=%d", len(atoms), MaxAtoms)
	}
	table := truthTable(e, atoms)
	essential := make([]string, 0, len(atoms))
	for i, a := range atoms {
		if affectsOutput(table, i, len(atoms)) {
			essential = append(essential, a)
		}
	}
	if len(essential) != len(atoms) {
		table = truthTable(e, essential)
	}
	return CanonSet{Atoms: essential, Table: table}, nil
}

// truthTable 在 atoms（位 i 对应 atoms[i] 的成员身份）上枚举表达式。
func truthTable(e *SetExpr, atoms []string) []uint64 {
	rows := 1 << uint(len(atoms))
	table := make([]uint64, (rows+63)/64)
	index := make(map[string]uint, len(atoms))
	for i, a := range atoms {
		index[a] = uint(i)
	}
	for m := 0; m < rows; m++ {
		mask := uint(m)
		v := e.evalWith(func(name string) bool {
			i, ok := index[name]
			return ok && mask>>i&1 == 1
		})
		if v {
			table[uint(m)>>6] |= 1 << (uint(m) & 63)
		}
	}
	return table
}

// affectsOutput 报告 n 个原子中第 atom 个是否影响真值表输出。
func affectsOutput(table []uint64, atom, n int) bool {
	rows := 1 << uint(n)
	bit := uint(atom)
	for m := 0; m < rows; m++ {
		if tableBit(table, uint(m)) != tableBit(table, uint(m)^(1<<bit)) {
			return true
		}
	}
	return false
}

func tableBit(table []uint64, m uint) bool {
	return table[m>>6]&(1<<(m&63)) != 0
}

// Key 返回规范键字符串；语义等价的表达式键相同。
func (c CanonSet) Key() string {
	var b strings.Builder
	b.WriteString("dom[")
	for i, a := range c.Atoms {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d:%s", len(a), a)
	}
	b.WriteString("]{")
	rows := 1 << uint(len(c.Atoms))
	byteLen := (rows + 7) / 8
	raw := make([]byte, 0, len(c.Table)*8)
	for _, w := range c.Table {
		for shift := 0; shift <= 56; shift += 8 {
			raw = append(raw, byte(w>>uint(shift)))
		}
	}
	if byteLen > len(raw) {
		byteLen = len(raw)
	}
	b.WriteString(hex.EncodeToString(raw[:byteLen]))
	b.WriteString("}")
	return b.String()
}

// Equal 报告两个规范形式是否相同。
func (c CanonSet) Equal(o CanonSet) bool {
	if len(c.Atoms) != len(o.Atoms) || len(c.Table) != len(o.Table) {
		return false
	}
	for i := range c.Atoms {
		if c.Atoms[i] != o.Atoms[i] || c.Table[i] != o.Table[i] {
			return false
		}
	}
	return true
}

// String 返回表达式的可读形式，便于调试与测试失败信息。
func (e *SetExpr) String() string {
	if e == nil {
		return "<nil>"
	}
	switch e.Kind {
	case EmptySet:
		return "∅"
	case FullSet:
		return "U"
	case BaseSet:
		return e.Name
	case UnionSet:
		return "(" + e.L.String() + "|" + e.R.String() + ")"
	case InterSet:
		return "(" + e.L.String() + "&" + e.R.String() + ")"
	case DiffSet:
		return "(" + e.L.String() + "-" + e.R.String() + ")"
	case CompSet:
		return "!" + e.X.String()
	}
	return "<invalid>"
}
