package main

import (
	"time"
)

// Row 是一行记录，列名 -> 值。
type Row map[string]any

// Table 是内存中的物理表。
type Table struct {
	Name string
	Rows []Row
}

// TableStats 是优化器可用的统计信息（可能已过期失效）。
type TableStats struct {
	RowCount float64 // 统计信息记录的全表行数
}

// Catalog 保存各表统计信息，模拟优化器的统计视图。
type Catalog struct {
	Stats map[string]TableStats
}

// ---- 查询算子（物理计划） ----

// Operator 是可执行的物理算子，同时携带优化器估算。
type Operator struct {
	ID      int
	Op      string
	Table   string
	Pred    func(Row) bool // Filter 谓词
	EstRows float64
	EstCost float64
	Child   *Operator
}

// EstPlan 将物理计划树转换为优化器估算树（EXPLAIN 输出）。
func EstPlan(op *Operator) *EstNode {
	n := &EstNode{ID: op.ID, Op: op.Op, Table: op.Table, EstRows: op.EstRows, EstCost: op.EstCost}
	if op.Child != nil {
		n.Children = []*EstNode{EstPlan(op.Child)}
	}
	return n
}

// Execute 执行物理计划并返回 EXPLAIN ANALYZE 实际执行树。
// 每个算子记录真实输出行数与耗时。
func Execute(op *Operator, tables map[string]*Table) *ActNode {
	_, act := run(op, tables)
	return act
}

func run(op *Operator, tables map[string]*Table) ([]Row, *ActNode) {
	start := time.Now()
	act := &ActNode{ID: op.ID, Op: op.Op, Table: op.Table, Loops: 1}

	var out []Row
	switch op.Op {
	case "Scan":
		t := tables[op.Table]
		out = append(out, t.Rows...)
	case "Filter":
		in, child := run(op.Child, tables)
		act.Children = []*ActNode{child}
		for _, r := range in {
			if op.Pred(r) {
				out = append(out, r)
			}
		}
	case "Aggregate":
		in, child := run(op.Child, tables)
		act.Children = []*ActNode{child}
		groups := map[any]int{}
		for _, r := range in {
			groups[r["region"]]++
		}
		for k := range groups {
			out = append(out, Row{"region": k})
		}
	default:
		panic("unknown operator: " + op.Op)
	}
	act.ActRows = int64(len(out))
	act.ActDuration = time.Since(start)
	return out, act
}

// ---- 优化器（基于可能过期的统计信息做估算） ----

// PlanQuery 生成 SELECT ... FROM users WHERE <pred> GROUP BY region 的物理计划，
// 并用 Catalog 中（可能失效的）统计信息填充估算行数与代价。
func PlanQuery(cat *Catalog, pred func(Row) bool, selectivity float64) *Operator {
	st := cat.Stats["users"]
	scan := &Operator{
		ID: 1, Op: "Scan", Table: "users",
		EstRows: st.RowCount, // 优化器只能看到统计信息中的行数
		EstCost: st.RowCount/10 + 1,
	}
	filter := &Operator{
		ID: 2, Op: "Filter", Pred: pred,
		EstRows: st.RowCount * selectivity, // 直方图给出的选择率
		EstCost: scan.EstCost + st.RowCount*0.01,
		Child:   scan,
	}
	agg := &Operator{
		ID: 3, Op: "Aggregate",
		EstRows: 10, // 优化器认为 region 基数很小
		EstCost: filter.EstCost + filter.EstRows*0.05,
		Child:   filter,
	}
	return agg
}

// ---- 演示场景：数据倾斜 + 统计信息失效 ----

// BuildSkewedUsers 构造一张含数据倾斜的 users 表：
// 共 50000 行，其中绝大多数 region='CN'（倾斜热点）。
func BuildSkewedUsers() *Table {
	t := &Table{Name: "users"}
	for i := 0; i < 49000; i++ {
		t.Rows = append(t.Rows, Row{"id": i, "region": "CN", "age": 20 + i%50})
	}
	for i := 0; i < 1000; i++ {
		t.Rows = append(t.Rows, Row{"id": 49000 + i, "region": "US", "age": 20 + i%50})
	}
	return t
}

// StaleCatalog 返回一份失效的统计信息：
// 上次 ANALYZE 时 users 只有 100 行，之后批量导入了 50000 行倾斜数据。
func StaleCatalog() *Catalog {
	return &Catalog{Stats: map[string]TableStats{
		"users": {RowCount: 100},
	}}
}
