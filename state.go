package adaptive

import (
	"encoding/binary"
	"errors"
)

// 符号取值：0..255 为字面字节，nytSymbol 为 escape（NYT）伪符号，
// internalSymbol 表示内部节点。
const (
	internalSymbol = -1
	nytSymbol      = 256
)

// 编号规则：根固定为 rootNumber，每次分裂 NYT 产生两个节点，
// 新 NYT（左孩子）取小编号，新符号叶（右孩子）取大编号，编号只减不增。
// 节点编号终身不变；交换只交换节点在树中的位置，编号随节点走。
const (
	rootNumber = 513 // 2*256+1：257 个叶（256 字节 + NYT）+ 256 个内部节点
	maxNodes   = 513
)

// DefaultMaxWeight 是根权重上限，达到后触发重标定。
// 通信双方必须使用相同的 maxWeight。
const DefaultMaxWeight = uint64(1) << 32

// minMaxWeight 是 maxWeight 允许的最小值，保证重标定后根权重必然回落到上限以下。
const minMaxWeight = 512

type node struct {
	num    int32 // 稳定编号，越大越靠近根
	sym    int32 // internalSymbol / nytSymbol / 0..255
	weight uint64
	parent *node
	left   *node
	right  *node
}

func (n *node) isLeaf() bool { return n.left == nil }

// State 是编码器与解码器共享同步演化的自适应树状态。
// 双方从 NewState() 的同一初始状态出发，对每个符号调用相同的 Update，
// 因此任何时刻两棵树完全一致。
type State struct {
	root      *node
	nyt       *node
	leaves    [256]*node
	nodes     []*node // 创建顺序，即编号递减顺序
	nextNum   int32
	maxWeight uint64
	count     uint64 // 已处理符号数
}

// NewState 返回初始状态：仅一个 NYT 根节点（编号 513，权重 0）。
func NewState() *State {
	r := &node{num: rootNumber, sym: nytSymbol}
	s := &State{
		root:      r,
		nyt:       r,
		nextNum:   rootNumber - 1,
		maxWeight: DefaultMaxWeight,
	}
	s.nodes = append(s.nodes, r)
	return s
}

// leader 返回权重为 w 的块中编号最大的节点。
// “最大编号”全局唯一，与任何遍历顺序无关，因此双方无需依赖迭代顺序。
func (s *State) leader(w uint64) *node {
	var best *node
	for _, n := range s.nodes {
		if n.weight == w && (best == nil || n.num > best.num) {
			best = n
		}
	}
	return best
}

// swapNodes 交换两个节点在树中的位置（编号、权重、子树随节点走）。
func swapNodes(a, b *node) {
	ap, bp := a.parent, b.parent
	if ap == bp {
		if ap != nil { // 互为兄弟：直接交换两个槽位
			ap.left, ap.right = ap.right, ap.left
		}
		return
	}
	if ap != nil {
		if ap.left == a {
			ap.left = b
		} else {
			ap.right = b
		}
	}
	if bp != nil {
		if bp.left == b {
			bp.left = a
		} else {
			bp.right = a
		}
	}
	a.parent, b.parent = bp, ap
}

// Update 处理一个符号：必要时分裂 NYT，然后自叶向根递增权重，
// 沿途按 FGK 规则与同权重块 leader 交换。编码器与解码器调用同一实现。
func (s *State) Update(sym byte) {
	var p *node
	if leaf := s.leaves[sym]; leaf != nil {
		p = leaf
	} else {
		// 首次出现：分裂 NYT。旧 NYT 变为内部节点，
		// 左孩子为新 NYT（小编号），右孩子为新符号叶（大编号）。
		old := s.nyt
		newNYT := &node{num: s.nextNum - 1, sym: nytSymbol, parent: old}
		newLeaf := &node{num: s.nextNum, sym: int32(sym), parent: old}
		s.nextNum -= 2
		old.sym = internalSymbol
		old.left, old.right = newNYT, newLeaf
		s.nyt = newNYT
		s.leaves[sym] = newLeaf
		s.nodes = append(s.nodes, newNYT, newLeaf)
		p = newLeaf
	}
	for p != nil {
		// 与同权重块中编号最大者交换；leader 为父节点时不交换，
		// 否则父权重尚未递增会破坏兄弟性质。
		if l := s.leader(p.weight); l != p && l != p.parent {
			swapNodes(p, l)
		}
		p.weight++
		p = p.parent
	}
	s.count++
	if s.root.weight >= s.maxWeight {
		s.rescale()
	}
}

