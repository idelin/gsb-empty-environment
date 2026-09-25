package mutex

// State 是对锁当前状态的诊断性分类。
type State int

const (
	// StateFree：无持有者（记录不存在或已释放）。
	StateFree State = iota
	// StateLive：持有者进程存活、身份匹配且租约未过期。
	StateLive
	// StateLost（失联）：持有者进程仍存活，但租约已过期——
	// 它可能挂起或时钟异常，无法证明其仍在正常工作，允许被接管。
	StateLost
	// StateStale（陈旧）：持有者进程已退出、PID 被复用，
	// 或记录指向的进程实例已不存在。
	StateStale
)

func (s State) String() string {
	switch s {
	case StateFree:
		return "free"
	case StateLive:
		return "live"
	case StateLost:
		return "lost"
	case StateStale:
		return "stale"
	}
	return "unknown"
}
