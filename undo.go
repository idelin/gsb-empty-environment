package cdcundo

import "time"

// Filter 圈定需要回滚的 CDC 事件范围（各字段为零值表示不限制）。
type Filter struct {
	Table string    // 只回滚指定表
	TxnID string    // 只回滚指定事务（误操作通常落在同一事务）
	From  time.Time // 事件时间下界（含），毫秒级
	To    time.Time // 事件时间上界（含），毫秒级
}

func (f Filter) match(e ChangeEvent) bool {
	if f.Table != "" && e.Table != f.Table {
		return false
	}
	if f.TxnID != "" && e.TxnID != f.TxnID {
		return false
	}
	if !f.From.IsZero() && e.Timestamp.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && e.Timestamp.After(f.To) {
		return false
	}
	return true
}

// Statement 是一条补偿 DML，既可渲染为 SQL，也可作为 API 调用参数。
type Statement struct {
	Seq     uint64 // 被冲正的原始事件序号
	Table   string
	Op      Op  // 补偿操作类型（原操作的逆操作）
	Key     Row // 主键条件
	Set     Row // UPDATE 的赋值列 / INSERT 的整行
	SQL     string
	Comment string
}

// UndoScript 是按“逆序重放”原则排好序的补偿 DML 序列。
type UndoScript struct {
	Statements []Statement
}

// SQL 输出整份回滚脚本（可直接交给数据库执行）。
func (s *UndoScript) SQL() string {
	out := ""
	for _, st := range s.Statements {
		out += "-- " + st.Comment + "\n" + st.SQL + "\n"
	}
	return out
}

// Generate 从 CDC 日志中筛出目标事件，逆向生成补偿 DML 序列。
//
// 规则：
//   INSERT -> DELETE（按主键删除误插入的行）
//   UPDATE -> UPDATE（用 Before Image 反向赋值）
//   DELETE -> INSERT（用 Before Image 整行补回）
//
// 事件按 Seq 逆序处理，保证同一行上的多次变更被严格倒放。
func Generate(log *CDCLog, f Filter) *UndoScript {
	events := log.Events()
	script := &UndoScript{}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if !f.match(e) {
			continue
		}
		script.Statements = append(script.Statements, compensate(e))
	}
	return script
}

// compensate 为单条 CDC 事件生成逆操作。
func compensate(e ChangeEvent) Statement {
	base := Statement{Seq: e.Seq, Table: e.Table, Key: e.Key.Clone()}
	switch e.Op {
	case OpInsert:
		base.Op = OpDelete
		base.SQL = renderDelete(e.Table, e.Key)
		base.Comment = "undo INSERT seq=" + itoa(e.Seq)
	case OpUpdate:
		base.Op = OpUpdate
		base.Set = e.Before.Clone()
		base.SQL = renderUpdate(e.Table, e.Key, e.Before)
		base.Comment = "undo UPDATE seq=" + itoa(e.Seq)
	case OpDelete:
		base.Op = OpInsert
		base.Set = e.Before.Clone()
		base.SQL = renderInsert(e.Table, e.Before)
		base.Comment = "undo DELETE seq=" + itoa(e.Seq)
	}
	return base
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
