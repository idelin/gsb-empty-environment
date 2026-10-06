// Package desim 是一个离散事件仿真内核。
//
// 事件键为 (时间, 阶段, 插入序号)，按字典序严格有序。
// 同一时刻的新事件不得插入到已经执行完的更早阶段，违规会被显式拒绝。
// 事件可以取消、可以生成因果后继；取消已执行事件会失败。
// 支持事件撤销与检查点回滚：撤销已执行事件会级联撤销其全部因果后继，
// 然后从最近的有效检查点重放日志；外部已确认效果不可撤销，只产生补偿要求。
package desim

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// AlreadyExecutedError 表示试图取消一个已经执行完的事件。
type AlreadyExecutedError struct{ Event *Event }

func (e *AlreadyExecutedError) Error() string {
	return fmt.Sprintf("desim: cannot cancel already executed event %s", e.Event)
}

// ErrNotFound 表示事件 ID 不存在。
var ErrNotFound = errors.New("desim: event not found")

// Time 是仿真的逻辑时钟。
type Time int64

// Key 是事件的全序键：(时间, 阶段, 插入序号)。
type Key struct {
	Time  Time
	Phase int
	Seq   uint64
}

// Less 按 (Time, Phase, Seq) 字典序比较。
func (k Key) Less(o Key) bool {
	if k.Time != o.Time {
		return k.Time < o.Time
	}
	if k.Phase != o.Phase {
		return k.Phase < o.Phase
	}
	return k.Seq < o.Seq
}

func (k Key) String() string {
	return fmt.Sprintf("(t=%d,p=%d,#%d)", k.Time, k.Phase, k.Seq)
}

// Status 是事件的生命周期状态。
type Status int

const (
	StatusPending   Status = iota // 在队列中等待执行
	StatusExecuted                // 已执行
	StatusCancelled               // 执行前被取消
	StatusUndone                  // 执行后被撤销
)

func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusExecuted:
		return "executed"
	case StatusCancelled:
		return "cancelled"
	case StatusUndone:
		return "undone"
	}
	return "unknown"
}

// Handler 是事件体。通过 ctx 访问/修改状态、生成后继事件。
// 重放期间 ctx.Replay 为 true：产生外部已确认效果的 Handler 应据此
// 跳过外部副作用，只做内部状态变更。
type Handler func(ctx *Context) error

// Event 是队列中的一个事件。
type Event struct {
	ID       uint64 // 等于 Key.Seq
	Key      Key
	Name     string
	External bool // 是否产生外部已确认效果（不可撤销，只能补偿）
	Status   Status

	parent   *Event
	children []*Event
	handler  Handler
}

func (e *Event) String() string {
	return fmt.Sprintf("%s%s", e.Name, e.Key)
}

// Parent 返回因果父事件（由哪个事件调度而来），根事件返回 nil。
func (e *Event) Parent() *Event { return e.parent }

// Children 返回该事件直接调度出的因果后继。
func (e *Event) Children() []*Event { return append([]*Event(nil), e.children...) }

// Context 是 Handler 的执行上下文。
type Context struct {
	sim    *Sim
	Event  *Event // 当前正在执行的事件
	Replay bool   // 是否处于回滚重放 / 校验重放中
}

// State 返回当前仿真状态（由 Handler 自行断言为具体类型后修改）。
func (c *Context) State() interface{} { return c.sim.state }

// Now 返回当前事件的逻辑时间。
func (c *Context) Now() Time { return c.Event.Key.Time }

// Schedule 调度一个因果后继：时间为当前事件时间 + delay，
// 该后继会被记录到当前事件的因果链上。
// 重放期间调度被抑制（后继已在日志中），返回 (nil, nil)。
func (c *Context) Schedule(name string, delay Time, phase int, h Handler) (*Event, error) {
	return c.schedule(name, delay, phase, false, h)
}

// ScheduleExternal 同 Schedule，但标记该事件产生外部已确认效果。
func (c *Context) ScheduleExternal(name string, delay Time, phase int, h Handler) (*Event, error) {
	return c.schedule(name, delay, phase, true, h)
}

func (c *Context) schedule(name string, delay Time, phase int, external bool, h Handler) (*Event, error) {
	if c.sim.replaying {
		return nil, nil
	}
	return c.sim.schedule(name, c.Event.Key.Time+delay, phase, external, h, c.Event)
}

type checkpoint struct {
	journalLen int
	state      interface{}
	key        Key
}

// Sim 是离散事件仿真器。
type Sim struct {
	state interface{}
	clone func(interface{}) interface{}
	equal func(a, b interface{}) bool

	seq     uint64
	last    Key // 最近执行事件的键（阶段守卫基准）
	queue   []*Event
	events  map[uint64]*Event
	journal []*Event

	checkpoints   []checkpoint
	replayLog     []ReplayEntry
	compensations []Compensation
	replaying     bool
}

