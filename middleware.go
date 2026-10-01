package splitter

import (
	"context"
	"fmt"
	"sync"
)

// SourceDB 业务源库的极简内存实现，保存各表行数据。
type SourceDB struct {
	mu     sync.Mutex
	tables map[string]map[int64]Row
}

func NewSourceDB() *SourceDB {
	return &SourceDB{tables: map[string]map[int64]Row{}}
}

// Insert 向源库写入一行（行中须含 "id"）。
func (db *SourceDB) Insert(table string, row Row) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.tables[table] == nil {
		db.tables[table] = map[int64]Row{}
	}
	db.tables[table][toInt64(row["id"])] = row
}

// Get 读取一行。
func (db *SourceDB) Get(table string, id int64) (Row, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	r, ok := db.tables[table][id]
	return r, ok
}

// applyUpdate 应用 SET 子句并返回前镜像。
func (db *SourceDB) applyUpdate(table string, id int64, set Row) (Row, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	row, ok := db.tables[table][id]
	if !ok {
		return nil, fmt.Errorf("splitter: %s id=%d 不存在", table, id)
	}
	before := Row{}
	for k, v := range row {
		before[k] = v
	}
	for k, v := range set {
		row[k] = v
	}
	return before, nil
}

// Middleware 业务逻辑标记处理中间件：对业务透明地拦截 UPDATE，
// 在源库执行后把变更事件交给分流器。
type Middleware struct {
	DB       *SourceDB
	Splitter *Splitter
	// OnRouted 分流完成回调（演示/观测用），可为 nil。
	OnRouted func(routed []Event)
}

// Exec 业务唯一入口：业务像平常一样执行 SQL，无需感知分流逻辑。
func (m *Middleware) Exec(ctx context.Context, sql string) error {
	table, id, set, err := ParseUpdate(sql)
	if err != nil {
		return err
	}
	before, err := m.DB.applyUpdate(table, id, set)
	if err != nil {
		return err
	}
	after, _ := m.DB.Get(table, id)
	evt := Event{Type: EventUpdate, Table: table, ID: id, Before: before, After: after, Raw: sql}
	routed, err := m.Splitter.Handle(ctx, evt)
	if err != nil {
		return err
	}
	if m.OnRouted != nil {
		m.OnRouted(routed)
	}
	return nil
}
