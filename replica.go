package deltacrdt

// Replica 是网络中的一个同步节点：持有文档副本，
// 并为每个远端对等节点维护"已确认水位"。
type Replica struct {
	ID   uint64
	Doc  *Document
	acks map[uint64]VersionVector // peerID -> 该远端已确认的水位
}

// NewReplica 创建一个节点。
func NewReplica(id uint64) *Replica {
	return &Replica{
		ID:   id,
		Doc:  NewDocument(id),
		acks: make(map[uint64]VersionVector),
	}
}

// DeltaFor 为指定对等节点生成增量 Delta 包：
// 仅包含自该节点上次确认水位以来的变动切片。
func (r *Replica) DeltaFor(peer uint64) *Delta {
	return r.Doc.GenerateDelta(r.acks[peer])
}

// FullStateFor 生成全量状态包（用于新节点首次同步）。
func (r *Replica) FullStateFor() *Delta {
	return r.Doc.FullState()
}

// Receive 接收并应用远端 Delta，返回本地水位作为确认回执（Ack）。
func (r *Replica) Receive(delta *Delta) VersionVector {
	return r.Doc.ApplyDelta(delta)
}

// Ack 记录远端对等节点返回的确认水位，后续只为它生成水位之后的增量。
func (r *Replica) Ack(peer uint64, vv VersionVector) {
	current := r.acks[peer]
	if current == nil {
		current = make(VersionVector)
	}
	current.Merge(vv)
	r.acks[peer] = current
}

// SyncTo 执行一轮单向同步：为 peer 生成 Delta、传输、应用并回传 Ack。
// 返回实际发送的字节数，可用于度量带宽。
func (r *Replica) SyncTo(peer *Replica) int {
	wire := r.DeltaFor(peer.ID).Encode()
	delta, err := DecodeDelta(wire)
	if err != nil {
		panic(err)
	}
	ack := peer.Receive(delta)
	r.Ack(peer.ID, ack)
	return len(wire)
}
