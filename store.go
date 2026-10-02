package cdcundo

import (
	"fmt"
	"sort"
	"time"
)

// Store 是一个带 CDC 捕获能力的内存表存储，用于演示与验收测试：
// 每次写入都会记录 Before/After Image 到 CDC 日志。
//
// 主键在内部统一规范化为字符串（fmt.Sprint），
// 以兼容 CDC 日志 JSON 往返后数字变为 float64 的情况。
type Store struct {
	table  string
	pk     string
	rows   map[string]Row
	seq    int64
	writer *LogWriter
	now    func() time.Time
}

// NewStore 创建内存表。pk 为主键列名，writer 为 CDC 日志输出（可为 nil）。
func NewStore(table, pk string, writer *LogWriter) *Store {
	return &Store{
		table:  table,
		pk:     pk,
		rows:   make(map[string]Row),
		writer: writer,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

// keyOf 规范化主键值。
func keyOf(v interface{}) string { return fmt.Sprint(v) }

// capture 记录一条 CDC 事件。
func (s *Store) capture(txnID string, op Op, before, after Row) {
	s.seq++
	if s.writer != nil {
		_ = s.writer.Append(ChangeEvent{
			TxnID:  txnID,
			Seq:    s.seq,
			Table:  s.table,
			Op:     op,
			Before: before,
			After:  after,
			At:     s.now(),
		})
	}
}

// Insert 插入一行并捕获 INSERT 事件。
func (s *Store) Insert(txnID string, row Row) {
	s.rows[keyOf(row[s.pk])] = row.Clone()
	s.capture(txnID, OpInsert, nil, row)
}

// UpdateAll 对全表执行赋值更新（模拟误操作），逐行捕获 Before/After Image。
// 返回受影响行数。
func (s *Store) UpdateAll(txnID string, set Row) int {
	keys := make([]string, 0, len(s.rows))
	for k := range s.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		before := s.rows[k].Clone()
		after := s.rows[k].Clone()
		for col, v := range set {
			after[col] = v
		}
		s.rows[k] = after
		s.capture(txnID, OpUpdate, before, after)
	}
	return len(keys)
}

// Get 按主键读取一行。
func (s *Store) Get(key interface{}) (Row, bool) {
	r, ok := s.rows[keyOf(key)]
	return r, ok
}

// Len 返回行数。
func (s *Store) Len() int { return len(s.rows) }

// Snapshot 返回全表副本（规范化主键 -> 行），用于比对恢复前后状态。
func (s *Store) Snapshot() map[string]Row {
	out := make(map[string]Row, len(s.rows))
	for k, v := range s.rows {
		out[k] = v.Clone()
	}
	return out
}

// Replay 将回滚包中的补偿语句逐条应用到存储（模拟在数据库上执行 Undo 脚本）。
func (s *Store) Replay(pkg *RollbackPackage) error {
	for i, stmt := range pkg.Statements {
		if stmt.Table != s.table {
			return fmt.Errorf("cdcundo: 第 %d 条语句目标表 %q 与存储表 %q 不符", i, stmt.Table, s.table)
		}
		switch stmt.Kind {
		case StmtInsert:
			s.rows[keyOf(stmt.Set[s.pk])] = stmt.Set.Clone()
		case StmtUpdate:
			key := keyOf(stmt.Where[s.pk])
			row, ok := s.rows[key]
			if !ok {
				return fmt.Errorf("cdcundo: 第 %d 条语句定位失败，主键 %v 不存在", i, stmt.Where[s.pk])
			}
			for col, v := range stmt.Set {
				row[col] = v
			}
		case StmtDelete:
			delete(s.rows, keyOf(stmt.Where[s.pk]))
		}
	}
	return nil
}