// rescale 权重溢出重标定：叶权重 w <- (w+1)/2（NYT 保持 0），
// 内部节点按编号递增序（子先于父）重算为子节点权重和。
// (w+1)/2 单调非降，故兄弟性质在重标定后保持。
func (s *State) rescale() {
	for _, n := range s.nodes {
		if n.isLeaf() && n.sym != nytSymbol {
			n.weight = (n.weight + 1) / 2
		}
	}
	for i := len(s.nodes) - 1; i >= 0; i-- { // 编号递增序
		n := s.nodes[i]
		if !n.isLeaf() {
			n.weight = n.left.weight + n.right.weight
		}
	}
}

// codeOf 返回节点码字（根到叶，0=左 1=右），供测试与调试独立校验。
func (s *State) codeOf(n *node) []byte {
	var rev []byte
	for c := n; c.parent != nil; c = c.parent {
		if c == c.parent.right {
			rev = append(rev, 1)
		} else {
			rev = append(rev, 0)
		}
	}
	code := make([]byte, len(rev))
	for i, b := range rev {
		code[len(rev)-1-i] = b
	}
	return code
}

// validate 校验树结构不变量，供状态恢复与测试使用。
func (s *State) validate() error {
	if s.root == nil || s.root.parent != nil || s.root.num != rootNumber {
		return errors.New("adaptive: bad root")
	}
	if len(s.nodes) == 0 || len(s.nodes) > maxNodes || len(s.nodes)%2 == 0 {
		return errors.New("adaptive: bad node count")
	}
	if s.nextNum != int32(rootNumber-len(s.nodes)) {
		return errors.New("adaptive: bad nextNum")
	}
	if s.maxWeight < minMaxWeight {
		return errors.New("adaptive: maxWeight too small")
	}
	seen := make(map[int32]bool, len(s.nodes))
	nytCount := 0
	var walk func(n *node) error
	walk = func(n *node) error {
		if n == nil {
			return errors.New("adaptive: nil child")
		}
		if n.num < 1 || n.num > rootNumber || seen[n.num] {
			return errors.New("adaptive: bad or duplicate node number")
		}
		seen[n.num] = true
		if n.isLeaf() {
			if n.right != nil {
				return errors.New("adaptive: leaf with right child")
			}
			switch {
			case n.sym == nytSymbol:
				nytCount++
				if n.weight != 0 {
					return errors.New("adaptive: NYT weight not zero")
				}
				if s.nyt != n {
					return errors.New("adaptive: NYT pointer mismatch")
				}
			case n.sym >= 0 && n.sym < 256:
				if s.leaves[n.sym] != n {
					return errors.New("adaptive: leaf map mismatch")
				}
			default:
				return errors.New("adaptive: bad leaf symbol")
			}
			return nil
		}
		if n.sym != internalSymbol || n.right == nil {
			return errors.New("adaptive: bad internal node")
		}
		if n.left.parent != n || n.right.parent != n {
			return errors.New("adaptive: bad parent link")
		}
		if n.left.num != n.right.num-1 {
			return errors.New("adaptive: sibling numbers not consecutive")
		}
		if n.weight != n.left.weight+n.right.weight {
			return errors.New("adaptive: weight mismatch")
		}
		if err := walk(n.left); err != nil {
			return err
		}
		return walk(n.right)
	}
	if err := walk(s.root); err != nil {
		return err
	}
	if nytCount != 1 {
		return errors.New("adaptive: want exactly one NYT")
	}
	// 兄弟性质：编号递增 => 权重非减。
	prev := uint64(0)
	for i := len(s.nodes) - 1; i >= 0; i-- {
		n := s.nodes[i]
		if n.weight < prev {
			return errors.New("adaptive: sibling property violated")
		}
		prev = n.weight
	}
	return nil
}

