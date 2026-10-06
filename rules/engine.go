// Package rules 实现同对象多事件的有效值裁决引擎。
//
// 裁决规则：业务优先级 Priority 高者胜；并列时事件序号 Seq 大者胜；
// 再并列时事件 ID 字典序小者胜（保证确定性）。
// 撤回（Retract）只针对特定事件 ID，且允许先于事件到达（墓碑先立，
// 事件后到时直接视为已撤回）。当前赢家被撤回后，下一候选自动显露。
package rules

import (
	"fmt"
	"sort"
)

// Event 是一条携带业务值的候选事件。
type Event struct {
	ID       string // 全局唯一事件 ID
	Object   string // 业务对象标识（同一 Object 内竞争有效值）
	Priority int    // 业务优先级，越大越优先
	Seq      int64  // 事件序号，同优先级时越大越优先
	Payload  string // 业务值
}

// better 报告 a 是否排在 b 之前（a 比 b 更优）。
func better(a, b Event) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if a.Seq != b.Seq {
		return a.Seq > b.Seq
	}
	return a.ID < b.ID
}

// Delta 描述一次操作后某对象有效值的变化。
type Delta struct {
	Object  string
	Changed bool
	HadOld  bool
	Old     Event
	HasNew  bool
	New     Event
}

func (d Delta) String() string {
	if !d.Changed {
		return fmt.Sprintf("[%s] 有效值不变", d.Object)
	}
	old := "<无>"
	if d.HadOld {
		old = fmt.Sprintf("%s(id=%s)", d.Old.Payload, d.Old.ID)
	}
	newV := "<无>"
	if d.HasNew {
		newV = fmt.Sprintf("%s(id=%s)", d.New.Payload, d.New.ID)
	}
	return fmt.Sprintf("[%s] 有效值: %s -> %s", d.Object, old, newV)
}

// Conflict 记录同 ID 但内容不一致的冲突载荷。
type Conflict struct {
	Existing Event
	Incoming Event
}

// Engine 增量维护每个对象的有效值。
type Engine struct {
	events    map[string]Event    // 全部已知事件（含已撤回），按 ID
	retracted map[string]struct{} // 撤回墓碑，允许先于事件到达
	active    map[string][]Event  // object -> 存活候选，按优劣降序（队首即赢家）
	effective map[string]Event    // object -> 当前有效事件
	hasEff    map[string]bool

	// Duplicates 记录内容完全一致的重复投递（幂等忽略）。
	Duplicates []Event
	// Conflicts 记录同 ID 不同内容的冲突载荷（保留先到者，忽略后来者）。
	Conflicts []Conflict
}

func New() *Engine {
	return &Engine{
		events:    make(map[string]Event),
		retracted: make(map[string]struct{}),
		active:    make(map[string][]Event),
		effective: make(map[string]Event),
		hasEff:    make(map[string]bool),
	}
}

// Effective 返回对象当前的有效事件。
func (e *Engine) Effective(object string) (Event, bool) {
	ev, ok := e.effective[object]
	return ev, ok
}

// Apply 接入一条事件，返回有效值差量。
// 同 ID 重复投递幂等忽略；同 ID 不同内容记为冲突并保留先到者。
func (e *Engine) Apply(ev Event) Delta {
	if prev, ok := e.events[ev.ID]; ok {
		if prev == ev {
			e.Duplicates = append(e.Duplicates, ev)
		} else {
			e.Conflicts = append(e.Conflicts, Conflict{Existing: prev, Incoming: ev})
		}
		return e.unchanged(ev.Object)
	}
	e.events[ev.ID] = ev

	if _, dead := e.retracted[ev.ID]; dead {
		// 墓碑先立：事件到达即视为已撤回，不进入候选。
		return e.unchanged(ev.Object)
	}

	cands := e.active[ev.Object]
	i := sort.Search(len(cands), func(i int) bool { return better(ev, cands[i]) })
	cands = append(cands, Event{})
	copy(cands[i+1:], cands[i:])
	cands[i] = ev
	e.active[ev.Object] = cands

	return e.refresh(ev.Object)
}

// Retract 撤回指定 ID 的事件，返回有效值差量。
// 只针对该事件 ID；事件尚未到达时先立墓碑，重复撤回幂等。
func (e *Engine) Retract(targetID string) Delta {
	e.retracted[targetID] = struct{}{}

	ev, ok := e.events[targetID]
	if !ok {
		return Delta{} // 墓碑已立，等事件到达时生效
	}
	cands := e.active[ev.Object]
	for i, c := range cands {
		if c.ID == targetID {
			e.active[ev.Object] = append(cands[:i], cands[i+1:]...)
			return e.refresh(ev.Object)
		}
	}
	return e.unchanged(ev.Object) // 已撤回过，幂等
}

// refresh 重算对象赢家并生成差量。
func (e *Engine) refresh(object string) Delta {
	old, hadOld := e.effective[object], e.hasEff[object]

	cands := e.active[object]
	if len(cands) == 0 {
		delete(e.effective, object)
		delete(e.hasEff, object)
	} else {
		e.effective[object] = cands[0]
		e.hasEff[object] = true
	}

	now, hasNow := e.effective[object]
	if hadOld == hasNow && (!hadOld || old == now) {
		return Delta{Object: object}
	}
	return Delta{Object: object, Changed: true, HadOld: hadOld, Old: old, HasNew: hasNow, New: now}
}

func (e *Engine) unchanged(object string) Delta { return Delta{Object: object} }

// Verify 用全候选排序模型从头重算，核对增量维护的状态。
// 返回不一致项描述，空切片表示完全一致。
func (e *Engine) Verify() []string {
	var problems []string

	// 按对象聚合全部存活候选并全量排序，作为参考模型。
	want := make(map[string][]Event)
	for _, ev := range e.events {
		if _, dead := e.retracted[ev.ID]; dead {
			continue
		}
		want[ev.Object] = append(want[ev.Object], ev)
	}
	for obj := range want {
		sort.Slice(want[obj], func(i, j int) bool { return better(want[obj][i], want[obj][j]) })
	}

	// 核对 1：增量候选集与全量模型逐位一致（暴露重复候选、遗漏、顺序错误）。
	objects := make(map[string]bool)
	for obj := range e.active {
		objects[obj] = true
	}
	for obj := range want {
		objects[obj] = true
	}
	for obj := range objects {
		got, exp := e.active[obj], want[obj]
		if len(got) != len(exp) {
			problems = append(problems, fmt.Sprintf("对象 %s 候选数不一致: 增量=%d 全量=%d", obj, len(got), len(exp)))
			continue
		}
		for i := range exp {
			if got[i] != exp[i] {
				problems = append(problems, fmt.Sprintf("对象 %s 第 %d 位候选不一致: 增量=%s 全量=%s", obj, i, got[i].ID, exp[i].ID))
			}
		}
	}

	// 核对 2：有效值与全量模型队首一致。
	for obj := range objects {
		eff, has := e.effective[obj], e.hasEff[obj]
		exp := want[obj]
		switch {
		case has && len(exp) == 0:
			problems = append(problems, fmt.Sprintf("对象 %s 有效值应为空，实际为 %s", obj, eff.ID))
		case !has && len(exp) > 0:
			problems = append(problems, fmt.Sprintf("对象 %s 有效值应为 %s，实际为空", obj, exp[0].ID))
		case has && eff != exp[0]:
			problems = append(problems, fmt.Sprintf("对象 %s 有效值应为 %s，实际为 %s", obj, exp[0].ID, eff.ID))
		}
	}
	return problems
}
