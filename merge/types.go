package merge

import "sort"

// MemberState 表示成员在集群视图中的状态。
type MemberState int

const (
	Alive MemberState = iota
	Suspect
)

func (s MemberState) String() string {
	if s == Suspect {
		return "suspect"
	}
	return "alive"
}

// Member 是集群中的一个节点条目。
type Member struct {
	ID          uint64
	State       MemberState
	Incarnation uint64
}

// View 是节点对所属集群（分区）的一致性强视图：
// 集群标识 + 纪元（每次拓扑/成员变更递增）+ 成员表。
type View struct {
	ClusterID uint64
	Epoch     uint64
	Members   map[uint64]Member
}

// Clone 深拷贝视图，避免共享底层 map。
func (v View) Clone() View {
	out := View{ClusterID: v.ClusterID, Epoch: v.Epoch, Members: make(map[uint64]Member, len(v.Members))}
	for id, m := range v.Members {
		out.Members[id] = m
	}
	return out
}

// memberSlice 将成员表转换为按 ID 排序的切片（用于消息传输，保证确定性）。
func memberSlice(m map[uint64]Member) []Member {
	out := make([]Member, 0, len(m))
	for _, mem := range m {
		out = append(out, mem)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// memberMap 将切片还原为成员表。
func memberMap(ms []Member) map[uint64]Member {
	out := make(map[uint64]Member, len(ms))
	for _, m := range ms {
		out[m.ID] = m
	}
	return out
}

// subsetOf 判断 a 的成员 ID 集合是否为 b 的子集。
func subsetOf(a, b map[uint64]Member) bool {
	for id := range a {
		if _, ok := b[id]; !ok {
			return false
		}
	}
	return true
}

// mergeKey 是合并协商的确定性优先级键。
// 键越大的一方成为合并发起方（initiator），双方独立计算结果一致，
// 这是避免双向同时发起合并导致活锁的关键。
type mergeKey struct {
	epoch     uint64
	size      int
	clusterID uint64
}

func keyOf(v View) mergeKey {
	return mergeKey{epoch: v.Epoch, size: len(v.Members), clusterID: v.ClusterID}
}

func (k mergeKey) greater(o mergeKey) bool {
	if k.epoch != o.epoch {
		return k.epoch > o.epoch
	}
	if k.size != o.size {
		return k.size > o.size
	}
	return k.clusterID > o.clusterID
}

// MsgType 是节点间消息类型。
type MsgType int

const (
	// MsgPingReq 是 SWIM 风格的心跳探活请求；分区重连后首个成功的
	// PingReq 即触发合并握手。
	MsgPingReq MsgType = iota
	// MsgAck 是 PingReq 的应答，同样携带集群标识，双向都可发现裂脑。
	MsgAck
	// MsgMergeHello 携带本侧完整视图摘要，是合并握手的第一阶段。
	MsgMergeHello
	// MsgMergeCommit 由协商出的发起方广播，携带融合后的最终视图，
	// 是合并握手的第二阶段（提交阶段）。
	MsgMergeCommit
)

func (t MsgType) String() string {
	switch t {
	case MsgPingReq:
		return "PingReq"
	case MsgAck:
		return "Ack"
	case MsgMergeHello:
		return "MergeHello"
	case MsgMergeCommit:
		return "MergeCommit"
	}
	return "unknown"
}

// Envelope 是节点间传递的消息信封。
type Envelope struct {
	Type      MsgType
	From      uint64
	To        uint64
	ClusterID uint64
	Epoch     uint64
	Clock     uint64
	Members   []Member
}
