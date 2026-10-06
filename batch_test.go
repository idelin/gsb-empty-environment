package batch

import (
	"strings"
	"testing"
)

func mustInbound(t *testing.T, s *Store, qty int) (string, string) {
	t.Helper()
	b, op, err := s.Inbound(qty)
	if err != nil {
		t.Fatalf("Inbound(%d) 失败: %v", qty, err)
	}
	return b, op
}

func remaining(t *testing.T, s *Store, id string) int {
	t.Helper()
	b, ok := s.Batch(id)
	if !ok {
		t.Fatalf("批次 %s 不存在", id)
	}
	return b.Remaining
}

func checkConservation(t *testing.T, s *Store) {
	t.Helper()
	if err := s.CheckConservation(); err != nil {
		t.Fatalf("守恒检查失败: %v", err)
	}
}

// 部分拆分：母批保留余量，子批数量精确，逐批守恒。
func TestPartialSplit(t *testing.T) {
	s := NewStore()
	parent, _ := mustInbound(t, s, 100)

	children, _, err := s.Split(parent, []int{30, 20})
	if err != nil {
		t.Fatalf("Split 失败: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("期望 2 个子批，得到 %d", len(children))
	}
	if got := remaining(t, s, parent); got != 50 {
		t.Fatalf("母批余量期望 50，得到 %d", got)
	}
	if got := remaining(t, s, children[0]); got != 30 {
		t.Fatalf("子批 1 期望 30，得到 %d", got)
	}
	if got := remaining(t, s, children[1]); got != 20 {
		t.Fatalf("子批 2 期望 20，得到 %d", got)
	}

	// 超量拆分必须失败且不改状态。
	if _, _, err := s.Split(parent, []int{51}); err == nil {
		t.Fatal("超量拆分应失败")
	}
	if got := remaining(t, s, parent); got != 50 {
		t.Fatalf("失败后母批余量应保持 50，得到 %d", got)
	}
	checkConservation(t, s)
}

// 合并共享：同一母批被多次合并取料，余量逐次扣减。
func TestMergeSharing(t *testing.T) {
	s := NewStore()
	a, _ := mustInbound(t, s, 100)
	b, _ := mustInbound(t, s, 10)
	c, _ := mustInbound(t, s, 5)

	m1, _, err := s.Merge([]Portion{{BatchID: a, Qty: 40}, {BatchID: b, Qty: 10}})
	if err != nil {
		t.Fatalf("第一次合并失败: %v", err)
	}
	m2, _, err := s.Merge([]Portion{{BatchID: a, Qty: 25}, {BatchID: c, Qty: 5}})
	if err != nil {
		t.Fatalf("第二次合并失败: %v", err)
	}
	if got := remaining(t, s, a); got != 35 {
		t.Fatalf("A 余量期望 35，得到 %d", got)
	}
	if got := remaining(t, s, m1); got != 50 {
		t.Fatalf("M1 期望 50，得到 %d", got)
	}
	if got := remaining(t, s, m2); got != 30 {
		t.Fatalf("M2 期望 30，得到 %d", got)
	}
	checkConservation(t, s)
}

// 原子失败：多来源合并中任一来源不足，所有母批数量不变、不产生操作记录。
func TestMergeAtomicFailure(t *testing.T) {
	s := NewStore()
	a, _ := mustInbound(t, s, 40)
	b, _ := mustInbound(t, s, 10)
	opsBefore := len(s.Operations())

	_, _, err := s.Merge([]Portion{{BatchID: a, Qty: 40}, {BatchID: b, Qty: 999}})
	if err == nil {
		t.Fatal("来源不足的合并应失败")
	}
	if got := remaining(t, s, a); got != 40 {
		t.Fatalf("失败后 A 应保持 40，得到 %d", got)
	}
	if got := remaining(t, s, b); got != 10 {
		t.Fatalf("失败后 B 应保持 10，得到 %d", got)
	}
	if len(s.Operations()) != opsBefore {
		t.Fatalf("失败操作不应留下记录，操作数期望 %d，得到 %d", opsBefore, len(s.Operations()))
	}

	// 引用不存在批次同样原子失败。
	if _, _, err := s.Merge([]Portion{{BatchID: a, Qty: 5}, {BatchID: "B999", Qty: 1}}); err == nil {
		t.Fatal("引用不存在批次的合并应失败")
	}
	if got := remaining(t, s, a); got != 40 {
		t.Fatalf("失败后 A 应保持 40，得到 %d", got)
	}
	checkConservation(t, s)
}

// 撤销祖先被拒绝：后代已消费产出，即使各批次剩余总量足够也不能撤销，
// 且错误中携带从祖先到消费后代的依赖链。
func TestUndoAncestorRejectedWithChain(t *testing.T) {
	s := NewStore()
	parent, inOp := mustInbound(t, s, 100)
	children, splitOp, err := s.Split(parent, []int{60})
	if err != nil {
		t.Fatalf("Split 失败: %v", err)
	}
	child := children[0]
	outOp, err := s.Outbound(child, 15)
	if err != nil {
		t.Fatalf("Outbound 失败: %v", err)
	}

	// 全库剩余总量 85 >= 入库的 100? 不足；构造总量足够的情形：
	// 再入库使总量充足，撤销仍必须被拒绝——不能凭总量足够就撤销。
	mustInbound(t, s, 100)

	err = s.Undo(inOp)
	if err == nil {
		t.Fatal("后代已消费，撤销入库应被拒绝")
	}
	ue, ok := err.(*UndoError)
	if !ok {
		t.Fatalf("期望 *UndoError，得到 %T: %v", err, err)
	}
	// 依赖链指向最近的阻塞后代：入库 -> 母批 -> 拆分。
	// （拆分本身即消费了母批，无需延伸到更远的出库。）
	_ = outOp
	want := []string{inOp, parent, splitOp}
	if strings.Join(ue.Chain, ",") != strings.Join(want, ",") {
		t.Fatalf("依赖链期望 %v，得到 %v", want, ue.Chain)
	}
	// 状态未被破坏。
	if got := remaining(t, s, parent); got != 40 {
		t.Fatalf("母批余量期望 40，得到 %d", got)
	}
	checkConservation(t, s)
}

// 撤销拆分被直接出库母批所阻：依赖链不经过中间批次。
func TestUndoSplitRejectedByDirectOutbound(t *testing.T) {
	s := NewStore()
	parent, _ := mustInbound(t, s, 100)
	_, splitOp, err := s.Split(parent, []int{30})
	if err != nil {
		t.Fatalf("Split 失败: %v", err)
	}
	// 直接从母批出库，母批是拆分操作的输入而非产出，不阻塞撤销。
	if _, err := s.Outbound(parent, 10); err != nil {
		t.Fatalf("Outbound 失败: %v", err)
	}
	// 拆分的产出子批未被消费，撤销应成功。
	if err := s.Undo(splitOp); err != nil {
		t.Fatalf("撤销拆分应成功: %v", err)
	}
	if got := remaining(t, s, parent); got != 100-10 {
		t.Fatalf("撤销后母批余量期望 90，得到 %d", got)
	}
	checkConservation(t, s)
}

// 自底向上撤销：先撤销后代，再撤销祖先即可成功，批次被移除、数量恢复。
func TestUndoBottomUp(t *testing.T) {
	s := NewStore()
	parent, inOp := mustInbound(t, s, 100)
	children, splitOp, err := s.Split(parent, []int{60})
	if err != nil {
		t.Fatalf("Split 失败: %v", err)
	}
	child := children[0]
	outOp, err := s.Outbound(child, 15)
	if err != nil {
		t.Fatalf("Outbound 失败: %v", err)
	}

	// 撤销出库：数量回到子批。
	if err := s.Undo(outOp); err != nil {
		t.Fatalf("撤销出库失败: %v", err)
	}
	if got := remaining(t, s, child); got != 60 {
		t.Fatalf("撤销出库后子批期望 60，得到 %d", got)
	}
	// 撤销拆分：子批移除，母批恢复。
	if err := s.Undo(splitOp); err != nil {
		t.Fatalf("撤销拆分失败: %v", err)
	}
	if _, ok := s.Batch(child); ok {
		t.Fatal("撤销拆分后子批应被移除")
	}
	if got := remaining(t, s, parent); got != 100 {
		t.Fatalf("撤销拆分后母批期望 100，得到 %d", got)
	}
	// 撤销入库：母批移除。
	if err := s.Undo(inOp); err != nil {
		t.Fatalf("撤销入库失败: %v", err)
	}
	if _, ok := s.Batch(parent); ok {
		t.Fatal("撤销入库后母批应被移除")
	}
	// 重复撤销报错。
	if err := s.Undo(inOp); err == nil {
		t.Fatal("重复撤销应报错")
	}
	checkConservation(t, s)
}

// 撤销合并：任一来源批次后续被其他操作消费不影响撤销（输入侧不阻塞），
// 但合并产出被出库则阻塞。
func TestUndoMerge(t *testing.T) {
	s := NewStore()
	a, _ := mustInbound(t, s, 50)
	b, _ := mustInbound(t, s, 50)
	m, mergeOp, err := s.Merge([]Portion{{BatchID: a, Qty: 20}, {BatchID: b, Qty: 20}})
	if err != nil {
		t.Fatalf("Merge 失败: %v", err)
	}
	// 母批 A 之后又被出库，不阻塞合并的撤销。
	if _, err := s.Outbound(a, 5); err != nil {
		t.Fatalf("Outbound 失败: %v", err)
	}
	// 合并产出被出库则阻塞。
	outOp, err := s.Outbound(m, 10)
	if err != nil {
		t.Fatalf("Outbound 失败: %v", err)
	}
	if err := s.Undo(mergeOp); err == nil {
		t.Fatal("合并产出已被出库，撤销应被拒绝")
	} else if ue, ok := err.(*UndoError); !ok {
		t.Fatalf("期望 *UndoError，得到 %T", err)
	} else if ue.Chain[0] != mergeOp || ue.Chain[len(ue.Chain)-1] != outOp {
		t.Fatalf("依赖链应从 %s 到 %s，得到 %v", mergeOp, outOp, ue.Chain)
	}
	// 撤销出库后再撤销合并：两个母批各自恢复。
	if err := s.Undo(outOp); err != nil {
		t.Fatalf("撤销出库失败: %v", err)
	}
	if err := s.Undo(mergeOp); err != nil {
		t.Fatalf("撤销合并失败: %v", err)
	}
	if got := remaining(t, s, a); got != 45 {
		t.Fatalf("A 期望 45（50 - 5 出库），得到 %d", got)
	}
	if got := remaining(t, s, b); got != 50 {
		t.Fatalf("B 期望 50，得到 %d", got)
	}
	if _, ok := s.Batch(m); ok {
		t.Fatal("撤销合并后产出批次应被移除")
	}
	checkConservation(t, s)
}

// 出库越界与非法数量。
func TestOutboundValidation(t *testing.T) {
	s := NewStore()
	b, _ := mustInbound(t, s, 10)
	if _, err := s.Outbound(b, 11); err == nil {
		t.Fatal("超量出库应失败")
	}
	if _, err := s.Outbound(b, 0); err == nil {
		t.Fatal("零出库应失败")
	}
	if _, err := s.Outbound("B999", 1); err == nil {
		t.Fatal("不存在批次出库应失败")
	}
	if got := remaining(t, s, b); got != 10 {
		t.Fatalf("失败后余量应保持 10，得到 %d", got)
	}
	checkConservation(t, s)
}
