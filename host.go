package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Mode 选择宿主关闭语义。
type Mode int

const (
	// ModeReviewed 是修订后的语义：Close 先冻结句柄列表并从对象上清空，
	// 再触发插件回调与 Release，回调期间所有权校验必然失败。
	ModeReviewed Mode = iota
	// ModePendingReview 是待审版本：“先回调再清空”，关闭期间句柄仍挂在
	// 对象上，且 MoveTo/Call 只做所有权校验，存在重入二次释放风险。
	ModePendingReview
)

// ViolationKind 是宿主在模拟中检测到的违规类型。
type ViolationKind int

const (
	// ViolationDoubleRelease 表示同一句柄被 Release 超过一次。
	ViolationDoubleRelease ViolationKind = iota
	// ViolationCallAfterClosing 表示对象进入 Closing 后 Call 仍然成功。
	ViolationCallAfterClosing
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationDoubleRelease:
		return "DoubleRelease"
	case ViolationCallAfterClosing:
		return "CallAfterClosing"
	}
	return "Unknown"
}

// Violation 记录一次违规及其发生时的完整重入栈。
type Violation struct {
	Kind   ViolationKind
	Detail string
	Stack  []string
}

// Plugin 是插件回调接口。BeforeRelease 仅由宿主模拟器在每次 Release 前
// 触发；OnClosed 是关闭完成后的一次性只读通知。
type Plugin interface {
	BeforeRelease(cb *Callback)
	OnClosed(cb *Callback)
}

type traceEntry struct {
	depth int
	msg   string
}

// Host 是插件宿主模拟器。模拟为单线程执行：插件回调与宿主调用在同一
// 协程内重入，状态转换由互斥锁保证原子性。
type Host struct {
	mu        sync.Mutex
	mode      Mode
	plugin    Plugin
	releaseFn func(Handle) error

	objects      map[string]*Object
	nextHandleID int
	releases     map[int]int // 句柄 ID -> Release 次数，修订版保证至多一次

	stack      []string // 当前重入栈
	trace      []traceEntry
	violations []Violation
}

// New 构造修订版语义的宿主。
func New(releaseFn func(Handle) error, plugin Plugin) *Host {
	return newHost(ModeReviewed, releaseFn, plugin)
}

// NewPendingReview 构造待审的“先回调再清空”版本宿主。
func NewPendingReview(releaseFn func(Handle) error, plugin Plugin) *Host {
	return newHost(ModePendingReview, releaseFn, plugin)
}

func newHost(mode Mode, releaseFn func(Handle) error, plugin Plugin) *Host {
	return &Host{
		mode:      mode,
		plugin:    plugin,
		releaseFn: releaseFn,
		objects:   make(map[string]*Object),
		releases:  make(map[int]int),
	}
}

// NewObject 创建一个 Open 状态的扩展对象。
func (h *Host) NewObject(name string) *Object {
	h.mu.Lock()
	defer h.mu.Unlock()
	o := &Object{host: h, name: name, state: StateOpen}
	h.objects[name] = o
	return o
}

// Object 按名字查找对象。
func (h *Host) Object(name string) *Object {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.objects[name]
}

// AddHandle 为 Open 对象分配一个绑定当前代次的资源句柄。
func (h *Host) AddHandle(o *Object) (Handle, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if o.state != StateOpen {
		return Handle{}, ErrNotOpen
	}
	if len(o.handles) >= MaxHandles {
		return Handle{}, ErrTooManyHandles
	}
	h.nextHandleID++
	hd := Handle{ID: h.nextHandleID, Gen: o.gen}
	o.handles = append(o.handles, hd)
	return hd, nil
}

// Close 关闭对象。从 Open 原子转为 Closing；重复 Close 幂等：
// 关闭完成后返回首次关闭的聚合结果，关闭进行中重入返回 ErrCloseInProgress。
func (h *Host) Close(o *Object) error {
	if h.mode == ModePendingReview {
		return h.closePendingReview(o)
	}
	return h.closeReviewed(o)
}

