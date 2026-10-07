package backtest

import (
	"fmt"
	"time"
)

// Read 记录策略的一次读取，用于审计。
type Read struct {
	ID       string    // 被读事件的逻辑 ID
	Version  int       // 读到的版本
	ArriveAt time.Time // 该版本的到达时间
	ReadAt   time.Time // 读取发生时的决策时间
	Value    float64
}

// Store 保存"当前已到达"的最新版本事件。
// 只有 ArriveAt <= 当前处理时间的事件才会被应用进来，
// 因此策略通过 View 读到的任何数据都不可能来自未来。
type Store struct {
	latest map[string]Event
}

func newStore() *Store {
	return &Store{latest: make(map[string]Event)}
}

// apply 将一条已到达的事件写入 store。
// 迟到更正（同 ID、更高版本）会覆盖旧值，影响后续决策；
// 已产生的历史动作不受影响。低版本晚到的高序号事件不会回滚高版本。
func (s *Store) apply(e Event) {
	cur, ok := s.latest[e.ID]
	if ok && cur.Version > e.Version {
		return
	}
	s.latest[e.ID] = e
}

// View 是策略在某个决策时间点看到的只读视图，
// 每次读取都会记录到审计读集合中。
type View struct {
	store *Store
	now   time.Time
	audit *[]Read
}

// Get 返回 ID 当前已到达的最新版本。
func (v *View) Get(id string) (Event, bool) {
	e, ok := v.store.latest[id]
	if !ok {
		return Event{}, false
	}
	*v.audit = append(*v.audit, Read{
		ID:       id,
		Version:  e.Version,
		ArriveAt: e.ArriveAt,
		ReadAt:   v.now,
		Value:    e.Value,
	})
	return e, true
}

// Now 返回当前决策时间（即正在处理事件的到达时间）。
func (v *View) Now() time.Time { return v.now }

// Audit 是审计读集合：策略运行期间全部读取的记录。
type Audit struct {
	Reads []Read
}

// Verify 校验审计读集合中没有任何"读取未来数据"的行为：
// 每条读记录所读版本的到达时间必须不晚于读取时的决策时间。
func (a *Audit) Verify() error {
	for i, r := range a.Reads {
		if r.ArriveAt.After(r.ReadAt) {
			return fmt.Errorf("audit: read #%d accessed future data: id=%s version=%d arrived=%s read-at=%s",
				i, r.ID, r.Version, r.ArriveAt, r.ReadAt)
		}
	}
	return nil
}
