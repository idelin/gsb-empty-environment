package matcher

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// bufferedEvent 在事件之外记录到达序号，用于事件时间相同时的稳定排序。
type bufferedEvent struct {
	Event
	seq int64
}

// nodeState 是一条匹配分支在两次求值之间的内部状态。
//
// 一个节点代表规则前 depth+1 个段已经处理完毕的某条绑定路径：正段逐一绑定
// 事件，否定段要么仍在等待水位线确认（节点保持暂定），要么已被违反（节点
// 进入 dead 集合）。
type nodeState struct {
	depth        int
	bound        map[string]Event
	evidence     []string
	lastSeq      int64
	complete     bool
	unconfirmed  int
	internalTag  string
	violatedBy   *bufferedEvent
	violatedStep string
	expiredAt    time.Time
	expiredStep  string
}

func (n *nodeState) matchID(ruleID string) string {
	id := ruleID + ":" + strings.Join(n.evidence, "|")
	if n.internalTag != "" {
		id += "#" + n.internalTag
	}
	return id
}

func (n *nodeState) anchorID() string {
	return n.evidence[0]
}

func (n *nodeState) isDead() bool {
	return n.violatedBy != nil || !n.expiredAt.IsZero()
}

func (n *nodeState) snapshot(ruleID string) Match {
	m := Match{
		MatchID:  ruleID + ":" + strings.Join(n.evidence, "|"),
		RuleID:   ruleID,
		Evidence: append([]string(nil), n.evidence...),
	}
	switch {
	case n.violatedBy != nil:
		m.Status = StatusRevoked
		m.RevokedBy = n.violatedBy.ID
	case !n.expiredAt.IsZero():
		m.Status = StatusExpired
	case n.complete && n.unconfirmed == 0:
		m.Status = StatusAlerted
	default:
		m.Status = StatusTentative
	}
	return m
}

// snapshotAs 以自身死因生成快照，但对外暴露目标节点（通常是无死因前缀）
// 的匹配 ID 与证据列表，保证撤销前后调用方看到的是同一个实例 ID。
func (n *nodeState) snapshotAs(ruleID string, target *nodeState) Match {
	m := n.snapshot(ruleID)
	m.MatchID = target.matchID(ruleID)
	m.Evidence = append([]string(nil), target.evidence...)
	return m
}

func (n *nodeState) clone() *nodeState {
	cp := &nodeState{
		depth:        n.depth,
		bound:        cloneBound(n.bound),
		evidence:     append([]string(nil), n.evidence...),
		lastSeq:      n.lastSeq,
		complete:     n.complete,
		unconfirmed:  n.unconfirmed,
		internalTag:  n.internalTag,
		violatedStep: n.violatedStep,
		expiredAt:    n.expiredAt,
		expiredStep:  n.expiredStep,
	}
	if n.violatedBy != nil {
		ev := *n.violatedBy
		cp.violatedBy = &ev
	}
	return cp
}

