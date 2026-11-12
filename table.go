package netsec

import (
	"fmt"
	"sort"
)

// Row 是关系中的一行：列名到带证明值的映射。
// 行本身不携带额外的“人数”，每个值各自记录其不同主体支持集。
type Row map[string]Value

// Table 是带列序的行集合，供查询宿主在算子之间传递。
type Table struct {
	Columns []string
	Rows    []Row
}

// NewTable 用列名与行构造关系，列序仅用于稳定输出。
func NewTable(columns []string, rows ...Row) Table {
	return Table{Columns: append([]string(nil), columns...), Rows: rows}
}

// CopyRows 复制每一行但不改写任何证明的支持集。
//
// 该算子对应投影放大或结果复制：行数可以任意增加，但每个值的不同主体
// 支持集保持不变，因此复制出来的行不能帮助任何值达到去标阈值。
func CopyRows(in Table, copies int) Table {
	if copies < 1 {
		copies = 1
	}
	out := Table{
		Columns: append([]string(nil), in.Columns...),
		Rows:    make([]Row, 0, len(in.Rows)*copies),
	}
	for i := 0; i < copies; i++ {
		for _, row := range in.Rows {
			clone := make(Row, len(row))
			for col, val := range row {
				clone[col] = val.Copy()
			}
			out.Rows = append(out.Rows, clone)
		}
	}
	return out
}

// Filter 保留谓词返回 true 的行。
//
// 过滤只决定行的去留，绝不改写保留值的证明：被保留值的真实支持主体
// 不因过滤而增加，也不会被悄悄“补上”被删掉行的主体。去标判定必须在
// 过滤之后针对实际保留的值重新评估，而不能沿用过滤前的整体结论。
func Filter(in Table, keep func(Row) bool) Table {
	out := Table{
		Columns: append([]string(nil), in.Columns...),
		Rows:    make([]Row, 0, len(in.Rows)),
	}
	for _, row := range in.Rows {
		if keep(row) {
			out.Rows = append(out.Rows, row)
		}
	}
	return out
}

// JoinKey 提取连接键值。参与比较的是 Value.Data；证明不参与键比较。
func JoinKey(row Row, column string) any {
	return row[column].Data
}

// Join 对两张表按等值连接键做内连接。
//
// 输出列加前缀（左 "l."、右 "r."）避免列名冲突。每个输出值的证明为其
// 源列证明与两侧连接键证明的并集：某主体在左右两侧、或在扇出的多行
// 命中里重复出现都只计一次，因此一对多连接不会按命中次数累加人数。
func Join(left, right Table, leftKey, rightKey string) Table {
	out := Table{Columns: make([]string, 0, len(left.Columns)+len(right.Columns))}
	for _, col := range left.Columns {
		out.Columns = append(out.Columns, "l."+col)
	}
	for _, col := range right.Columns {
		out.Columns = append(out.Columns, "r."+col)
	}

	index := map[any][]Row{}
	for _, row := range right.Rows {
		key := JoinKey(row, rightKey)
		index[key] = append(index[key], row)
	}

	for _, lrow := range left.Rows {
		for _, rrow := range index[JoinKey(lrow, leftKey)] {
			joined := make(Row, len(left.Columns)+len(right.Columns))
			keyProof := Union(lrow[leftKey].Proof, rrow[rightKey].Proof)
			for col, val := range lrow {
				joined["l."+col] = WithProof(val.Data, Union(val.Proof, keyProof))
			}
			for col, val := range rrow {
				joined["r."+col] = WithProof(val.Data, Union(val.Proof, keyProof))
			}
			out.Rows = append(out.Rows, joined)
		}
	}
	return out
}

// AggregateFn 把同组内的带证明值折叠为一个结果数据。
// 聚合结果的证明由 GroupAggregate 统一按不同主体并集传播，聚合函数
// 自身只负责计算数据，不得用行数冒充主体数。
type AggregateFn func(values []Value) any

// SumInts 是示例聚合函数：对整型数据求和，非整型数据按 0 处理。
func SumInts(values []Value) any {
	total := 0
	for _, v := range values {
		if n, ok := v.Data.(int); ok {
			total += n
		}
	}
	return total
}

// GroupAggregate 按分组列对输入分组，并对目标列应用聚合函数。
//
// 每个分组结果值的证明是该组所有输入值证明的并集，所以：
//   - 同一主体有多行参与时只贡献一个不同主体；
//   - 只有并集中不同主体数达到策略阈值 K，结果才可去标；
//   - 行数再多也不能替代主体数。
//
// 输出表包含分组列与结果列；结果值自带最终证明，去标判定延迟到宿主
// 读取结果时按 Policy.CanRelease 进行。
func GroupAggregate(in Table, groupColumns []string, aggColumn, resultColumn string, fn AggregateFn) Table {
	outColumns := append(append([]string(nil), groupColumns...), resultColumn)
	out := Table{Columns: outColumns}

	groups := map[string][]Row{}
	order := []string{}
	for _, row := range in.Rows {
		key := groupKey(row, groupColumns)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], row)
	}

	for _, key := range order {
		members := groups[key]
		values := make([]Value, 0, len(members))
		proofs := make([]Proof, 0, len(members))
		for _, row := range members {
			val := row[aggColumn]
			values = append(values, val)
			proofs = append(proofs, val.Proof)
		}

		result := Row{}
		for _, col := range groupColumns {
			result[col] = members[0][col]
		}
		result[resultColumn] = WithProof(fn(values), Union(proofs...))
		out.Rows = append(out.Rows, result)
	}

	sort.SliceStable(out.Rows, func(i, j int) bool {
		return groupKey(out.Rows[i], groupColumns) < groupKey(out.Rows[j], groupColumns)
	})
	return out
}

// Aggregate 是无分组的整表聚合，等价于单组 GroupAggregate。
func Aggregate(in Table, aggColumn, resultColumn string, fn AggregateFn) Table {
	return GroupAggregate(in, nil, aggColumn, resultColumn, fn)
}

// RowReleasable 报告一行中是否所有值都达到去标阈值。
// 任一带证明值的不同主体数不足，整行都不能放行。
func RowReleasable(row Row, policy Policy) bool {
	for _, val := range row {
		if policy.Sensitive(val) {
			return false
		}
	}
	return true
}

// Materialize 按策略把结果拆成可放行行与仍敏感行。
//
// 放行判定逐值进行，且基于该值自身证明的不同主体数；敏感行原样保留在
// 第二个返回值中，由宿主决定拒绝或改写，绝不与已去标行混发给未授权方。
func Materialize(in Table, policy Policy) (released []Row, sensitive []Row) {
	for _, row := range in.Rows {
		if RowReleasable(row, policy) {
			released = append(released, row)
		} else {
			sensitive = append(sensitive, row)
		}
	}
	return released, sensitive
}

func groupKey(row Row, columns []string) string {
	key := ""
	for _, col := range columns {
		key += string(rune(0)) + col + "=" + toKey(row[col].Data)
	}
	return key
}

func toKey(data any) string {
	return fmt.Sprint(data)
}