// closeReviewed 修订版：先冻结并清空句柄，再回调与 Release。
func (h *Host) closeReviewed(o *Object) error {
	h.mu.Lock()
	switch o.state {
	case StateClosed:
		err := o.closeErr
		h.mu.Unlock()
		return err // 幂等返回原结果
	case StateClosing:
		h.mu.Unlock()
		return ErrCloseInProgress // 重入为空操作，不重复执行
	}
	o.state = StateClosing
	// 关键顺序：先冻结句柄列表并立即从对象上清空，代次递增使旧句柄失效。
	o.frozen = append([]Handle(nil), o.handles...)
	o.handles = nil
	o.gen++
	frozen := append([]Handle(nil), o.frozen...)
	h.mu.Unlock()

	h.push(fmt.Sprintf("Close(%s)", o.name))
	var errs []error
	for _, hd := range frozen {
		h.fireBeforeRelease(o, hd)
		if err := h.releaseOnce(o, hd); err != nil {
			errs = append(errs, fmt.Errorf("release handle #%d: %w", hd.ID, err))
		}
	}
	agg := errors.Join(errs...)

	h.mu.Lock()
	o.state = StateClosed // 全部 Release 尝试结束后先置 Closed
	o.closeErr = agg
	h.mu.Unlock()

	h.fireOnClosed(o) // 再调用一次 OnClosed 只读通知
	h.pop()
	return agg
}

// releaseOnce 修订版 Release：宿主记录每个句柄最多一次 Release。
func (h *Host) releaseOnce(o *Object, hd Handle) error {
	h.mu.Lock()
	if h.releases[hd.ID] > 0 {
		h.mu.Unlock()
		h.logf("Release(h%d) 被抑制：宿主对每个句柄最多 Release 一次", hd.ID)
		return nil
	}
	h.releases[hd.ID]++
	h.mu.Unlock()
	return h.doRelease(hd)
}

func (h *Host) doRelease(hd Handle) error {
	h.push(fmt.Sprintf("Release(h%d)", hd.ID))
	defer h.pop()
	if h.releaseFn == nil {
		return nil
	}
	return h.releaseFn(hd)
}

// MoveTo 将句柄从源对象转移到目标对象。
func (h *Host) MoveTo(src, dst *Object, hd Handle) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if src == dst {
		return ErrSelfMove
	}
	if dst.state != StateOpen {
		return ErrNotOpen
	}
	if len(dst.handles) >= MaxHandles {
		return ErrTooManyHandles
	}
	if h.mode == ModeReviewed {
		// 代次 → 源状态 → 所有权 → 冻结列表，任一不满足即拒绝。
		if hd.Gen != src.gen {
			return ErrStaleGeneration
		}
		if src.state != StateOpen {
			return ErrNotOpen
		}
		if !ownsHandle(src, hd.ID) {
			return ErrHandleNotOwned
		}
		if containsHandle(src.frozen, hd.ID) {
			return ErrHandleFrozen
		}
	} else {
		// 待审版本：仅校验所有权，遗漏源状态与冻结列表检查。
		if !ownsHandle(src, hd.ID) {
			return ErrHandleNotOwned
		}
	}
	removeHandle(src, hd.ID)
	dst.handles = append(dst.handles, Handle{ID: hd.ID, Gen: dst.gen})
	return nil
}

// Call 模拟对句柄的调用。
func (h *Host) Call(o *Object, hd Handle) error {
	h.mu.Lock()
	if h.mode == ModeReviewed {
		if hd.Gen != o.gen {
			h.mu.Unlock()
			return ErrStaleGeneration
		}
		if o.state != StateOpen {
			h.mu.Unlock()
			return ErrNotOpen
		}
		if !ownsHandle(o, hd.ID) {
			h.mu.Unlock()
			return ErrHandleNotOwned
		}
	} else {
		// 待审版本：仅校验所有权，句柄未清空前 Closing 对象上仍可通过。
		if !ownsHandle(o, hd.ID) {
			h.mu.Unlock()
			return ErrHandleNotOwned
		}
	}
	state := o.state
	h.mu.Unlock()

	h.push(fmt.Sprintf("Call(h%d)", hd.ID))
	defer h.pop()
	if state != StateOpen {
		h.recordViolation(ViolationCallAfterClosing,
			fmt.Sprintf("对象 %s 处于 %s 时 Call(h%d) 成功", o.name, state, hd.ID))
	}
	return nil
}

