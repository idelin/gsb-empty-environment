// Package graceful 实现节点优雅离开（Graceful Leave）协议：
// 节点主动广播离开意向，进入 Leaving 状态；各节点在平静期
// （Quiescent Period）内将其移出就绪路由池、阻断新流量，
// 待定时器超时后彻底物理销毁路由条目。
package graceful

// State 表示节点/路由条目的生命周期状态。
type State int32

const (
	// StateAlive 正常服务，位于就绪路由池中。
	StateAlive State = iota
	// StateLeaving 已广播离开意向，处于平静期：阻断新流量，允许在途请求完成。
	StateLeaving
	// StateDead 平静期结束，路由条目被物理销毁。
	StateDead
)

func (s State) String() string {
	switch s {
	case StateAlive:
		return "Alive"
	case StateLeaving:
		return "Leaving"
	case StateDead:
		return "Dead"
	default:
		return "Unknown"
	}
}
