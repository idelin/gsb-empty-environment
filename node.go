package session

import "fmt"

// Item 是键的副本状态：值 + 因果版本。
type Item struct {
	Value string
	Ver   VersionVector
}

// Disk 是持久化抽象（模拟磁盘），崩溃重启后数据从这里恢复。
type Disk interface {
	Load() map[string]Item
	Store(data map[string]Item)
}

// MemDisk 是内存实现的 Disk（在模拟器进程内代表持久介质）。
type MemDisk struct{ data map[string]Item }

// NewMemDisk 创建空磁盘。
func NewMemDisk() *MemDisk { return &MemDisk{data: map[string]Item{}} }

// Load 返回深拷贝。
func (d *MemDisk) Load() map[string]Item {
	out := make(map[string]Item, len(d.data))
	for k, it := range d.data {
		out[k] = Item{Value: it.Value, Ver: it.Ver.Copy()}
	}
	return out
}

// Store 深拷贝写入。
func (d *MemDisk) Store(data map[string]Item) {
	out := make(map[string]Item, len(data))
	for k, it := range data {
		out[k] = Item{Value: it.Value, Ver: it.Ver.Copy()}
	}
	d.data = out
}

// ReadReply 是读请求的响应；Err 非空表示一致性要求被显式拒绝。
type ReadReply struct {
	Value string
	Ver   VersionVector
	Err   error
}

type pendingRead struct {
	key   string
	token Token
	reply func(ReadReply)
}

// Node 是一个副本节点。
type Node struct {
	ID        string
	sim       *Simulator
	disk      Disk
	peers     []string
	replDelay int64

	data    map[string]Item
	up      bool
	pending map[int]*pendingRead
	nextID  int
}

// NewNode 创建节点并注册到模拟器。replDelay 为异步复制的消息延迟。
func NewNode(sim *Simulator, id string, disk Disk, replDelay int64) *Node {
	n := &Node{
		ID:        id,
		sim:       sim,
		disk:      disk,
		replDelay: replDelay,
		data:      disk.Load(),
		up:        true,
		pending:   map[int]*pendingRead{},
	}
	sim.nodes[id] = n
	return n
}

// SetPeers 设置复制对等节点（全连接异步复制，无全局同步）。
func (n *Node) SetPeers(peers ...string) { n.peers = peers }

func (n *Node) persist() { n.disk.Store(n.data) }

// satisfies 检查本地状态是否满足令牌的全部跨键因果要求。
func (n *Node) satisfies(tok Token) bool {
	for k, req := range tok.Versions {
		var have VersionVector
		if it, ok := n.data[k]; ok {
			have = it.Ver
		} else {
			have = VersionVector{}
		}
		if !have.Dominates(req) {
			return false
		}
	}
	return true
}

// ClientWrite 处理客户端写：本地定序、持久化、确认，然后异步复制。
func (n *Node) ClientWrite(sess *Session, key, value string, reply func(VersionVector, error)) {
	if !n.up {
		reply(nil, ErrNodeDown)
		return
	}
	if sess.Token.SessionID != sess.ID {
		reply(nil, ErrTokenMismatch)
		return
	}
	vv := n.data[key].Ver.Copy()
	vv[n.ID]++
	n.data[key] = Item{Value: value, Ver: vv}
	n.persist()
	n.sim.logf("%s: write %s=%q ver=%v ack->%s", n.ID, key, value, vv, sess.ID)
	reply(vv.Copy(), nil)
	for _, peer := range n.peers {
		n.sendReplicate(peer, key, n.data[key])
	}
}

// ClientRead 处理客户端读：满足令牌则立即返回；否则挂起等待复制，
// 超时则以 ErrConsistencyTimeout 显式失败，绝不返回旧值。
func (n *Node) ClientRead(sess *Session, key string, timeout int64, reply func(ReadReply)) {
	if !n.up {
		reply(ReadReply{Err: ErrNodeDown})
		return
	}
	if sess.Token.SessionID != sess.ID {
		n.sim.logf("%s: read %s by %s REJECTED: token belongs to %s", n.ID, key, sess.ID, sess.Token.SessionID)
		reply(ReadReply{Err: ErrTokenMismatch})
		return
	}
	if n.satisfies(sess.Token) {
		it := n.data[key]
		n.sim.logf("%s: read %s -> %q ver=%v (token satisfied)", n.ID, key, it.Value, it.Ver)
		reply(ReadReply{Value: it.Value, Ver: it.Ver.Copy()})
		return
	}
	id := n.nextID
	n.nextID++
	n.pending[id] = &pendingRead{key: key, token: sess.Token.Copy(), reply: reply}
	n.sim.logf("%s: read %s by %s PENDING (token not satisfied, timeout=%d)", n.ID, key, sess.ID, timeout)
	n.sim.After(timeout, func() {
		if _, ok := n.pending[id]; ok {
			delete(n.pending, id)
			n.sim.logf("%s: read %s by %s TIMEOUT: consistency not met", n.ID, key, sess.ID)
			reply(ReadReply{Err: ErrConsistencyTimeout})
		}
	})
}

