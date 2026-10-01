package graceful

import (
	"sort"
	"sync"
	"time"
)

// route 是路由表中的一条条目。
type route struct {
	nodeID string
	state  State
	timer  *time.Timer // 平静期定时器，超时后物理销毁条目
}

// RouteTable 是单个节点维护的对端路由表。
// 收到 Leave 广播后，条目进入 Leaving：立即移出就绪池（阻断新流量），
// 并启动平静期定时器；超时后条目被物理删除。
type RouteTable struct {
	mu        sync.Mutex
	routes    map[string]*route
	quiescent time.Duration
	// afterFunc 可注入以便测试手动推进定时器；默认为 time.AfterFunc。
	afterFunc func(time.Duration, func()) *time.Timer
	// onRemove 在条目被物理销毁后回调（用于观测/测试）。
	onRemove func(nodeID string)
}

// NewRouteTable 创建路由表，quiescent 为平静期时长。
func NewRouteTable(quiescent time.Duration) *RouteTable {
	return &RouteTable{
		routes:    make(map[string]*route),
		quiescent: quiescent,
		afterFunc: time.AfterFunc,
	}
}

// Add 将节点加入路由表并置于就绪池。
func (rt *RouteTable) Add(nodeID string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, ok := rt.routes[nodeID]; ok {
		return
	}
	rt.routes[nodeID] = &route{nodeID: nodeID, state: StateAlive}
}

// MarkLeaving 处理收到的离开广播：条目转为 Leaving，
// 从就绪池消失，并启动平静期定时器，超时后物理销毁。
func (rt *RouteTable) MarkLeaving(nodeID string) {
	rt.mu.Lock()
	r, ok := rt.routes[nodeID]
	if !ok || r.state != StateAlive {
		rt.mu.Unlock()
		return
	}
	r.state = StateLeaving
	// 在锁内调度，避免定时器回调与赋值竞争；
	// time.AfterFunc 在独立 goroutine 中执行回调，不会死锁。
	r.timer = rt.afterFunc(rt.quiescent, func() {
		rt.Remove(nodeID)
	})
	rt.mu.Unlock()
}

// Remove 物理销毁路由条目（平静期超时或强制下线时调用）。
func (rt *RouteTable) Remove(nodeID string) {
	rt.mu.Lock()
	r, ok := rt.routes[nodeID]
	if ok {
		delete(rt.routes, nodeID)
	}
	rt.mu.Unlock()
	if ok {
		if r.timer != nil {
			r.timer.Stop()
		}
		if rt.onRemove != nil {
			rt.onRemove(nodeID)
		}
	}
}

// IsReady 报告节点是否仍在就绪池中（可接收新流量）。
func (rt *RouteTable) IsReady(nodeID string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	r, ok := rt.routes[nodeID]
	return ok && r.state == StateAlive
}

// State 返回条目状态；条目不存在时 ok=false（已物理销毁）。
func (rt *RouteTable) State(nodeID string) (s State, ok bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	r, ok := rt.routes[nodeID]
	if !ok {
		return StateDead, false
	}
	return r.state, true
}

// Ready 返回就绪池快照（按 ID 排序），即当前可路由新流量的节点。
func (rt *RouteTable) Ready() []string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]string, 0, len(rt.routes))
	for id, r := range rt.routes {
		if r.state == StateAlive {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
