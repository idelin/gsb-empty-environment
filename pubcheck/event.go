package pubcheck

import (
	"fmt"
	"time"
)

// GoroutineID 标识一次执行轨迹中的一个 goroutine。
type GoroutineID string

// ObjectID 标识被发布的惰性初始化对象。
type ObjectID string

// FieldID 标识对象上的某个敏感字段。
type FieldID string

// SyncID 标识一个同步原语（通道、WaitGroup、互斥锁等）。
type SyncID string

// Generation 标识对象的代次。对象被回收并重新使用时必须使用新的代次，
// 不同代次上的字段写与读取在先行发生图中彼此隔离。
type Generation int64

// Kind 是事件类型。事件显式区分字段写、指针发布、获取侧读取与释放侧同步，
// 不能用同一个“内存访问”事件笼统表达。
type Kind int

const (
	KindInvalid Kind = iota
	// KindFieldWrite 对对象字段的普通写，例如 obj.secret = v。
	KindFieldWrite
	// KindPublish 指针发布写，例如 shared = obj。它本身只是一次内存写，
	// 不会凭空产生同步关系，必须配合释放侧同步事件才能建立同步。
	KindPublish
	// KindAcquireRead 获取侧读取，例如 obj := shared。它本身只是一次内存读，
	// 必须配合获取侧同步事件才能与释放侧建立同步。
	KindAcquireRead
	// KindReleaseSync 释放侧同步事件，例如 close(ch)、wg.Done()、mu.Unlock()。
	KindReleaseSync
	// KindAcquireSync 获取侧同步事件，例如 <-ch、wg.Wait()、mu.Lock()。
	KindAcquireSync
)

// String 返回事件类型的稳定名称。
func (k Kind) String() string {
	switch k {
	case KindFieldWrite:
		return "FieldWrite"
	case KindPublish:
		return "Publish"
	case KindAcquireRead:
		return "AcquireRead"
	case KindReleaseSync:
		return "ReleaseSync"
	case KindAcquireSync:
		return "AcquireSync"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Ref 以“对象 + 代次”的方式唯一定位一个具体对象实例。
// 同一 ObjectID 在不同 Generation 上的访问互不可见，避免代次重用导致的串扰。
type Ref struct {
	Object     ObjectID
	Generation Generation
}

// String 返回引用的可读表示。
func (r Ref) String() string {
	return fmt.Sprintf("%s#g%d", r.Object, r.Generation)
}

// Event 是发布检查轨迹中的一个事件。
//
// Wall 仅用于宿主记录现实时间与排查问题，构造先行发生图时会被完全忽略：
// 现实时间上的先后不构成同步，只有程序顺序与显式同步事件才建立先行发生关系。
type Event struct {
	Seq       int
	Kind      Kind
	Goroutine GoroutineID
	Ref       Ref
	Field     FieldID
	Sync      SyncID
	Wall      time.Time
	Tag       string
}

// Label 返回用于报告的事件标签。
func (e Event) Label() string {
	if e.Tag != "" {
		return e.Tag
	}
	switch e.Kind {
	case KindReleaseSync, KindAcquireSync:
		return fmt.Sprintf("E%d(%s %s/%s)", e.Seq, e.Kind, e.Goroutine, e.Sync)
	default:
		if e.Field != "" {
			return fmt.Sprintf("E%d(%s %s %s.%s)", e.Seq, e.Kind, e.Goroutine, e.Ref, e.Field)
		}
		return fmt.Sprintf("E%d(%s %s %s)", e.Seq, e.Kind, e.Goroutine, e.Ref)
	}
}

// SyncMode 决定同一个 SyncID 上释放事件与获取事件的配对方式。
type SyncMode int

const (
	// SyncChannel 表示通道式 1:1 FIFO 配对：一次释放至多唤醒一次获取，
	// 顺序严格按事件在轨迹中的登记次序匹配，与 Wall 时间无关。
	SyncChannel SyncMode = iota
	// SyncBarrier 表示屏障式配对（如 WaitGroup）：释放侧全部登记后，
	// 每一次获取与屏障之前的每一次释放都建立同步边。
	SyncBarrier
)

// Checker 收集执行轨迹事件并构造先行发生图。
// 零值不可直接使用，请通过 NewChecker 创建。
type Checker struct {
	events []Event
	syncs  map[SyncID]SyncMode
}

// NewChecker 创建一个空的轨迹检查器。
func NewChecker() *Checker {
	return &Checker{syncs: map[SyncID]SyncMode{}}
}

// NewSync 登记一个同步原语及其配对模式。重复登记以最后一次为准。
func (c *Checker) NewSync(id SyncID, mode SyncMode) {
	c.syncs[id] = mode
}

// Emit 追加一个事件并返回带有序号的完整事件。Seq 按登记次序单调递增，
// 该次序只代表轨迹记录顺序，不代表任何同步语义。
func (c *Checker) Emit(ev Event) Event {
	if c.syncs == nil {
		c.syncs = map[SyncID]SyncMode{}
	}
	ev.Seq = len(c.events)
	c.events = append(c.events, ev)
	return ev
}

// FieldWrite 记录对象字段写。
func (c *Checker) FieldWrite(g GoroutineID, ref Ref, field FieldID) Event {
	return c.Emit(Event{Kind: KindFieldWrite, Goroutine: g, Ref: ref, Field: field})
}

// Publish 记录指针发布写。
func (c *Checker) Publish(g GoroutineID, ref Ref) Event {
	return c.Emit(Event{Kind: KindPublish, Goroutine: g, Ref: ref})
}

// AcquireRead 记录获取侧对已发布指针的读取。
func (c *Checker) AcquireRead(g GoroutineID, ref Ref) Event {
	return c.Emit(Event{Kind: KindAcquireRead, Goroutine: g, Ref: ref})
}

// ReleaseSync 记录释放侧同步事件。
func (c *Checker) ReleaseSync(g GoroutineID, sync SyncID) Event {
	return c.Emit(Event{Kind: KindReleaseSync, Goroutine: g, Sync: sync})
}

// AcquireSync 记录获取侧同步事件。未登记的同步原语默认按通道模式配对。
func (c *Checker) AcquireSync(g GoroutineID, sync SyncID) Event {
	if _, ok := c.syncs[sync]; !ok {
		c.syncs[sync] = SyncChannel
	}
	return c.Emit(Event{Kind: KindAcquireSync, Goroutine: g, Sync: sync})
}

// Events 返回已登记事件的副本。
func (c *Checker) Events() []Event {
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}
