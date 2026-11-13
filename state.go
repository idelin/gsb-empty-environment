package netsec

import "errors"

// State 是扩展对象的生命周期状态。
type State int

const (
	StateOpen State = iota
	StateClosing
	StateClosed
)

func (s State) String() string {
	switch s {
	case StateOpen:
		return "Open"
	case StateClosing:
		return "Closing"
	case StateClosed:
		return "Closed"
	}
	return "Unknown"
}

// MaxHandles 是单个对象允许持有的资源句柄上限。
const MaxHandles = 4

// Handle 是宿主分配的资源句柄，绑定对象代次（Gen）。
// 对象每次进入 Closing 时代次递增，旧代次句柄随即失效。
type Handle struct {
	ID  int
	Gen uint64
}

var (
	// ErrNotOpen 表示对象不处于 Open 状态，请求被拒绝。
	ErrNotOpen = errors.New("object is not open")
	// ErrCloseInProgress 表示 Close 重入：关闭正在进行，本次为空操作。
	ErrCloseInProgress = errors.New("close already in progress")
	// ErrHandleNotOwned 表示句柄当前不归该对象所有。
	ErrHandleNotOwned = errors.New("handle not owned by object")
	// ErrStaleGeneration 表示句柄绑定的是旧代次，已失效。
	ErrStaleGeneration = errors.New("handle bound to stale generation")
	// ErrHandleFrozen 表示句柄已进入本次关闭的冻结列表。
	ErrHandleFrozen = errors.New("handle frozen by in-progress close")
	// ErrTooManyHandles 表示对象持有的句柄已达上限。
	ErrTooManyHandles = errors.New("object holds the maximum number of handles")
	// ErrSelfMove 表示源对象与目标对象相同。
	ErrSelfMove = errors.New("cannot move handle to the same object")
	// ErrCallbackReadOnly 表示在只读通知（OnClosed）中尝试变更操作。
	ErrCallbackReadOnly = errors.New("callback notification is read-only")
)

// Object 是插件宿主管理的扩展对象。
// 所有字段仅由所属 Host 在持锁或单线程模拟期间访问。
type Object struct {
	host  *Host
	name  string
	state State
	gen   uint64

	handles []Handle // 当前持有的句柄，至多 MaxHandles 个
	frozen  []Handle // Close 时冻结的句柄列表

	closeErr      error // 首次 Close 的聚合结果，之后的 Close 幂等返回
	onClosedFired bool  // OnClosed 只触发一次
}

// Name 返回对象名。
func (o *Object) Name() string { return o.name }

// State 返回对象当前状态。
func (o *Object) State() State {
	o.host.mu.Lock()
	defer o.host.mu.Unlock()
	return o.state
}

// Gen 返回对象当前代次。
func (o *Object) Gen() uint64 {
	o.host.mu.Lock()
	defer o.host.mu.Unlock()
	return o.gen
}

// Handles 返回对象当前持有的句柄副本。
func (o *Object) Handles() []Handle {
	o.host.mu.Lock()
	defer o.host.mu.Unlock()
	return append([]Handle(nil), o.handles...)
}

// Frozen 返回本次关闭冻结的句柄列表副本。
func (o *Object) Frozen() []Handle {
	o.host.mu.Lock()
	defer o.host.mu.Unlock()
	return append([]Handle(nil), o.frozen...)
}

func ownsHandle(o *Object, id int) bool {
	for _, hd := range o.handles {
		if hd.ID == id {
			return true
		}
	}
	return false
}

func containsHandle(list []Handle, id int) bool {
	for _, hd := range list {
		if hd.ID == id {
			return true
		}
	}
	return false
}

func removeHandle(o *Object, id int) {
	for i, hd := range o.handles {
		if hd.ID == id {
			o.handles = append(o.handles[:i], o.handles[i+1:]...)
			return
		}
	}
}
