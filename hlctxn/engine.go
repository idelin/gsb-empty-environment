package hlctxn

import (
	"hash/fnv"
	"sync"
	"time"
)

// TxnStatus 是事务记录状态机的三个状态。迁移只允许
// PENDING -> COMMITTED 或 PENDING -> ABORTED，均为原子单向迁移。
type TxnStatus int

const (
	Pending TxnStatus = iota
	Committed
	Aborted
)

func (s TxnStatus) String() string {
	switch s {
	case Pending:
		return "PENDING"
	case Committed:
		return "COMMITTED"
	case Aborted:
		return "ABORTED"
	}
	return "UNKNOWN"
}

// TxnRecord 是事务记录（TxnRecord），承载权威状态机与被推高的时间戳。
type TxnRecord struct {
	ID          uint64
	Coordinator int
	Priority    int32
	Status      TxnStatus

	// WriteTS 跟踪所有已写意图的最大（被推高）时间戳；
	// 提交时 commitTS 必须 >= WriteTS。
	WriteTS Timestamp

	// CommitTS 仅在 COMMITTED 后有效，是全部意图统一的原子可见时间戳。
	CommitTS Timestamp

	// 真实物理时间（无偏斜基准钟），用于外部一致性审计。
	trueStartNS  int64
	trueCommitNS int64

	// IntentKeys 记录本事务当前持有的全部意图键。
	IntentKeys map[string]struct{}
}

// mvccVersion 是键上的一个已提交 MVCC 版本。
type mvccVersion struct {
	ts    Timestamp
	value string
	txnID uint64
}

// Intent 是写意图，指向其事务记录。
type Intent struct {
	TxnID uint64
	Node  int
	TS    Timestamp
	Value string
}

// keyState 保存单个键上的已提交版本链与至多一个未决意图。
type keyState struct {
	versions []mvccVersion // 按 ts 降序维护
	intent   *Intent
}

// Node 是持有分片数据与本地 HLC 的节点。
type Node struct {
	ID    int
	HLC   *Clock
	store map[string]*keyState
}

// Stats 汇总引擎运行期计数，供验收测试断言。
type Stats struct {
	ReadRestarts    int
	TimestampPushes int
	TxnAborts       int
	TxnCommits      int
	IntentResolved  int
	LocalForwards   int
}

// Cluster 是仿真集群：单一互斥锁保证事务记录状态机迁移与意图解析的
// 原子性（真实系统由事务记录范围锁 + Raft 保证，仿真以此等价建模）。
type Cluster struct {
	mu        sync.Mutex
	nodes     []*Node
	records   map[uint64]*TxnRecord
	trueClock PhysicalClock
	maxOffset time.Duration
	nextTxnID uint64

	// LazyResolution=true 时，提交/中止保留意图，由后续读/写者在
	// 撞到意图时原地解析（用于完整覆盖意图解析路径）。
	LazyResolution bool

	stats Stats
}

// NewCluster 用显式物理钟构造集群。每个节点 HLC 各自挂一个物理钟，
// trueClock 是无偏斜基准钟（真实时间），仅用于审计与提交时间记录。
func NewCluster(physClocks []PhysicalClock, trueClock PhysicalClock, maxOffset time.Duration) *Cluster {
	if len(physClocks) == 0 {
		panic("hlctxn: cluster needs at least one node")
	}
	c := &Cluster{
		records:   make(map[uint64]*TxnRecord),
		trueClock: trueClock,
		maxOffset: maxOffset,
		nextTxnID: 1,
	}
	for i, pc := range physClocks {
		c.nodes = append(c.nodes, &Node{
			ID:    i,
			HLC:   NewClock(pc),
			store: make(map[string]*keyState),
		})
	}
	return c
}

// NewRealCluster 构造真实墙钟集群，按 offsets 为各节点注入时钟偏斜；
// 每个偏移的绝对值必须 <= maxOffset。
func NewRealCluster(n int, offsets []time.Duration, maxOffset time.Duration) *Cluster {
	base := RealPhysicalClock{}
	phys := make([]PhysicalClock, n)
	for i := 0; i < n; i++ {
		var off time.Duration
		if i < len(offsets) {
			off = offsets[i]
		}
		if off > maxOffset || off < -maxOffset {
			panic("hlctxn: node clock offset exceeds maxOffset")
		}
		phys[i] = SkewedPhysicalClock{Base: base, Offset: off}
	}
	return NewCluster(phys, RealPhysicalClock{}, maxOffset)
}

// NodeCount 返回节点数。
func (c *Cluster) NodeCount() int { return len(c.nodes) }

// MaxOffset 返回全集群最大时钟偏斜界。
func (c *Cluster) MaxOffset() time.Duration { return c.maxOffset }

// StatsSnapshot 在锁保护下取运行期计数快照。
func (c *Cluster) StatsSnapshot() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// nodeForKey 用 FNV-1a 将键哈希分片到固定节点。
func (c *Cluster) nodeForKey(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(len(c.nodes)))
}

func (c *Cluster) keyStateOn(node int, key string) *keyState {
	n := c.nodes[node]
	ks, ok := n.store[key]
	if !ok {
		ks = &keyState{}
		n.store[key] = ks
	}
	return ks
}

// insertVersion 按时间戳降序插入已提交版本（同戳覆盖，正常不应发生）。
func (ks *keyState) insertVersion(v mvccVersion) {
	for i, existing := range ks.versions {
		if v.ts.Equal(existing.ts) {
			ks.versions[i] = v
			return
		}
		if v.ts.Greater(existing.ts) {
			ks.versions = append(ks.versions, mvccVersion{})
			copy(ks.versions[i+1:], ks.versions[i:])
			ks.versions[i] = v
			return
		}
	}
	ks.versions = append(ks.versions, v)
}

func (t Timestamp) Equal(o Timestamp) bool { return t.Compare(o) == 0 }

// PendingIntents 返回当前集群仍未解析的意图（诊断/断言用）。
func (c *Cluster) PendingIntents() []Intent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Intent
	for _, n := range c.nodes {
		for _, ks := range n.store {
			if ks.intent != nil {
				out = append(out, *ks.intent)
			}
		}
	}
	return out
}

// TxnRecordView 是事务记录的只读视图。
type TxnRecordView struct {
	ID           uint64
	Coordinator  int
	Priority     int32
	Status       TxnStatus
	WriteTS      Timestamp
	CommitTS     Timestamp
	TrueStartNS  int64
	TrueCommitNS int64
}

// LookupTxn 按 ID 查看事务记录；不存在返回 false。
func (c *Cluster) LookupTxn(id uint64) (TxnRecordView, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.records[id]
	if !ok {
		return TxnRecordView{}, false
	}
	return TxnRecordView{
		ID: r.ID, Coordinator: r.Coordinator, Priority: r.Priority,
		Status: r.Status, WriteTS: r.WriteTS, CommitTS: r.CommitTS,
		TrueStartNS: r.trueStartNS, TrueCommitNS: r.trueCommitNS,
	}, true
}
