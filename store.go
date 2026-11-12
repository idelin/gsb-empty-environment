package taskpkg

type seqKey struct {
	node  NodeID
	epoch uint64
	seq   uint64
}

// Store 保存已验签事件并检测双签。事件可乱序到达；
// 依赖缺失不拒绝入库，而是在判定时报告未完成。
type Store struct {
	reg    *KeyRegistry
	events map[Hash]*Event
	bySeq  map[seqKey]Hash
}

func NewStore(reg *KeyRegistry) *Store {
	return &Store{
		reg:    reg,
		events: make(map[Hash]*Event),
		bySeq:  make(map[seqKey]Hash),
	}
}

// Add 校验并入库一个事件。发现同序号异内容时返回
// *DoubleSignEvidence（两份事件签名均已验证有效）。
func (s *Store) Add(e *Event) (Hash, error) {
	pub, ok := s.reg.lookup(e.Node, e.Epoch)
	if !ok {
		return ZeroHash, ErrUnknownKey
	}
	if !e.VerifySignature(pub) {
		return ZeroHash, ErrBadSignature
	}
	if e.Seq == 1 && e.Prev != ZeroHash {
		return ZeroHash, ErrBadSeq
	}
	h := e.Hash()
	if _, dup := s.events[h]; dup {
		return h, nil
	}
	// 严格序号：Prev 若已入库，必须是同节点同代次的 Seq-1。
	if e.Seq > 1 {
		if prev, ok := s.events[e.Prev]; ok {
			if prev.Node != e.Node || prev.Epoch != e.Epoch || prev.Seq != e.Seq-1 {
				return ZeroHash, ErrBadPrev
			}
		}
	}
	k := seqKey{e.Node, e.Epoch, e.Seq}
	if existing, ok := s.bySeq[k]; ok && existing != h {
		return h, &DoubleSignEvidence{
			Node:  e.Node,
			Epoch: e.Epoch,
			Seq:   e.Seq,
			A:     s.events[existing],
			B:     e,
		}
	}
	s.bySeq[k] = h
	s.events[h] = e
	return h, nil
}

// Get 返回已入库事件。
func (s *Store) Get(h Hash) (*Event, bool) {
	e, ok := s.events[h]
	return e, ok
}

// Missing 列出事件尚未入库的因果依赖。
func (s *Store) Missing(h Hash) ([]Hash, error) {
	e, ok := s.events[h]
	if !ok {
		return nil, ErrUnknownEvent
	}
	var out []Hash
	for _, d := range e.deps() {
		if _, ok := s.events[d]; !ok {
			out = append(out, d)
		}
	}
	return out, nil
}
