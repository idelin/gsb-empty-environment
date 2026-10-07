package backtest

import (
	"fmt"
	"time"
)

// Action 是策略在某个决策时间点产生的一个动作。
type Action struct {
	Seq       int64     // 动作序号，按产生顺序单调递增
	DecidedAt time.Time // 决策时间（触发事件的到达时间）
	TriggerID string    // 触发该动作的事件 ID
	Kind      string    // 动作类型，由策略定义
	Value     float64   // 动作携带的数值，由策略定义
}

// Strategy 由使用者实现。OnEvent 在每条事件到达时被调用，
// 策略只能通过 view 读取已到达的数据，并返回要执行的动作。
type Strategy interface {
	OnEvent(e Event, view *View) []Action
}

// Result 是一次回测运行的完整输出。
type Result struct {
	Actions []Action
	Audit   Audit
}

// Engine 按到达时间驱动事件，保证策略看不到未来数据。
type Engine struct {
	strategy Strategy
}

func NewEngine(s Strategy) *Engine {
	return &Engine{strategy: s}
}

// Run 执行一次回测：
//  1. 事件按 (ArriveAt, Seq) 排序，同到达时间按输入序号处理；
//  2. 事件先应用进 store（迟到更正在此生效），再回调策略；
//  3. 策略的所有读取记入审计读集合，动作按序编号。
func (eng *Engine) Run(events []Event) (*Result, error) {
	for i, e := range events {
		if e.ArriveAt.Before(e.OccurAt) {
			return nil, fmt.Errorf("engine: event #%d (id=%s) arrives before it occurs", i, e.ID)
		}
	}

	store := newStore()
	res := &Result{}
	var actionSeq int64

	for _, e := range orderEvents(events) {
		store.apply(e)
		view := &View{store: store, now: e.ArriveAt, audit: &res.Audit.Reads}
		for _, a := range eng.strategy.OnEvent(e, view) {
			actionSeq++
			a.Seq = actionSeq
			a.DecidedAt = e.ArriveAt
			a.TriggerID = e.ID
			res.Actions = append(res.Actions, a)
		}
	}

	if err := res.Audit.Verify(); err != nil {
		return nil, err
	}
	return res, nil
}

// Replay 用相同输入再跑一次，核对两次产生的动作序列完全一致，
// 以此验证回测行为的确定性（可重放性）。
func Replay(eng *Engine, events []Event) (*Result, error) {
	first, err := eng.Run(events)
	if err != nil {
		return nil, err
	}
	second, err := eng.Run(events)
	if err != nil {
		return nil, err
	}
	if len(first.Actions) != len(second.Actions) {
		return nil, fmt.Errorf("replay: action count differs: %d vs %d",
			len(first.Actions), len(second.Actions))
	}
	for i := range first.Actions {
		if first.Actions[i] != second.Actions[i] {
			return nil, fmt.Errorf("replay: action #%d differs: %+v vs %+v",
				i, first.Actions[i], second.Actions[i])
		}
	}
	return first, nil
}
