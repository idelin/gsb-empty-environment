package causalrouter

import (
	"fmt"
	"strings"
	"sync"
)

// UnreachableEvidence 不可达证据：记录被隔离消息及其原因。
type UnreachableEvidence struct {
	MessageID string   // 被隔离的消息ID
	Reason    string   // 隔离原因
	Cycle     []string // 若因循环因果被隔离，记录环上的消息ID
}

func (e UnreachableEvidence) String() string {
	if len(e.Cycle) > 0 {
		return fmt.Sprintf("message %s quarantined: %s; cycle: %s",
			e.MessageID, e.Reason, strings.Join(e.Cycle, " -> "))
	}
	return fmt.Sprintf("message %s quarantined: %s", e.MessageID, e.Reason)
}

// msgStatus 消息相对于路由器交付前沿的状态。
type msgStatus int

const (
	statusDuplicate msgStatus = iota // 已交付过的重复消息
	statusReady                      // 所有前置依赖已满足，可立即交付
	statusWaiting                    // 前置事件尚未到达，需要暂存
)

// Router 因果一致性消息路由器。
// 维护每个客户端已连续交付的最大序号（交付前沿），
// 对前置依赖未满足的消息入队等待，对循环因果的消息生成不可达证据并隔离。
type Router struct {
	mu         sync.Mutex
	frontier   VectorClock // 每个客户端已连续交付的最大序号
	seen       map[string]bool
	pending    []*Message
	quarantine []*Message
	evidence   []UnreachableEvidence
	log        []*Message

	// OnDeliver 交付回调（在锁外调用），可为 nil。
	OnDeliver func(*Message)
}

func NewRouter() *Router {
	return &Router{
		frontier: VectorClock{},
		seen:     make(map[string]bool),
	}
}

// Submit 提交一条消息。返回本次提交触发交付的消息（按因果顺序）。
func (r *Router) Submit(m *Message) []*Message {
	r.mu.Lock()
	before := len(r.log)
	switch r.statusLocked(m) {
	case statusDuplicate:
		// 幂等丢弃
	case statusReady:
		r.deliverLocked(m)
		r.drainLocked()
	default:
		r.pending = append(r.pending, m)
		r.detectUnreachableLocked()
	}
	delivered := append([]*Message(nil), r.log[before:]...)
	hook := r.OnDeliver
	r.mu.Unlock()

	if hook != nil {
		for _, d := range delivered {
			hook(d)
		}
	}
	return delivered
}

// statusLocked 判定消息是否可交付。
// 可交付条件：发送方序号恰好是下一个期望序号，且其余分量均不超过交付前沿。
func (r *Router) statusLocked(m *Message) msgStatus {
	if r.seen[m.ID] {
		return statusDuplicate
	}
	if m.Clock[m.Sender] <= r.frontier[m.Sender] {
		return statusDuplicate
	}
	if m.Clock[m.Sender] > r.frontier[m.Sender]+1 {
		return statusWaiting
	}
	for k, v := range m.Clock {
		if k == m.Sender {
			continue
		}
		if v > r.frontier[k] {
			return statusWaiting
		}
	}
	return statusReady
}

// deliverLocked 交付消息并推进交付前沿。
func (r *Router) deliverLocked(m *Message) {
	r.frontier[m.Sender] = m.Clock[m.Sender]
	r.seen[m.ID] = true
	r.log = append(r.log, m)
}

// drainLocked 反复扫描暂存队列，交付所有变为可交付的消息。
func (r *Router) drainLocked() {
	for {
		progress := false
		remaining := r.pending[:0]
		for _, p := range r.pending {
			if r.statusLocked(p) == statusReady {
				r.deliverLocked(p)
				progress = true
			} else {
				remaining = append(remaining, p)
			}
		}
		r.pending = remaining
		if !progress {
			return
		}
	}
}

// dependsOn 报告 m 是否因果依赖于 p 所包含的事件（p 是 m 的前置）。
// 即 p 发送方的分量在 p 中大于交付前沿、且被 m 的时钟覆盖。
func (r *Router) dependsOn(m, p *Message) bool {
	s := p.Sender
	return r.frontier[s] < p.Clock[s] && p.Clock[s] <= m.Clock[s]
}

// detectUnreachableLocked 在暂存队列上检测循环因果：
// 以“互相等待对方提供前置事件”构成环的消息永远无法交付，
// 为其生成不可达证据并隔离；随后传染式隔离所有依赖被隔离消息的暂存消息。
func (r *Router) detectUnreachableLocked() {
	for {
		cycle := r.findCycleLocked()
		if len(cycle) == 0 {
			break
		}
		ids := make([]string, len(cycle))
		for i, m := range cycle {
			ids[i] = m.ID
		}
		for _, m := range cycle {
			r.evidence = append(r.evidence, UnreachableEvidence{
				MessageID: m.ID,
				Reason:    "cyclic causal dependency: message waits on events that transitively wait on itself",
				Cycle:     append(append([]string(nil), ids...), ids[0]),
			})
			r.quarantineLocked(m)
		}
	}
	// 传染：依赖于被隔离消息的暂存消息同样不可达。
	for {
		progress := false
		remaining := r.pending[:0]
		for _, p := range r.pending {
			var blocker *Message
			for _, q := range r.quarantine {
				if r.dependsOn(p, q) {
					blocker = q
					break
				}
			}
			if blocker != nil {
				r.evidence = append(r.evidence, UnreachableEvidence{
					MessageID: p.ID,
					Reason:    fmt.Sprintf("depends on unreachable message %s", blocker.ID),
				})
				r.quarantineLocked(p)
				progress = true
			} else {
				remaining = append(remaining, p)
			}
		}
		r.pending = remaining
		if !progress {
			return
		}
	}
}

// findCycleLocked 在暂存消息依赖图中用三色DFS寻找一个环，返回环上的消息。
func (r *Router) findCycleLocked() []*Message {
	n := len(r.pending)
	adj := make([][]int, n)
	for i, m := range r.pending {
		for j, p := range r.pending {
			if i != j && r.dependsOn(m, p) {
				adj[i] = append(adj[i], j)
			}
		}
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, n)
	var stack []int
	var cycle []*Message
	var dfs func(u int) bool
	dfs = func(u int) bool {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range adj[u] {
			switch color[v] {
			case gray:
				idx := 0
				for i, node := range stack {
					if node == v {
						idx = i
						break
					}
				}
				for _, node := range stack[idx:] {
					cycle = append(cycle, r.pending[node])
				}
				return true
			case white:
				if dfs(v) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return false
	}
	for i := 0; i < n; i++ {
		if color[i] == white && dfs(i) {
			return cycle
		}
	}
	return nil
}

// quarantineLocked 将消息从暂存队列移入隔离区。
func (r *Router) quarantineLocked(m *Message) {
	remaining := r.pending[:0]
	for _, p := range r.pending {
		if p.ID != m.ID {
			remaining = append(remaining, p)
		}
	}
	r.pending = remaining
	r.quarantine = append(r.quarantine, m)
}

// Delivered 返回已交付消息日志（按交付顺序）。
func (r *Router) Delivered() []*Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Message(nil), r.log...)
}

// Pending 返回仍在暂存等待的消息。
func (r *Router) Pending() []*Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Message(nil), r.pending...)
}

// Quarantined 返回被隔离的消息。
func (r *Router) Quarantined() []*Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*Message(nil), r.quarantine...)
}

// Evidence 返回已生成的不可达证据。
func (r *Router) Evidence() []UnreachableEvidence {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]UnreachableEvidence(nil), r.evidence...)
}
