package graceful

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrNotReady 目标不在调用方的就绪路由池中：本地快速失败，不产生 RPC 超时。
	ErrNotReady = errors.New("graceful: target not in ready pool")
	// ErrDraining 目标处于平静期，拒绝新流量（在途请求不受影响）。
	ErrDraining = errors.New("graceful: target is draining")
	// ErrGone 目标路由条目已物理销毁。
	ErrGone = errors.New("graceful: target is gone")
)

// Node 是集群中的一个节点，持有自身状态与指向对端的路由表。
type Node struct {
	ID     string
	Router *RouteTable

	state   int32 // State，原子访问
	cluster *Cluster

	mu       sync.Mutex
	inflight int // 正在处理的在途 RPC 数
	drained  chan struct{}
}

func newNode(id string, quiescent time.Duration) *Node {
	return &Node{
		ID:      id,
		Router:  NewRouteTable(quiescent),
		drained: make(chan struct{}),
	}
}

// State 返回节点当前状态。
func (n *Node) State() State {
	return State(atomic.LoadInt32(&n.state))
}

// Leave 由节点主动发起：自身置为 Leaving（停止接收新流量），
// 并向集群广播离开意向；随后等待在途请求排空，最终转为 Dead。
func (n *Node) Leave() {
	if !atomic.CompareAndSwapInt32(&n.state, int32(StateAlive), int32(StateLeaving)) {
		return
	}
	n.cluster.broadcastLeave(n.ID)

	// 异步等待在途请求排空后转为 Dead。
	go func() {
		n.waitDrained()
		atomic.StoreInt32(&n.state, int32(StateDead))
	}()

	n.mu.Lock()
	if n.inflight == 0 {
		n.signalDrainedLocked()
	}
	n.mu.Unlock()
}

// waitDrained 阻塞直到在途请求数降为 0。
func (n *Node) waitDrained() {
	<-n.drained
}

func (n *Node) signalDrainedLocked() {
	select {
	case <-n.drained:
	default:
		close(n.drained)
	}
}

// serve 处理一个进入的 RPC：Alive 时接受并计入在途；
// Leaving/Dead 时拒绝新流量。
func (n *Node) serve(work func()) error {
	n.mu.Lock()
	if State(atomic.LoadInt32(&n.state)) != StateAlive {
		n.mu.Unlock()
		if n.State() == StateLeaving {
			return ErrDraining
		}
		return ErrGone
	}
	n.inflight++
	n.mu.Unlock()

	work()

	n.mu.Lock()
	n.inflight--
	if n.inflight == 0 && n.State() != StateAlive {
		n.signalDrainedLocked()
	}
	n.mu.Unlock()
	return nil
}
