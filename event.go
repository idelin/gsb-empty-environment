package backtest

import (
	"sort"
	"time"
)

// Event 是回测中的一条数据事件。
// 发生时间(OccurAt)是事件在真实世界发生的时间；
// 到达时间(ArriveAt)是策略实际能看到该事件的时间。
// 策略只能看到 ArriveAt <= 当前决策时间的事件。
type Event struct {
	Seq      int64     // 输入序号：到达时间相同时按 Seq 升序处理
	ID       string    // 逻辑事件 ID，更正事件与被更正事件共用同一 ID
	Version  int       // 版本号，更正事件的版本号更大
	OccurAt  time.Time // 发生时间
	ArriveAt time.Time // 到达时间
	Value    float64   // 载荷
}

// IsCorrection 报告该事件是否为对同 ID 事件的迟到更正。
func (e Event) IsCorrection() bool { return e.Version > 1 }

// orderEvents 按 (ArriveAt, Seq) 排序，返回新的切片，不修改输入。
func orderEvents(events []Event) []Event {
	ordered := make([]Event, len(events))
	copy(ordered, events)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if !a.ArriveAt.Equal(b.ArriveAt) {
			return a.ArriveAt.Before(b.ArriveAt)
		}
		return a.Seq < b.Seq
	})
	return ordered
}