func (h *Host) fireBeforeRelease(o *Object, hd Handle) {
	if h.plugin == nil {
		return
	}
	h.push(fmt.Sprintf("BeforeRelease(%s, h%d)", o.name, hd.ID))
	h.plugin.BeforeRelease(&Callback{host: h, obj: o, handle: hd, hasHandle: true})
	h.pop()
}

func (h *Host) fireOnClosed(o *Object) {
	h.mu.Lock()
	if o.onClosedFired {
		h.mu.Unlock()
		return
	}
	o.onClosedFired = true
	h.mu.Unlock()
	if h.plugin == nil {
		return
	}
	h.push(fmt.Sprintf("OnClosed(%s)", o.name))
	h.plugin.OnClosed(&Callback{host: h, obj: o, readOnly: true})
	h.pop()
}

func (h *Host) recordViolation(kind ViolationKind, detail string) {
	h.violations = append(h.violations, Violation{
		Kind:   kind,
		Detail: detail,
		Stack:  append([]string(nil), h.stack...),
	})
	h.logf("!! 违规[%s]: %s", kind, detail)
}

func (h *Host) push(frame string) {
	h.logf("%s", frame)
	h.stack = append(h.stack, frame)
}

func (h *Host) pop() {
	h.stack = h.stack[:len(h.stack)-1]
}

func (h *Host) logf(format string, args ...any) {
	h.trace = append(h.trace, traceEntry{
		depth: len(h.stack),
		msg:   fmt.Sprintf(format, args...),
	})
}

// ReleaseCount 返回句柄被 Release 的次数。
func (h *Host) ReleaseCount(id int) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.releases[id]
}

// Violations 返回模拟期间检测到的违规。
func (h *Host) Violations() []Violation {
	return append([]Violation(nil), h.violations...)
}

// Trace 返回按重入深度缩进的完整调用轨迹。
func (h *Host) Trace() string {
	var b strings.Builder
	for _, e := range h.trace {
		b.WriteString(strings.Repeat("  ", e.depth))
		b.WriteString(e.msg)
		b.WriteByte('\n')
	}
	return b.String()
}

// Callback 是宿主传给插件回调的门面，插件只能经由它发起请求。
type Callback struct {
	host      *Host
	obj       *Object
	handle    Handle
	hasHandle bool
	readOnly  bool // OnClosed 只读通知
}

// Object 返回正在关闭（或已关闭）的对象。
func (c *Callback) Object() *Object { return c.obj }

// Handle 返回即将被 Release 的句柄；OnClosed 中不存在。
func (c *Callback) Handle() (Handle, bool) { return c.handle, c.hasHandle }

// Close 请求关闭对象，幂等。
func (c *Callback) Close(o *Object) error {
	err := c.host.Close(o)
	c.host.logf("插件请求 Close(%s) -> %v", o.name, err)
	return err
}

// MoveTo 请求把句柄从当前对象转移到 dst；只读通知中拒绝。
func (c *Callback) MoveTo(hd Handle, dst *Object) error {
	if c.readOnly {
		c.host.logf("插件请求 MoveTo(h%d -> %s) -> %v", hd.ID, dst.name, ErrCallbackReadOnly)
		return ErrCallbackReadOnly
	}
	err := c.host.MoveTo(c.obj, dst, hd)
	c.host.logf("插件请求 MoveTo(h%d -> %s) -> %v", hd.ID, dst.name, err)
	return err
}

// Call 请求对句柄发起调用；只读通知中拒绝。
func (c *Callback) Call(hd Handle) error {
	if c.readOnly {
		c.host.logf("插件请求 Call(h%d) -> %v", hd.ID, ErrCallbackReadOnly)
		return ErrCallbackReadOnly
	}
	err := c.host.Call(c.obj, hd)
	c.host.logf("插件请求 Call(h%d) -> %v", hd.ID, err)
	return err
}
