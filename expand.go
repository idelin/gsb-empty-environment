package planner

import "fmt"

// ExpandContract 生成经典的「扩-收」三操作序列：
// 新增列 → 填充数据 → 建立约束。
// 三者通过 Requires/Provides 串成强制顺序，规划器不可能将其错序。
// 其中建立约束需要停写窗口；填充数据默认不可逆（无回滚语句）。
func ExpandContract(table, column string) []Op {
	tbl := "table:" + table
	col := fmt.Sprintf("col:%s.%s", table, column)
	return []Op{
		{
			ID:       "add-column-" + column,
			Kind:     "add_column",
			Reads:    []string{tbl},
			Writes:   []string{col},
			Provides: []string{col + ":exists"},
			Rollback: fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column),
		},
		{
			ID:       "backfill-" + column,
			Kind:     "backfill",
			Reads:    []string{tbl},
			Writes:   []string{col},
			Requires: []string{col + ":exists"},
			Provides: []string{col + ":backfilled"},
			// 无 Rollback：批量填充不可逆，构成不可逆边界。
		},
		{
			ID:             "add-constraint-" + column,
			Kind:           "add_constraint",
			Reads:          []string{col},
			Writes:         []string{col},
			Requires:       []string{col + ":backfilled"},
			Provides:       []string{col + ":constrained"},
			NeedsWriteStop: true,
			Rollback:       fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s_not_null", table, column),
		},
	}
}