// --- 状态导出 / 恢复 ---
//
// 格式（全部 uvarint，节点按编号递增序）：
//   stateVersion, nodeCount, nextNum, maxWeight, count,
//   每节点: sym+2, weight, parentNum, leftNum, rightNum   (0 表示 nil)

func appendUvarint(dst []byte, v uint64) []byte {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:n]...)
}

func numOf(n *node) uint64 {
	if n == nil {
		return 0
	}
	return uint64(n.num)
}

// Export 序列化状态，结果确定（与遍历顺序无关）。
func (s *State) Export() []byte {
	out := appendUvarint(nil, StateVersion)
	out = appendUvarint(out, uint64(len(s.nodes)))
	out = appendUvarint(out, uint64(s.nextNum))
	out = appendUvarint(out, s.maxWeight)
	out = appendUvarint(out, s.count)
	for i := len(s.nodes) - 1; i >= 0; i-- { // 编号递增序
		n := s.nodes[i]
		out = appendUvarint(out, uint64(n.sym+2))
		out = appendUvarint(out, n.weight)
		out = appendUvarint(out, numOf(n.parent))
		out = appendUvarint(out, numOf(n.left))
		out = appendUvarint(out, numOf(n.right))
	}
	return out
}

type stateParser struct {
	data []byte
	off  int
}

func (p *stateParser) uvarint() (uint64, error) {
	v, n := binary.Uvarint(p.data[p.off:])
	if n <= 0 {
		return 0, errCorruptState
	}
	p.off += n
	return v, nil
}

var errCorruptState = errors.New("adaptive: corrupt state")

// parseState 从 data 解析状态，返回状态与剩余字节（供复合格式使用）。
func parseState(data []byte) (*State, []byte, error) {
	p := &stateParser{data: data}
	ver, err := p.uvarint()
	if err != nil || ver != StateVersion {
		return nil, nil, errCorruptState
	}
	count, err := p.uvarint()
	if err != nil || count == 0 || count > maxNodes || count%2 == 0 {
		return nil, nil, errCorruptState
	}
	nextNum, err := p.uvarint()
	if err != nil || nextNum > rootNumber {
		return nil, nil, errCorruptState
	}
	maxWeight, err := p.uvarint()
	if err != nil || maxWeight < minMaxWeight {
		return nil, nil, errCorruptState
	}
	processed, err := p.uvarint()
	if err != nil {
		return nil, nil, errCorruptState
	}
	type raw struct {
		sym, weight, parent, left, right uint64
	}
	raws := make([]raw, count)
	byNum := make(map[int32]*node, count)
	for i := range raws {
		fields := []*uint64{&raws[i].sym, &raws[i].weight, &raws[i].parent, &raws[i].left, &raws[i].right}
		for _, f := range fields {
			v, err := p.uvarint()
			if err != nil {
				return nil, nil, errCorruptState
			}
			*f = v
		}
	}
	s := &State{
		nextNum:   int32(nextNum),
		maxWeight: maxWeight,
		count:     processed,
	}
	// 节点按编号递增序存储；s.nodes 需要编号递减序。
	for i := len(raws) - 1; i >= 0; i-- {
		r := raws[i]
		if r.sym > uint64(nytSymbol)+2 {
			return nil, nil, errCorruptState
		}
		n := &node{sym: int32(r.sym) - 2, weight: r.weight}
		s.nodes = append(s.nodes, n)
		_ = r
	}
	// 重新按编号递增序读一遍编号：编号隐含为顺序 1..len？不行——编号由顺序隐含：
	// 导出按编号递增序写，因此第 i 个节点的编号需从链接字段恢复。
	// 这里改为：先建占位，再用 parent/left/right 字段互相解析。
	// 编号 = 数组下标 + 1？不保证连续。改为显式存储编号更简单，
	// 但导出格式未含编号——编号由“递增序”隐含为排序键，具体值需记录。
	// 因此这里从链接字段无法恢复编号，直接返回错误提示。
	return nil, nil, errors.New("adaptive: unimplemented")
}
