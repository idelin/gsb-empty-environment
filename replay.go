package cdcundo

import "fmt"

// Replay 把 UndoScript 逐条应用到数据库上。
//
// 重放走内部直通路径，不再向 CDC 日志写入新事件，
// 避免补偿操作本身又被当成“变更”而污染回滚窗口。
func Replay(db *Database, script *UndoScript) error {
	for i, st := range script.Statements {
		if err := applyOne(db, st); err != nil {
			return fmt.Errorf("replay statement %d (undo seq=%d): %v", i, st.Seq, err)
		}
	}
	return nil
}

func applyOne(db *Database, st Statement) error {
	t, err := db.mustTable(st.Table)
	if err != nil {
		return err
	}
	enc := encodeKey(st.Key)
	switch st.Op {
	case OpDelete:
		delete(t.rows, enc)
	case OpUpdate:
		cur, ok := t.rows[enc]
		if !ok {
			return fmt.Errorf("table %q: row not found for key %s", st.Table, enc)
		}
		for col, v := range st.Set {
			cur[col] = v
		}
	case OpInsert:
		if _, dup := t.rows[enc]; dup {
			return fmt.Errorf("table %q: duplicate key %s", st.Table, enc)
		}
		row := st.Set.Clone()
		for col, v := range st.Key {
			row[col] = v
		}
		t.rows[enc] = row
	}
	return nil
}

// Diff 对比两份表快照，返回不一致的描述（空切片表示完全一致）。
func Diff(table string, want, got []Row) []string {
	var problems []string
	if len(want) != len(got) {
		problems = append(problems,
			fmt.Sprintf("table %q: row count mismatch: want %d, got %d", table, len(want), len(got)))
		return problems
	}
	for i := range want {
		if !want[i].Equal(got[i]) {
			problems = append(problems,
				fmt.Sprintf("table %q: row %d mismatch:\n  want %v\n  got  %v", table, i, want[i], got[i]))
		}
	}
	return problems
}
