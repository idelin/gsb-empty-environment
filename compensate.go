package cdcundo

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Compensator 反向补偿生成器：将 CDC 事件逆向映射为补偿 DML。
//
// 映射规则：
//   INSERT -> DELETE（按 After Image 主键删除）
//   DELETE -> INSERT（按 Before Image 整行还原）
//   UPDATE -> UPDATE（用 Before Image 反向赋值，按主键定位）
type Compensator struct {
	// PrimaryKeys 表名 -> 主键列（有序）。
	PrimaryKeys map[string][]string
}

// NewCompensator 创建补偿生成器。
func NewCompensator(pk map[string][]string) *Compensator {
	return &Compensator{PrimaryKeys: pk}
}

// pick 从镜像中抽取指定列。
func pick(r Row, cols []string) Row {
	out := make(Row, len(cols))
	for _, c := range cols {
		out[c] = r[c]
	}
	return out
}

// diffColumns 返回 After 相对 Before 发生变化的列（UPDATE 只需回滚变更列）。
func diffColumns(before, after Row) []string {
	var cols []string
	for k, av := range after {
		if bv, ok := before[k]; !ok || fmt.Sprint(bv) != fmt.Sprint(av) {
			cols = append(cols, k)
		}
	}
	sort.Strings(cols)
	return cols
}

// Inverse 为单条 CDC 事件生成反向补偿语句。
func (c *Compensator) Inverse(ev ChangeEvent) (Statement, error) {
	if err := ev.Validate(); err != nil {
		return Statement{}, err
	}
	pk, ok := c.PrimaryKeys[ev.Table]
	if !ok || len(pk) == 0 {
		return Statement{}, fmt.Errorf("cdcundo: 表 %q 未配置主键，无法定位补偿目标", ev.Table)
	}
	meta := fmt.Sprintf("undo txn=%s seq=%d op=%s at=%s",
		ev.TxnID, ev.Seq, ev.Op, ev.At.UTC().Format("2006-01-02T15:04:05.000Z"))

	switch ev.Op {
	case OpInsert:
		// 反向：按主键删除新插入的行。
		where := pick(ev.After, pk)
		sql, args := renderDelete(ev.Table, where)
		return Statement{Kind: StmtDelete, Table: ev.Table, SQL: sql, Args: args, Where: where, Comment: meta}, nil

	case OpDelete:
		// 反向：按 Before Image 整行插回。
		set := ev.Before.Clone()
		sql, args := renderInsert(ev.Table, set)
		return Statement{Kind: StmtInsert, Table: ev.Table, SQL: sql, Args: args, Set: set, Comment: meta}, nil

	case OpUpdate:
		// 反向：将变更列恢复为 Before Image 的值。
		changed := diffColumns(ev.Before, ev.After)
		if len(changed) == 0 {
			return Statement{}, fmt.Errorf("cdcundo: seq=%d UPDATE 无实际变更列，无需补偿", ev.Seq)
		}
		set := pick(ev.Before, changed)
		where := pick(ev.After, pk)
		sql, args := renderUpdate(ev.Table, set, where)
		return Statement{Kind: StmtUpdate, Table: ev.Table, SQL: sql, Args: args, Set: set, Where: where, Comment: meta}, nil
	}
	return Statement{}, fmt.Errorf("cdcundo: 不支持的操作 %q", ev.Op)
}

// RollbackPackage 是一份可交付的数据回滚包：有序补偿语句序列 + 元信息。
type RollbackPackage struct {
	Name       string      `json:"name"`
	CreatedAt  time.Time   `json:"created_at"`
	Statements []Statement `json:"statements"`
}

// BuildRollbackPackage 由 CDC 事件流构建回滚包。
//
// 关键：补偿语句按源事件逆序（LIFO）排列——后发生的变更先冲正，
// 保证同一行多次变更时镜像逐层还原，不会出现中间态覆盖。
func (c *Compensator) BuildRollbackPackage(name string, events []ChangeEvent) (*RollbackPackage, error) {
	pkg := &RollbackPackage{Name: name, CreatedAt: time.Now().UTC()}
	for i := len(events) - 1; i >= 0; i-- {
		stmt, err := c.Inverse(events[i])
		if err != nil {
			return nil, err
		}
		pkg.Statements = append(pkg.Statements, stmt)
	}
	return pkg, nil
}

// FilterByTxn 提取指定事务的事件子集（用于只回滚误操作事务）。
func FilterByTxn(events []ChangeEvent, txnID string) []ChangeEvent {
	var out []ChangeEvent
	for _, ev := range events {
		if ev.TxnID == txnID {
			out = append(out, ev)
		}
	}
	return out
}

// SQL 渲染整份回滚包为可独立执行的 Undo 脚本文本。
func (p *RollbackPackage) SQL() string {
	var b strings.Builder
	fmt.Fprintf(&b, "-- Rollback Package: %s\n", p.Name)
	fmt.Fprintf(&b, "-- Generated At: %s\n", p.CreatedAt.Format("2006-01-02T15:04:05.000Z"))
	fmt.Fprintf(&b, "-- Statements: %d\n", len(p.Statements))
	b.WriteString("BEGIN;\n")
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "-- %s\n", s.Comment)
		b.WriteString(inlineSQL(s.SQL, s.Args))
		b.WriteString("\n")
	}
	b.WriteString("COMMIT;\n")
	return b.String()
}
