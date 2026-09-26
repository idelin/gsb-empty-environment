package sessionkv

import (
	"errors"
	"fmt"
)

// ErrNotSatisfied 表示在超时前无法满足一致性要求（显式标记，非静默降级）。
var ErrNotSatisfied = errors.New("sessionkv: consistency requirement not satisfied before timeout")

// VersionedValue 是带因果版本的值。
type VersionedValue struct {
	Value   string
	Version Vector
}

// WriteMsg 是复制消息：携带完整版本与写时的因果依赖。
// 接收方只有在自身知识覆盖 Deps 后才投递，保证因果顺序。
type WriteMsg struct {
	Key     string
	Value   string
	Version Vector
	Deps    Vector
}

// Snapshot 是节点状态的一致性快照。
type Snapshot struct {
	KV        map[string]VersionedValue
	Knowledge Vector
}

// ReadResult 是异步读的最终结果。Err 非 nil 表示显式未满足（如超时）；
// 只要 Err 为 nil，返回的快照必然覆盖请求令牌。
type ReadResult struct {
	Done    bool
	Found   bool
	Value   string
	Version Vector
	Err     error
}

// WriteResult 是异步写的最终结果；Version 为写确认向量（用于并入会话令牌）。
type WriteResult struct {
	Done    bool
	Version Vector
	Err     error
}

type readWaiter struct {
	key      string
	required Vector
	result   *ReadResult
	onDone   func(*ReadResult)
}

type pendingWrite struct {
	key    string
	value  string
	deps   Vector
	result *WriteResult
	onDone func(*WriteResult)
}

// Node 是异步复制系统中的一个副本节点。
// 读只在节点因果前沿覆盖请求令牌时才被服务；否则挂起等待，
// 直到复制/快照使前沿前进，或超时显式失败。不强迫全局同步。
type Node struct {
	ID        string
	sim       *Simulator
	peers     []*Node
	kv        map[string]VersionedValue
	knowledge Vector
	seq       uint64
	up        bool
	reads     map[*readWaiter]struct{}
	writes    map[*pendingWrite]struct{}
	buffered  []WriteMsg
}

// NewNode 创建一个空节点。
func NewNode(sim *Simulator, id string) *Node {
	return &Node{
		ID:        id,
		sim:       sim,
		kv:        map[string]VersionedValue{},
		knowledge: Vector{},
		up:        true,
		reads:     map[*readWaiter]struct{}{},
		writes:    map[*pendingWrite]struct{}{},
	}
}

// AddPeer 注册复制对等方（消息沿 sim 配置的链路异步发送）。
func (n *Node) AddPeer(p *Node) { n.peers = append(n.peers, p) }

// Up 报告节点是否在运行。
func (n *Node) Up() bool { return n.up }

// Knowledge 返回节点当前因果前沿的拷贝（供测试与演示检查）。
func (n *Node) Knowledge() Vector { return n.knowledge.Copy() }

// WriteAsync 发起一次写：仅当节点因果前沿覆盖 deps 时才应用，
// 否则等待复制补齐依赖；超时则显式失败。写确认携带完整因果版本。
func (n *Node) WriteAsync(key, value string, deps Vector, timeout int64, onDone func(*WriteResult)) *WriteResult {
	res := &WriteResult{}
	pw := &pendingWrite{key: key, value: value, deps: deps.Copy(), result: res, onDone: onDone}
	n.writes[pw] = struct{}{}
	n.progress()
	n.sim.After(timeout, func() {
		if res.Done {
			return
		}
		delete(n.writes, pw)
		res.Done = true
		res.Err = fmt.Errorf("%w: write key=%q deps=%s", ErrNotSatisfied, key, pw.deps)
		if pw.onDone != nil {
			pw.onDone(res)
		}
	})
	return res
}

// ReadAsync 发起一次读：仅当节点因果前沿覆盖 token.Vector 时才返回，
// 因此绝不会返回"满足本键版本却缺少其依赖"的快照；
// 超时则显式标记 ErrNotSatisfied，绝不静默降级为旧值。
func (n *Node) ReadAsync(key string, token Token, timeout int64, onDone func(*ReadResult)) *ReadResult {
	res := &ReadResult{}
	w := &readWaiter{key: key, required: token.Vector.Copy(), result: res, onDone: onDone}
	n.reads[w] = struct{}{}
	n.progress()
	n.sim.After(timeout, func() {
		if res.Done {
			return
		}
		delete(n.reads, w)
		res.Done = true
		res.Err = fmt.Errorf("%w: read key=%q required=%s", ErrNotSatisfied, key, w.required)
		if w.onDone != nil {
			w.onDone(res)
		}
	})
	return res
}

// ApplyRemote 接收复制消息；依赖未满足时进入因果缓冲，满足后按序投递。
func (n *Node) ApplyRemote(m WriteMsg) {
	if !n.up {
		return
	}
	n.buffered = append(n.buffered, m)
	n.progress()
}

