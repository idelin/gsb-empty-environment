// Package batch 维护批次库存：入库、拆分、合并、出库，数量均为整数。
// 所有操作构成一张有向来源图（批次 -> 消费它的操作 -> 它产出的批次），
// 撤销祖先操作时若其后代已消费产出，则拒绝并返回依赖链；
// 不能仅凭总量足够就撤销。每次变更后可调用 CheckConservation 做逐批守恒核对。
package batch

import (
	"fmt"
	"strings"
)

// OpType 操作类型。
type OpType string

const (
	OpInbound  OpType = "inbound"  // 入库：无中生有，产出一个批次
	OpSplit    OpType = "split"    // 拆分：一个母批拆出若干子批，母批保留余量
	OpMerge    OpType = "merge"    // 合并：多个母批各出一部分合成一个子批
	OpOutbound OpType = "outbound" // 出库：从批次消耗数量，无产出
)

// Portion 表示某批次在一次操作中出/入的数量。
type Portion struct {
	BatchID string
	Qty     int
}

// Operation 来源图中的一条操作记录。
type Operation struct {
	ID      string
	Type    OpType
	Inputs  []Portion // 消耗的批次及数量
	Outputs []Portion // 产出的批次及数量（出库为空）
	Undone  bool
}

// Batch 一个批次节点。Remaining 始终等于 Initial 减去所有有效操作的消耗。
type Batch struct {
	ID        string
	Initial   int
	Remaining int
}

// UndoError 撤销被拒绝：后代操作已消费该操作的产出。
// Chain 是从被撤销操作到某个消费后代的交替路径（操作、批次、操作……）。
type UndoError struct {
	OpID  string
	Chain []string
}

func (e *UndoError) Error() string {
	return fmt.Sprintf("无法撤销 %s：产出已被后代操作消费，依赖链: %s",
		e.OpID, strings.Join(e.Chain, " -> "))
}

// Store 批次库，持有全部批次与操作日志。
type Store struct {
	batches map[string]*Batch
	ops     map[string]*Operation
	order   []string // 操作 ID，按发生顺序
	batchN  int
	opN     int
}

// NewStore 创建空批次库。
func NewStore() *Store {
	return &Store{
		batches: make(map[string]*Batch),
		ops:     make(map[string]*Operation),
	}
}

// Batch 返回批次快照；不存在时 ok=false。
func (s *Store) Batch(id string) (b Batch, ok bool) {
	p, found := s.batches[id]
	if !found {
		return Batch{}, false
	}
	return *p, true
}

// Operation 返回操作快照；不存在时 ok=false。
func (s *Store) Operation(id string) (op Operation, ok bool) {
	p, found := s.ops[id]
	if !found {
		return Operation{}, false
	}
	return *p, true
}

// Operations 按发生顺序返回全部操作快照（含已撤销的）。
func (s *Store) Operations() []Operation {
	out := make([]Operation, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, *s.ops[id])
	}
	return out
}

func (s *Store) nextBatchID() string {
	s.batchN++
	return fmt.Sprintf("B%d", s.batchN)
}

func (s *Store) nextOpID() string {
	s.opN++
	return fmt.Sprintf("OP%d", s.opN)
}

// Inbound 入库：新建一个数量为 qty 的批次。返回批次 ID 与操作 ID。
func (s *Store) Inbound(qty int) (batchID, opID string, err error) {
	if qty <= 0 {
		return "", "", fmt.Errorf("入库数量必须为正整数，得到 %d", qty)
	}
	batchID = s.nextBatchID()
	op := &Operation{
		ID:      s.nextOpID(),
		Type:    OpInbound,
		Outputs: []Portion{{BatchID: batchID, Qty: qty}},
	}
	s.batches[batchID] = &Batch{ID: batchID, Initial: qty, Remaining: qty}
	s.commit(op)
	return batchID, op.ID, nil
}

