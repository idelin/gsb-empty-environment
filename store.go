package cdcundo

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Database 是一个内存数据库，所有写操作都会把
// Before/After 镜像捕获进 CDC 日志，模拟真实库的 CDC 采集端。
type Database struct {
	tables map[string]*table
	log    *CDCLog
	txnSeq uint64
}

type table struct {
	name string
	pk   []string
	rows map[string]Row // 主键编码 -> 行
}

// NewDatabase 创建挂载了指定 CDC 日志的数据库实例。
func NewDatabase(log *CDCLog) *Database {
	return &Database{tables: make(map[string]*table), log: log}
}

// Log 返回数据库正在写入的 CDC 日志。
func (db *Database) Log() *CDCLog {
	return db.log
}

// CreateTable 建表，pk 为主键列（有序）。
func (db *Database) CreateTable(name string, pk ...string) error {
	if _, ok := db.tables[name]; ok {
		return fmt.Errorf("table %q already exists", name)
	}
	if len(pk) == 0 {
		return fmt.Errorf("table %q: primary key required", name)
	}
	db.tables[name] = &table{name: name, pk: append([]string(nil), pk...), rows: make(map[string]Row)}
	return nil
}

func (db *Database) mustTable(name string) (*table, error) {
	t, ok := db.tables[name]
	if !ok {
		return nil, fmt.Errorf("table %q does not exist", name)
	}
	return t, nil
}

// encodeKey 把主键列编码为唯一字符串。
func encodeKey(key Row) string {
	keys := key.SortedKeys()
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%#v", k, key[k]))
	}
	return strings.Join(parts, "\x00")
}

func (t *table) keyOf(row Row) (Row, string, error) {
	key := make(Row, len(t.pk))
	for _, col := range t.pk {
		v, ok := row[col]
		if !ok {
			return nil, "", fmt.Errorf("table %q: missing primary key column %q", t.name, col)
		}
		key[col] = v
	}
	return key, encodeKey(key), nil
}

// nextTxn 生成事务 ID。
func (db *Database) nextTxn() string {
	db.txnSeq++
	return fmt.Sprintf("txn-%d", db.txnSeq)
}

// Insert 插入一行，并捕获 After 镜像。
func (db *Database) Insert(table string, row Row) error {
	return db.InsertTxn(db.nextTxn(), table, row)
}

// InsertTxn 在指定事务中插入一行。
func (db *Database) InsertTxn(txn, table string, row Row) error {
	t, err := db.mustTable(table)
	if err != nil {
		return err
	}
	key, enc, err := t.keyOf(row)
	if err != nil {
		return err
	}
	if _, dup := t.rows[enc]; dup {
		return fmt.Errorf("table %q: duplicate key %s", table, enc)
	}
	stored := row.Clone()
	t.rows[enc] = stored
	db.log.Append(ChangeEvent{
		TxnID: txn, Timestamp: time.Now(), Table: table, Op: OpInsert,
		Key: key, After: stored.Clone(),
	})
	return nil
}

// Update 按主键更新一行，set 为要修改的列，返回是否命中。
func (db *Database) Update(table string, key Row, set Row) (bool, error) {
	return db.UpdateTxn(db.nextTxn(), table, key, set)
}

// UpdateTxn 在指定事务中按主键更新一行。
func (db *Database) UpdateTxn(txn, table string, key Row, set Row) (bool, error) {
	t, err := db.mustTable(table)
	if err != nil {
		return false, err
	}
	enc := encodeKey(key)
	cur, ok := t.rows[enc]
	if !ok {
		return false, nil
	}
	before := cur.Clone()
	for col, v := range set {
		if contains(t.pk, col) {
			return false, fmt.Errorf("table %q: cannot update primary key column %q", table, col)
		}
		cur[col] = v
	}
	db.log.Append(ChangeEvent{
		TxnID: txn, Timestamp: time.Now(), Table: table, Op: OpUpdate,
		Key: key.Clone(), Before: before, After: cur.Clone(),
	})
	return true, nil
}

// UpdateWhere 批量更新：对所有满足 pred 的行应用 set，
// 整个批量操作共用一个事务 ID（误操作场景的典型形态）。
// 返回受影响行数与事务 ID。
func (db *Database) UpdateWhere(table string, set Row, pred func(Row) bool) (int, string, error) {
	t, err := db.mustTable(table)
	if err != nil {
		return 0, "", err
	}
	txn := db.nextTxn()
	// 固定遍历顺序，保证日志序号确定。
	encs := make([]string, 0, len(t.rows))
	for enc := range t.rows {
		encs = append(encs, enc)
	}
	sort.Strings(encs)
	n := 0
	for _, enc := range encs {
		cur := t.rows[enc]
		if pred != nil && !pred(cur.Clone()) {
			continue
		}
		before := cur.Clone()
		for col, v := range set {
			if contains(t.pk, col) {
				return n, txn, fmt.Errorf("table %q: cannot update primary key column %q", table, col)
			}
			cur[col] = v
		}
		key := make(Row, len(t.pk))
		for _, col := range t.pk {
			key[col] = cur[col]
		}
		db.log.Append(ChangeEvent{
			TxnID: txn, Timestamp: time.Now(), Table: table, Op: OpUpdate,
			Key: key, Before: before, After: cur.Clone(),
		})
		n++
	}
	return n, txn, nil
}

// Delete 按主键删除一行，并捕获 Before 镜像。
func (db *Database) Delete(table string, key Row) (bool, error) {
	t, err := db.mustTable(table)
	if err != nil {
		return false, err
	}
	enc := encodeKey(key)
	cur, ok := t.rows[enc]
	if !ok {
		return false, nil
	}
	delete(t.rows, enc)
	db.log.Append(ChangeEvent{
		TxnID: db.nextTxn(), Timestamp: time.Now(), Table: table, Op: OpDelete,
		Key: key.Clone(), Before: cur.Clone(),
	})
	return true, nil
}

// Get 按主键读取一行（返回拷贝）。
func (db *Database) Get(table string, key Row) (Row, bool, error) {
	t, err := db.mustTable(table)
	if err != nil {
		return nil, false, err
	}
	cur, ok := t.rows[encodeKey(key)]
	if !ok {
		return nil, false, nil
	}
	return cur.Clone(), true, nil
}

// Snapshot 导出整张表的行拷贝，按主键编码排序，用于状态比对。
func (db *Database) Snapshot(table string) ([]Row, error) {
	t, err := db.mustTable(table)
	if err != nil {
		return nil, err
	}
	encs := make([]string, 0, len(t.rows))
	for enc := range t.rows {
		encs = append(encs, enc)
	}
	sort.Strings(encs)
	out := make([]Row, 0, len(encs))
	for _, enc := range encs {
		out = append(out, t.rows[enc].Clone())
	}
	return out, nil
}

// Count 返回表内行数。
func (db *Database) Count(table string) (int, error) {
	t, err := db.mustTable(table)
	if err != nil {
		return 0, err
	}
	return len(t.rows), nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