// Crash 模拟节点崩溃：停止服务；已确认状态视为持久化保留。
// 等待中的读不会被静默失败，只能由重启后的状态补齐或超时来收尾。
func (n *Node) Crash() { n.up = false }

// Restart 重启节点：令牌继续有效——节点向对等方发起反熵拉取，
// 状态补齐后服务等待中的读；若直到超时仍无法满足，则由超时显式失败。
func (n *Node) Restart() {
	n.up = true
	for _, p := range n.peers {
		peer := p
		n.sim.Send(n.ID, peer.ID, func() { peer.SendStateTo(n) })
	}
	n.progress()
}

// SendStateTo 把本节点快照发送给 target（反熵/快照源）。
func (n *Node) SendStateTo(target *Node) {
	if !n.up {
		return
	}
	snap := n.Snapshot()
	n.sim.Send(n.ID, target.ID, func() { target.InstallSnapshot(snap) })
}

// Snapshot 导出当前状态的一致性快照。
func (n *Node) Snapshot() Snapshot {
	kv := make(map[string]VersionedValue, len(n.kv))
	for k, v := range n.kv {
		kv[k] = VersionedValue{Value: v.Value, Version: v.Version.Copy()}
	}
	return Snapshot{KV: kv, Knowledge: n.knowledge.Copy()}
}

// InstallSnapshot 合入快照（只前进不回退）；可能唤醒等待中的读。
func (n *Node) InstallSnapshot(s Snapshot) {
	if !n.up {
		return
	}
	for k, v := range s.KV {
		n.deliver(WriteMsg{Key: k, Value: v.Value, Version: v.Version.Copy(), Deps: Vector{}})
	}
	n.knowledge.Merge(s.Knowledge)
	n.progress()
}

// ResetSnapshot 模拟回退式快照安装（如从旧备份恢复）：状态被整体替换。
// 会话令牌不会因此静默降级：要求超出新前沿的读只能等待状态重新覆盖令牌，
// 否则超时并显式标记 ErrNotSatisfied。
func (n *Node) ResetSnapshot(s Snapshot) {
	n.kv = make(map[string]VersionedValue, len(s.KV))
	for k, v := range s.KV {
		n.kv[k] = VersionedValue{Value: v.Value, Version: v.Version.Copy()}
	}
	n.knowledge = s.Knowledge.Copy()
	n.progress()
}

// progress 推动状态机前进：投递因果缓冲、应用依赖已满足的写、唤醒可服务的读。
func (n *Node) progress() {
	for {
		n.retryBuffered()
		before := len(n.writes)
		n.retryWrites()
		if len(n.writes) == before {
			break
		}
	}
	n.notify()
}

func (n *Node) retryBuffered() {
	for {
		progressed := false
		remaining := n.buffered[:0]
		for _, m := range n.buffered {
			if n.knowledge.Covers(m.Deps) {
				n.deliver(m)
				progressed = true
			} else {
				remaining = append(remaining, m)
			}
		}
		n.buffered = remaining
		if !progressed {
			return
		}
	}
}

func (n *Node) retryWrites() {
	for pw := range n.writes {
		if pw.result.Done || !n.up || !n.knowledge.Covers(pw.deps) {
			continue
		}
		n.seq++
		version := pw.deps.Copy()
		if cur, ok := n.kv[pw.key]; ok {
			version.Merge(cur.Version)
		}
		version[n.ID] = n.seq
		n.deliver(WriteMsg{Key: pw.key, Value: pw.value, Version: version, Deps: pw.deps.Copy()})
		delete(n.writes, pw)
		pw.result.Done = true
		pw.result.Version = version.Copy()
		if pw.onDone != nil {
			pw.onDone(pw.result)
		}
	}
}

func (n *Node) notify() {
	for w := range n.reads {
		if w.result.Done || !n.up || !n.knowledge.Covers(w.required) {
			continue
		}
		vv, ok := n.kv[w.key]
		w.result.Done = true
		w.result.Found = ok
		if ok {
			w.result.Value = vv.Value
			w.result.Version = vv.Version.Copy()
		} else {
			w.result.Version = Vector{}
		}
		delete(n.reads, w)
		if w.onDone != nil {
			w.onDone(w.result)
		}
	}
}

// deliver 应用一条写消息（幂等），并泛洪转发给对等方。
// 版本单调合并保证转发必然终止。
func (n *Node) deliver(m WriteMsg) {
	if cur, ok := n.kv[m.Key]; ok && cur.Version.Covers(m.Version) {
		return
	}
	n.kv[m.Key] = VersionedValue{Value: m.Value, Version: m.Version.Copy()}
	n.knowledge.Merge(m.Version)
	for _, p := range n.peers {
		peer := p
		n.sim.Send(n.ID, peer.ID, func() { peer.ApplyRemote(m) })
	}
}