// Split 部分拆分：从 parent 拆出 parts 中指定的各份数量，母批保留余量。
// 每份产出一个子批次。返回子批次 ID 列表与操作 ID。
func (s *Store) Split(parent string, parts []int) (childIDs []string, opID string, err error) {
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("拆分至少需要一份")
	}
	p, ok := s.batches[parent]
	if !ok {
		return nil, "", fmt.Errorf("批次 %s 不存在", parent)
	}
	total := 0
	for _, q := range parts {
		if q <= 0 {
			return nil, "", fmt.Errorf("拆分数量必须为正整数，得到 %d", q)
		}
		total += q
	}
	if total > p.Remaining {
		return nil, "", fmt.Errorf("批次 %s 余量 %d 不足以拆出 %d", parent, p.Remaining, total)
	}
	// 校验全部通过后才变更状态，保证原子性。
	outputs := make([]Portion, 0, len(parts))
	for _, q := range parts {
		id := s.nextBatchID()
		outputs = append(outputs, Portion{BatchID: id, Qty: q})
	}
	op := &Operation{
		ID:      s.nextOpID(),
		Type:    OpSplit,
		Inputs:  []Portion{{BatchID: parent, Qty: total}},
		Outputs: outputs,
	}
	p.Remaining -= total
	for _, o := range outputs {
		s.batches[o.BatchID] = &Batch{ID: o.BatchID, Initial: o.Qty, Remaining: o.Qty}
	}
	s.commit(op)
	childIDs = make([]string, 0, len(outputs))
	for _, o := range outputs {
		childIDs = append(childIDs, o.BatchID)
	}
	return childIDs, op.ID, nil
}

// Merge 合并：从 contribs 中每个母批各取指定数量，合成一个新批次。
// 同一母批可被多次合并共享，只要余量足够。任一来源不足则整体失败，
// 不改动任何母批（原子失败）。
func (s *Store) Merge(contribs []Portion) (batchID, opID string, err error) {
	if len(contribs) == 0 {
		return "", "", fmt.Errorf("合并至少需要一个来源")
	}
	// 先聚合每个母批的总需求，再统一校验，全部通过后才扣减。
	need := make(map[string]int)
	total := 0
	for _, c := range contribs {
		if c.Qty <= 0 {
			return "", "", fmt.Errorf("合并数量必须为正整数，得到 %d", c.Qty)
		}
		p, ok := s.batches[c.BatchID]
		if !ok {
			return "", "", fmt.Errorf("批次 %s 不存在", c.BatchID)
		}
		need[c.BatchID] += c.Qty
		total += c.Qty
		_ = p
	}
	for id, q := range need {
		if s.batches[id].Remaining < q {
			return "", "", fmt.Errorf("批次 %s 余量 %d 不足以合并出 %d", id, s.batches[id].Remaining, q)
		}
	}
	batchID = s.nextBatchID()
	op := &Operation{
		ID:      s.nextOpID(),
		Type:    OpMerge,
		Inputs:  append([]Portion(nil), contribs...),
		Outputs: []Portion{{BatchID: batchID, Qty: total}},
	}
	for id, q := range need {
		s.batches[id].Remaining -= q
	}
	s.batches[batchID] = &Batch{ID: batchID, Initial: total, Remaining: total}
	s.commit(op)
	return batchID, op.ID, nil
}

// Outbound 出库：从批次消耗 qty，无产出批次。
func (s *Store) Outbound(batchID string, qty int) (opID string, err error) {
	if qty <= 0 {
		return "", fmt.Errorf("出库数量必须为正整数，得到 %d", qty)
	}
	p, ok := s.batches[batchID]
	if !ok {
		return "", fmt.Errorf("批次 %s 不存在", batchID)
	}
	if p.Remaining < qty {
		return "", fmt.Errorf("批次 %s 余量 %d 不足以出库 %d", batchID, p.Remaining, qty)
	}
	op := &Operation{
		ID:     s.nextOpID(),
		Type:   OpOutbound,
		Inputs: []Portion{{BatchID: batchID, Qty: qty}},
	}
	p.Remaining -= qty
	s.commit(op)
	return op.ID, nil
}

func (s *Store) commit(op *Operation) {
	s.ops[op.ID] = op
	s.order = append(s.order, op.ID)
}

// Undo 撤销一个操作。仅当该操作没有任何后代消费（即来源图中从它的
// 产出批次出发不可达任何有效操作）时才允许；否则返回 *UndoError，
// 其中携带依赖链。撤销会恢复输入批次的数量并移除产出批次。
func (s *Store) Undo(opID string) error {
	op, ok := s.ops[opID]
	if !ok {
		return fmt.Errorf("操作 %s 不存在", opID)
	}
	if op.Undone {
		return fmt.Errorf("操作 %s 已被撤销", opID)
	}
	if chain := s.dependentChain(op); chain != nil {
		return &UndoError{OpID: opID, Chain: chain}
	}
	// 无后代消费：恢复输入、移除产出。
	for _, in := range op.Inputs {
		s.batches[in.BatchID].Remaining += in.Qty
	}
	for _, out := range op.Outputs {
		delete(s.batches, out.BatchID)
	}
	op.Undone = true
	return nil
}