// New 创建仿真器。initial 为初始状态；clone 用于检查点快照与回滚，
// 必须深拷贝状态；equal 用于全量核对，传 nil 则使用 reflect.DeepEqual。
func New(initial interface{}, clone func(interface{}) interface{}, equal func(a, b interface{}) bool) *Sim {
	if equal == nil {
		equal = func(a, b interface{}) bool { return reflect.DeepEqual(a, b) }
	}
	s := &Sim{
		state:  initial,
		clone:  clone,
		equal:  equal,
		events: make(map[uint64]*Event),
	}
	// 初始状态即第一个检查点。
	s.checkpoints = append(s.checkpoints, checkpoint{journalLen: 0, state: clone(initial)})
	return s
}

// State 返回当前仿真状态。
func (s *Sim) State() interface{} { return s.state }

// schedule 校验键序、分配序号并按序插入队列。
func (s *Sim) schedule(name string, t Time, phase int, external bool, h Handler, parent *Event) (*Event, error) {
	k := Key{Time: t, Phase: phase, Seq: s.seq}
	if err := s.checkKey(k); err != nil {
		return nil, err
	}
	s.seq++
	ev := &Event{
		ID:       k.Seq,
		Key:      k,
		Name:     name,
		External: external,
		Status:   StatusPending,
		handler:  h,
		parent:   parent,
	}
	if parent != nil {
		parent.children = append(parent.children, ev)
	}
	s.events[ev.ID] = ev
	// 有序列表插入：保持队列按 Key 升序。
	i := sort.Search(len(s.queue), func(i int) bool { return ev.Key.Less(s.queue[i].Key) })
	s.queue = append(s.queue, nil)
	copy(s.queue[i+1:], s.queue[i:])
	s.queue[i] = ev
	return ev, nil
}

// checkKey 阶段守卫：拒绝任何落入已执行完的更早时刻/更早阶段的键。
func (s *Sim) checkKey(k Key) error {
	if k.Time < s.last.Time || (k.Time == s.last.Time && k.Phase < s.last.Phase) {
		return fmt.Errorf("desim: rejected %v: would insert into already-executed phase (last executed %v)", k, s.last)
	}
	return nil
}

// Schedule 调度一个根事件（无因果父）。
func (s *Sim) Schedule(name string, t Time, phase int, h Handler) (*Event, error) {
	return s.schedule(name, t, phase, false, h, nil)
}

// ScheduleExternal 调度一个产生外部已确认效果的根事件。
func (s *Sim) ScheduleExternal(name string, t Time, phase int, h Handler) (*Event, error) {
	return s.schedule(name, t, phase, true, h, nil)
}

// Get 按 ID 查询事件。
func (s *Sim) Get(id uint64) (*Event, bool) {
	ev, ok := s.events[id]
	return ev, ok
}

// Cancel 取消一个待执行事件。取消已执行事件返回 ErrAlreadyExecuted。
func (s *Sim) Cancel(id uint64) error {
	ev, ok := s.events[id]
	if !ok {
		return ErrNotFound
	}
	switch ev.Status {
	case StatusExecuted:
		return &AlreadyExecutedError{Event: ev}
	case StatusPending:
		ev.Status = StatusCancelled
	}
	return nil
}

// Step 执行队首事件，队列为空时返回 false。
func (s *Sim) Step() bool {
	for len(s.queue) > 0 {
		ev := s.queue[0]
		s.queue = s.queue[1:]
		if ev.Status != StatusPending {
			continue // 已取消/已撤销，惰性跳过
		}
		s.execute(ev, false)
		return true
	}
	return false
}

// Run 运行至队列耗尽（空队列结束）。
func (s *Sim) Run() {
	for s.Step() {
	}
}

// execute 执行事件并记入日志。
func (s *Sim) execute(ev *Event, replay bool) {
	s.last = ev.Key
	ev.Status = StatusExecuted
	s.journal = append(s.journal, ev)
	ctx := &Context{sim: s, Event: ev, Replay: replay}
	if err := ev.handler(ctx); err != nil {
		panic(fmt.Sprintf("desim: handler of %s failed: %v", ev, err))
	}
}

// Checkpoint 在当前位置记录状态快照，供后续回滚使用。
// 应在事件之间（Step/Run 之外）调用。
func (s *Sim) Checkpoint() {
	if s.replaying {
		return
	}
	s.checkpoints = append(s.checkpoints, checkpoint{
		journalLen: len(s.journal),
		state:      s.clone(s.state),
		key:        s.last,
	})
}
