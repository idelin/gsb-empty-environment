package sensitivity

import (
	"fmt"
	"math/big"
	"sort"
)

// Cell 是一个数据单元。使用 *big.Rat 保证敏感度与核验过程中不引入
// 浮点舍入；nil 表示 NULL（连接键不允许为 NULL）。
type Cell = *big.Rat

// Row 是有序单元向量，列序由所属表的 Schema 决定。
type Row struct {
	Values []Cell
}

// Table 是一个具名关系实例。列名在 Schema 内必须唯一。
type Table struct {
	Name   string
	Schema []string
	Rows   []Row
}

// NewRat 由整数构造精确有理数。
func NewRat(n int64) Cell { return big.NewRat(n, 1) }

// ParseRat 解析 "a/b" 或普通十进制字符串为精确有理数。
func ParseRat(s string) (Cell, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("sensitivity: 无法解析数值 %q", s)
	}
	return r, nil
}

func ratClone(c Cell) Cell {
	if c == nil {
		return nil
	}
	return new(big.Rat).Set(c)
}

func ratEqual(a, b Cell) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Cmp(b) == 0
}

// ratAbs 返回 |x|，入参为非 nil。
func ratAbs(x Cell) Cell {
	if x.Sign() < 0 {
		return new(big.Rat).Neg(x)
	}
	return new(big.Rat).Set(x)
}

// ratMax 返回两个非负数中的较大者；nil 视为 0。
func ratMax(a, b Cell) Cell {
	switch {
	case a == nil:
		return ratClone(b)
	case b == nil:
		return ratClone(a)
	case a.Cmp(b) >= 0:
		return ratClone(a)
	default:
		return ratClone(b)
	}
}

// rowClone 深拷贝一行，避免算子之间共享可变单元。
func rowClone(r Row) Row {
	out := Row{Values: make([]Cell, len(r.Values))}
	for i, v := range r.Values {
		out.Values[i] = ratClone(v)
	}
	return out
}

// canonicalKey 生成行的确定性字符串编码，用于核验时比较两张表。
func (t Table) canonicalKey(r Row) string {
	b := make([]byte, 0, 32)
	for i, v := range r.Values {
		if i > 0 {
			b = append(b, '|')
		}
		if v == nil {
			b = append(b, 0)
		} else {
			b = append(b, v.RatString()...)
		}
	}
	return string(b)
}

// columnIndex 返回列名到位置的映射，列缺失时返回错误。
func (t Table) columnIndex(col string) (int, error) {
	for i, name := range t.Schema {
		if name == col {
			return i, nil
		}
	}
	return 0, fmt.Errorf("sensitivity: 表 %q 不存在列 %q", t.Name, col)
}

// validate 检查表结构与行宽是否一致。
func (t Table) validate() error {
	seen := map[string]bool{}
	for _, name := range t.Schema {
		if seen[name] {
			return fmt.Errorf("sensitivity: 表 %q 列名重复 %q", t.Name, name)
		}
		seen[name] = true
	}
	for i, r := range t.Rows {
		if len(r.Values) != len(t.Schema) {
			return fmt.Errorf("sensitivity: 表 %q 第 %d 行宽度 %d 与模式宽度 %d 不一致",
				t.Name, i, len(r.Values), len(t.Schema))
		}
	}
	return nil
}

// multisetDiff 返回多行集 t 相对 base 的多重集差：
// 正数表示 t 比 base 多出的行数，负数表示减少的行数（按行编码聚合）。
func multisetDiff(base, t Table) map[string]int {
	counts := map[string]int{}
	for _, r := range base.Rows {
		counts[base.canonicalKey(r)]--
	}
	for _, r := range t.Rows {
		counts[t.canonicalKey(r)]++
	}
	for k, v := range counts {
		if v == 0 {
			delete(counts, k)
		}
	}
	return counts
}

// multisetDistance 返回两个同模式行多重集之间的 L1 距离（行数差总和）。
func multisetDistance(base, t Table) int {
	d := 0
	for _, delta := range multisetDiff(base, t) {
		if delta > 0 {
			d += delta
		} else {
			d -= delta
		}
	}
	return d
}

// sortedKeys 返回 map 的确定性键序，供报告与测试使用。
func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