func (n *Node) sendReplicate(to, key string, it Item) {
	n.sim.msgSq++
	mc := MsgContext{Seq: n.sim.msgSq, From: n.ID, To: to, Key: key}
	drop, delay := false, n.replDelay
	if n.sim.Fault != nil {
		drop, delay = n.sim.Fault(mc)
	}
	if drop {
		n.sim.logf("%s->%s: replicate %s DROPPED (fault injection)", n.ID, to, key)
		return
	}
	n.sim.logf("%s->%s: replicate %s ver=%v delay=%d", n.ID, to, key, it.Ver, delay)
	n.sim.After(delay, func() {
		if peer := n.sim.nodes[to]; peer != nil {
			peer.ApplyReplicate(n.ID, key, it)
		}
	})
}

// ApplyReplicate 应用来自同伴的复制消息并唤醒被满足的挂起读。
func (n *Node) ApplyReplicate(from, key string, it Item) {
	if !n.up {
		n.sim.logf("%s: replicate %s from %s ignored (node down)", n.ID, key, from)
		return
	}
	cur, ok := n.data[key]
	switch {
	case !ok || it.Ver.Dominates(cur.Ver):
		n.data[key] = Item{Value: it.Value, Ver: it.Ver.Copy()}
	case cur.Ver.Dominates(it.Ver):
		// 旧消息，保留本地较新版本。
	default:
		// 并发写：合并版本向量，确定性取来值（冲突解决不影响会话保证）。
		n.data[key] = Item{Value: it.Value, Ver: Merge(cur.Ver, it.Ver)}
	}
	n.persist()
	n.sim.logf("%s: applied replicate %s from %s, ver=%v", n.ID, key, from, n.data[key].Ver)
	n.checkPending()
}

// InstallSnapshot 安装快照：逐键合并，版本只升不降，禁止静默降级。
func (n *Node) InstallSnapshot(from string, snap map[string]Item) {
	if !n.up {
		return
	}
	for k, it := range snap {
		cur, ok := n.data[k]
		switch {
		case !ok || it.Ver.Dominates(cur.Ver):
			n.data[k] = Item{Value: it.Value, Ver: it.Ver.Copy()}
		case cur.Ver.Dominates(it.Ver):
			n.sim.logf("%s: snapshot %s from %s is stale, keep local ver=%v", n.ID, k, from, cur.Ver)
		default:
			n.data[k] = Item{Value: it.Value, Ver: Merge(cur.Ver, it.Ver)}
		}
	}
	n.persist()
	n.sim.logf("%s: installed snapshot from %s", n.ID, from)
	n.checkPending()
}

// Snapshot 导出当前状态的深拷贝。
func (n *Node) Snapshot() map[string]Item {
	out := make(map[string]Item, len(n.data))
	for k, it := range n.data {
		out[k] = Item{Value: it.Value, Ver: it.Ver.Copy()}
	}
	return out
}

// ReplicateAll 反熵：把本地全部数据重新异步复制给所有同伴。
func (n *Node) ReplicateAll() {
	for _, peer := range n.peers {
		for k, it := range n.data {
			n.sendReplicate(peer, k, it)
		}
	}
}

// Crash 模拟节点崩溃：内存丢失，挂起读被显式失败（非静默）。
func (n *Node) Crash() {
	n.up = false
	n.data = nil
	for id, p := range n.pending {
		delete(n.pending, id)
		p.reply(ReadReply{Err: ErrNodeDown})
	}
	n.sim.logf("%s: CRASH", n.ID)
}

// Restart 从持久盘恢复；令牌要求的版本仍然有效，不会降级。
func (n *Node) Restart() {
	n.data = n.disk.Load()
	n.up = true
	n.sim.logf("%s: restart, restored %d keys from disk", n.ID, len(n.data))
}

func (n *Node) checkPending() {
	for id, p := range n.pending {
		if n.satisfies(p.token) {
			delete(n.pending, id)
			it := n.data[p.key]
			n.sim.logf("%s: pending read %s now satisfied -> %q ver=%v", n.ID, p.key, it.Value, it.Ver)
			p.reply(ReadReply{Value: it.Value, Ver: it.Ver.Copy()})
		}
	}
}

// String 便于调试输出。
func (n *Node) String() string {
	return fmt.Sprintf("Node(%s, up=%v, keys=%d)", n.ID, n.up, len(n.data))
}