// dependentChain 在来源图上从 op 的产出批次做 BFS，
// 若可达任何有效（未撤销）操作，返回从 op 到最近一个后代操作的
// 交替 ID 路径（操作、批次、操作……）；无后代时返回 nil。
func (s *Store) dependentChain(op *Operation) []string {
	type node struct {
		isOp bool
		id   string
	}
	prev := make(map[node]node)
	seen := make(map[node]bool)
	var queue []node
	start := node{isOp: true, id: op.ID}
	seen[start] = true
	for _, out := range op.Outputs {
		bn := node{id: out.BatchID}
		if !seen[bn] {
			seen[bn] = true
			prev[bn] = start
			queue = append(queue, bn)
		}
	}
	var found node
	for len(queue) > 0 && found.id == "" {
		cur := queue[0]
		queue = queue[1:]
		if cur.isOp {
			// 到达一个消费后代操作。
			found = cur
			break
		}
		// 批次节点：扩展到消费它的有效操作。
		for _, other := range s.ops {
			if other.Undone || other.ID == op.ID {
				continue
			}
			if !consumes(other, cur.id) {
				continue
			}
			on := node{isOp: true, id: other.ID}
			if seen[on] {
				continue
			}
			seen[on] = true
			prev[on] = cur
			// 操作节点再扩展到其产出批次。
			for _, out := range other.Outputs {
				bn := node{id: out.BatchID}
				if !seen[bn] {
					seen[bn] = true
					prev[bn] = on
					queue = append(queue, bn)
				}
			}
			queue = append(queue, on)
		}
	}
	if found.id == "" {
		return nil
	}
	// 回溯路径：op -> 批次 -> ... -> 后代操作。
	var rev []string
	for n := found; n != start; n = prev[n] {
		rev = append(rev, n.id)
	}
	rev = append(rev, op.ID)
	chain := make([]string, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		chain = append(chain, rev[i])
	}
	return chain
}

func consumes(op *Operation, batchID string) bool {
	for _, in := range op.Inputs {
		if in.BatchID == batchID {
			return true
		}
	}
	return false
}

// CheckConservation 逐批守恒核对：
//  1. 每个批次的 Remaining 等于 Initial 减去所有有效操作的消耗，且非负；
//  2. 每个拆分/合并操作的输入总量等于产出总量；
//  3. 入库无输入、出库无产出；
//  4. 所有数量为正整数。
// 返回第一个发现的违例。
func (s *Store) CheckConservation() error {
	consumed := make(map[string]int)
	for _, id := range s.order {
		op := s.ops[id]
		if op.Undone {
			continue
		}
		inSum, outSum := 0, 0
		for _, in := range op.Inputs {
			if in.Qty <= 0 {
				return fmt.Errorf("操作 %s 含非正输入数量 %d", op.ID, in.Qty)
			}
			if _, ok := s.batches[in.BatchID]; !ok {
				return fmt.Errorf("操作 %s 引用了不存在的批次 %s", op.ID, in.BatchID)
			}
			consumed[in.BatchID] += in.Qty
			inSum += in.Qty
		}
		for _, out := range op.Outputs {
			if out.Qty <= 0 {
				return fmt.Errorf("操作 %s 含非正产出数量 %d", op.ID, out.Qty)
			}
			outSum += out.Qty
		}
		switch op.Type {
		case OpInbound:
			if len(op.Inputs) != 0 || len(op.Outputs) != 1 {
				return fmt.Errorf("入库操作 %s 形状非法", op.ID)
			}
		case OpOutbound:
			if len(op.Outputs) != 0 || len(op.Inputs) != 1 {
				return fmt.Errorf("出库操作 %s 形状非法", op.ID)
			}
		case OpSplit, OpMerge:
			if inSum != outSum {
				return fmt.Errorf("操作 %s 不守恒：输入 %d != 产出 %d", op.ID, inSum, outSum)
			}
		default:
			return fmt.Errorf("操作 %s 类型未知: %s", op.ID, op.Type)
		}
	}
	for id, b := range s.batches {
		want := b.Initial - consumed[id]
		if want != b.Remaining {
			return fmt.Errorf("批次 %s 不守恒：Initial %d - 已消耗 %d = %d，但 Remaining 为 %d",
				id, b.Initial, consumed[id], want, b.Remaining)
		}
		if b.Remaining < 0 {
			return fmt.Errorf("批次 %s 余量为负: %d", id, b.Remaining)
		}
	}
	return nil
}
