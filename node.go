package main

// 同步协议（反熵，逐对进行）：
//
// 发起方周期性发送根摘要（MsgDigest，path 为空）。
// 接收方对每个摘要条目逐条比较：
//   - 摘要一致且非复核轮：整棵子树剪枝，不传任何记录（相同子树不传全量）；
//   - 摘要一致但本轮为复核轮（Verify）：继续下钻，叶层强制交换记录——
//     这是摘要碰撞时的复核路径，即使摘要撒谎也能发现分歧；
//   - 摘要不一致且为内部节点：下发全部子节点摘要，按路径递归细化；
//   - 摘要不一致且为叶桶：把本桶记录发给对方（MsgRecords, WantReply=true），
//     只传差异叶，而不是全量数据；本桶为空也要回（附桶路径），
//     否则单向同步在空桶处会停摆。
//
// 收到 MsgRecords 后按显式规则逐条 Merge；若存储发生变化或对方要求回执，
// 则把涉及桶的（已合并）记录回传。由于版本单调、合并幂等，该往返必然终止。
type MsgKind int

const (
	MsgDigest MsgKind = iota
	MsgRecords
)

type DigestEntry struct {
	Path   []int
	Digest uint64
}

type Msg struct {
	Kind      MsgKind
	From      string
	To        string
	Entries   []DigestEntry
	Records   []Record
	Buckets   [][]int // 本消息涉及的叶桶路径（可能无记录，仅标识桶）
	Verify    bool    // 复核轮：摘要一致也继续下钻/交换
	WantReply bool    // 请求对方回传对应桶的记录
}

type Node struct {
	id    string
	sim   *Sim
	store *Store
	seq   uint64
	peers []string
	round uint64
}

func NewNode(sim *Sim, id string, peers ...string) *Node {
	n := &Node{id: id, sim: sim, store: NewStore(), peers: peers}
	sim.AddNode(n)
	return n
}

// Put 本地写入，版本为本节点单调递增序号。
func (n *Node) Put(key, value string) Record {
	n.seq++
	r := Record{Key: key, Value: value, Version: n.seq, Origin: n.id}
	n.store.Merge(r)
	return r
}

// Delete 写入墓碑而非移除条目；墓碑随同步传播，旧值无法将其复活。
func (n *Node) Delete(key string) Record {
	n.seq++
	r := Record{Key: key, Deleted: true, Version: n.seq, Origin: n.id}
	n.store.Merge(r)
	return r
}

// SyncRound 向所有对等方发起一轮摘要交换。
// 每 VerifyEvery 轮为复核轮：不再信任摘要，全树下钻到叶并交换记录。
func (n *Node) SyncRound() {
	n.round++
	verify := n.sim.cfg.VerifyEvery > 0 && n.round%uint64(n.sim.cfg.VerifyEvery) == 0
	for _, peer := range n.peers {
		n.sim.Send(Msg{
			Kind:    MsgDigest,
			From:    n.id,
			To:      peer,
			Entries: []DigestEntry{{Path: nil, Digest: n.digestAt(nil)}},
			Verify:  verify,
		})
	}
}

func (n *Node) onMsg(m Msg) {
	switch m.Kind {
	case MsgDigest:
		n.onDigest(m)
	case MsgRecords:
		n.onRecords(m)
	}
}

func (n *Node) onDigest(m Msg) {
	var childEntries []DigestEntry
	var records []Record
	var buckets [][]int
	for _, e := range m.Entries {
		match := n.digestAt(e.Path) == e.Digest
		if match && !m.Verify {
			continue // 相同子树：剪枝，一个字节都不传
		}
		if len(e.Path) == TreeDepth {
			// 叶层：只传该差异桶的记录，并请求对方回传它的桶内容。
			// 即使本桶为空也要回应（带上桶路径），否则单向同步会在空桶处停摆。
			buckets = append(buckets, e.Path)
			records = append(records, n.bucketRecords(e.Path)...)
		} else {
			for c := 0; c < TreeFanout; c++ {
				cp := appendPath(e.Path, c)
				childEntries = append(childEntries, DigestEntry{Path: cp, Digest: n.digestAt(cp)})
			}
		}
	}
	if len(childEntries) > 0 {
		n.sim.Send(Msg{Kind: MsgDigest, From: n.id, To: m.From, Entries: childEntries, Verify: m.Verify})
	}
	if len(buckets) > 0 {
		n.sim.Send(Msg{Kind: MsgRecords, From: n.id, To: m.From, Records: records, Buckets: buckets, WantReply: true})
	}
}

func (n *Node) onRecords(m Msg) {
	changed := false
	for _, r := range m.Records {
		if n.store.Merge(r) {
			changed = true
		}
	}
	if !changed && !m.WantReply {
		return // 无新信息且无需回执：终止，保证往返有界
	}
	// 涉及桶 = 消息记录所在的桶 ∪ 消息显式标注的桶
	touched := map[string][]int{}
	for _, r := range m.Records {
		bp := keyPath(r.Key)
		touched[pathStr(bp)] = bp
	}
	for _, bp := range m.Buckets {
		touched[pathStr(bp)] = bp
	}
	// 回传涉及桶的合并后记录，让对方向同一不动点收敛
	var back []Record
	for _, bp := range touched {
		back = append(back, n.bucketRecords(bp)...)
	}
	if len(back) > 0 {
		n.sim.Send(Msg{Kind: MsgRecords, From: n.id, To: m.From, Records: back})
	}
}