func cloneBound(in map[string]Event) map[string]Event {
	out := make(map[string]Event, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// inWindow 判断 t 是否落在左闭右开区间 [lo, hi) 内。
func inWindow(t, lo, hi time.Time) bool {
	return !t.Before(lo) && t.Before(hi)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// Engine 是单条规则的事件时间匹配引擎。
//
// 引擎维护已接收事件的事件时间缓冲、单调水位线、当前存活的匹配节点以及
// 已经定案（撤销/过期）的节点死因表。零值不可用，请使用 NewEngine 构造。
type Engine struct {
	rule      Rule
	buffer    []bufferedEvent
	seen      map[string]struct{}
	watermark time.Time
	nextSeq   int64

	live map[string]*nodeState
	dead map[string]*nodeState
}

// NewEngine 校验规则并构造匹配引擎。
func NewEngine(rule Rule) (*Engine, error) {
	if err := rule.Validate(); err != nil {
		return nil, err
	}
	return &Engine{
		rule: rule,
		seen: map[string]struct{}{},
		live: map[string]*nodeState{},
		dead: map[string]*nodeState{},
	}, nil
}

// Watermark 返回当前水位线；尚未推进过时返回零值时间。
func (e *Engine) Watermark() time.Time {
	return e.watermark
}

// ProcessEvent 接收一个事件并基于事件时间增量求值。
//
// 事件时间不晚于当前水位线时判定为迟到事件：引擎状态不变，返回的
// RejectedEvent 非空并附带拒绝原因；事件 ID 重复或字段非法时返回错误。
// 正常接收时返回本次事件引发的全部状态变更输出（撤销、过期、暂定或告警）。
func (e *Engine) ProcessEvent(ev Event) ([]Output, *RejectedEvent, error) {
	if ev.ID == "" {
		return nil, nil, errors.New("事件 ID 不能为空")
	}
	if ev.Type == "" {
		return nil, nil, fmt.Errorf("事件 %q 的类型不能为空", ev.ID)
	}
	if ev.At.IsZero() {
		return nil, nil, fmt.Errorf("事件 %q 的事件时间不能为零值", ev.ID)
	}
	if !e.watermark.IsZero() && !ev.At.After(e.watermark) {
		reason := fmt.Sprintf(
			"迟到事件 %s 已拒绝：事件时间 %s 不晚于当前水位线 %s",
			ev.ID, formatTime(ev.At), formatTime(e.watermark),
		)
		return nil, &RejectedEvent{Event: ev, Reason: reason}, nil
	}
	if _, dup := e.seen[ev.ID]; dup {
		return nil, nil, fmt.Errorf("事件 ID %q 重复", ev.ID)
	}

	e.nextSeq++
	e.buffer = append(e.buffer, bufferedEvent{Event: ev, seq: e.nextSeq})
	e.seen[ev.ID] = struct{}{}
	sort.SliceStable(e.buffer, func(i, j int) bool {
		if e.buffer[i].At.Equal(e.buffer[j].At) {
			return e.buffer[i].seq < e.buffer[j].seq
		}
		return e.buffer[i].At.Before(e.buffer[j].At)
	})

	return e.refresh(), nil, nil
}

// AdvanceWatermark 把水位线推进到 t。
//
// 水位线必须单调递增：传入零值或不晚于当前水位线的时刻会返回错误且不改变
// 引擎状态。推进后引擎会重新求值：确认到期的否定段、关闭到期的正段窗口，
// 并返回由此产生的状态变更输出。
func (e *Engine) AdvanceWatermark(t time.Time) ([]Output, error) {
	if t.IsZero() {
		return nil, errors.New("水位线不能为零值时间")
	}
	if !e.watermark.IsZero() && !t.After(e.watermark) {
		return nil, fmt.Errorf(
			"水位线只能单调递增：当前 %s，拒绝回退到 %s",
			formatTime(e.watermark), formatTime(t),
		)
	}
	e.watermark = t
	return e.refresh(), nil
}

// Matches 返回引擎已知的全部匹配实例快照，按匹配 ID 排序。
//
// 其中既包含存活的暂定/告警实例，也包含已经撤销或过期的历史实例，便于
// 调用方核对撤销前的暂定状态。
func (e *Engine) Matches() []Match {
	out := make([]Match, 0, len(e.live)+len(e.dead))
	for _, n := range e.live {
		out = append(out, n.snapshot(e.rule.ID))
	}
	for _, n := range e.dead {
		m := n.snapshot(e.rule.ID)
		if !n.isDead() {
			if cause := e.findDeathCause(n); cause != nil {
				m = cause.snapshotAs(e.rule.ID, n)
			}
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MatchID < out[j].MatchID })
	return out
}

// Match 按匹配 ID 查询单个实例；不存在时第二个返回值为 false。
func (e *Engine) Match(matchID string) (Match, bool) {
	if n, ok := e.live[matchID]; ok {
		return n.snapshot(e.rule.ID), true
	}
	if n, ok := e.dead[matchID]; ok {
		if !n.isDead() {
			if cause := e.findDeathCause(n); cause != nil {
				return cause.snapshotAs(e.rule.ID, n), true
			}
		}
		return n.snapshot(e.rule.ID), true
	}
	return Match{}, false
}
