package treesched

import (
	"math/rand"
	"testing"
)

// userIDs 返回除虚拟根外的全部节点 ID。
func userIDs(snap map[NodeID]NodeInfo) []NodeID {
	ids := make([]NodeID, 0, len(snap))
	for id := range snap {
		if id != RootID {
			ids = append(ids, id)
		}
	}
	return ids
}

// assertStructureAcyclic 直接检查被测树无环且父子关系双向一致。
func assertStructureAcyclic(t *testing.T, tr *Tree) {
	t.Helper()
	snap := tr.Snapshot()
	for id, info := range snap {
		if id != RootID {
			if _, ok := snap[info.Parent]; !ok {
				t.Fatalf("node %d 指向不存在的父节点 %d", id, info.Parent)
			}
			if info.Parent == id {
				t.Fatalf("node %d 形成自环", id)
			}
		}
		seen := map[NodeID]struct{}{}
		cur := id
		for cur != RootID {
			if _, dup := seen[cur]; dup {
				t.Fatalf("从节点 %d 上溯遇到环", id)
			}
			seen[cur] = struct{}{}
			cur = snap[cur].Parent
		}
	}
	for id, info := range snap {
		for _, c := range info.Children {
			child, ok := snap[c]
			if !ok {
				t.Fatalf("node %d 引用不存在的孩子 %d", id, c)
			}
			if child.Parent != id {
				t.Fatalf("孩子 %d 的父指针为 %d，期望 %d", c, child.Parent, id)
			}
		}
	}
}

// assertMirrors 断言被测树与参考树结构、状态完全一致，且两边账面守恒。
func assertMirrors(t *testing.T, tr *Tree, ref *refTree) {
	t.Helper()
	snap := tr.Snapshot()
	if len(snap) != len(ref.nodes) {
		t.Fatalf("节点数不一致: sut=%d ref=%d", len(snap), len(ref.nodes))
	}
	for id, rn := range ref.nodes {
		info, ok := snap[NodeID(id)]
		if !ok {
			t.Fatalf("被测树缺少节点 %d", id)
		}
		if uint32(info.Parent) != rn.parent {
			t.Fatalf("节点 %d 父指针不一致: sut=%d ref=%d", id, info.Parent, rn.parent)
		}
		if len(info.Children) != len(rn.children) {
			t.Fatalf("节点 %d 孩子数不一致: sut=%d ref=%d", id, len(info.Children), len(rn.children))
		}
		for _, c := range info.Children {
			if _, ok := rn.children[uint32(c)]; !ok {
				t.Fatalf("节点 %d 的孩子 %d 不在参考树中", id, c)
			}
		}
		if info.Blocked != rn.blocked {
			t.Fatalf("节点 %d 阻塞状态不一致", id)
		}
		if info.Closed != rn.closed {
			t.Fatalf("节点 %d 关闭状态不一致", id)
		}
		if info.Delivered != rn.delivered {
			t.Fatalf("节点 %d 已投递字节不一致: sut=%d ref=%d", id, info.Delivered, rn.delivered)
		}
		// 单节点守恒：载入 = 已投递 + 剩余队列。
		if rn.loaded != info.Delivered+info.Queued {
			t.Fatalf("节点 %d 计费不守恒: loaded=%d delivered=%d queued=%d",
				id, rn.loaded, info.Delivered, info.Queued)
		}
	}
	// 全局守恒：载入总量 = 已投递 + 存活队列 + 关闭节点冻结残留。
	loaded, delivered, frozen := ref.conservation()
	var liveQueued int64
	for _, info := range snap {
		if !info.Closed {
			liveQueued += info.Queued
		}
	}
	if loaded != delivered+liveQueued+frozen {
		t.Fatalf("全局计费不守恒: loaded=%d delivered=%d queued=%d frozen=%d",
			loaded, delivered, liveQueued, frozen)
	}
	assertStructureAcyclic(t, tr)
}

func pickID(rng *rand.Rand, ids []NodeID) NodeID {
	return ids[rng.Intn(len(ids))]
}

// TestFuzzAgainstReferenceModel 用参考树模型镜像执行上千次随机操作，
// 覆盖反复重排（含挂到后代、独占挂接）、阻塞、关闭、零载荷与持续调度，
// 每一步都检查无环、结构一致与计费守恒。
func TestFuzzAgainstReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	tr := New()
	ref := newRefTree()

	const n = 12
	for id := NodeID(1); id <= n; id++ {
		parent := NodeID(uint32(rng.Intn(int(id))))
		weight := uint32(1 + rng.Intn(8))
		if err := tr.Add(id, parent, weight); err != nil {
			t.Fatalf("Add(%d): %v", id, err)
		}
		ref.add(uint32(id), uint32(parent), weight)
	}
	// 给每个节点载入初始载荷，部分节点载入零载荷。
	for id := NodeID(1); id <= n; id++ {
		bytes := int64(rng.Intn(400))
		if id%5 == 0 {
			bytes = 0
		}
		if err := tr.Load(id, bytes); err != nil {
			t.Fatalf("Load(%d): %v", id, err)
		}
		ref.load(uint32(id), bytes)
	}
	assertMirrors(t, tr, ref)

	for step := 0; step < 3000; step++ {
		ids := userIDs(tr.Snapshot())
		id := pickID(rng, ids)
		switch rng.Intn(9) {
		case 0, 1: // 普通重排，目标可能是自身或后代
			target := pickID(rng, ids)
			if rng.Intn(4) == 0 {
				target = id
			}
			if err := tr.Reparent(id, target, false); err != nil {
				t.Fatalf("Reparent(%d,%d): %v", id, target, err)
			}
			ref.reparent(uint32(id), uint32(target), false)
		case 2, 3: // 独占挂接
			target := pickID(rng, ids)
			if err := tr.Reparent(id, target, true); err != nil {
				t.Fatalf("Reparent exclusive(%d,%d): %v", id, target, err)
			}
			ref.reparent(uint32(id), uint32(target), true)
		case 4: // 追加载荷，含零载荷
			bytes := int64(rng.Intn(5))
			if err := tr.Load(id, bytes); err != nil {
				t.Fatalf("Load(%d,%d): %v", id, bytes, err)
			}
			ref.load(uint32(id), bytes)
		case 5: // 切换阻塞
			blocked := rng.Intn(2) == 0
			if err := tr.SetBlocked(id, blocked); err != nil {
				t.Fatalf("SetBlocked(%d): %v", id, err)
			}
			ref.setBlocked(uint32(id), blocked)
		case 6: // 关闭中间节点
			if err := tr.Close(id); err != nil {
				t.Fatalf("Close(%d): %v", id, err)
			}
			ref.close(uint32(id))
		case 7: // 调整权重
			weight := uint32(1 + rng.Intn(16))
			if err := tr.SetWeight(id, weight); err != nil {
				t.Fatalf("SetWeight(%d): %v", id, err)
			}
			ref.setWeight(uint32(id), weight)
		case 8: // 调度一小笔预算
			budget := int64(1 + rng.Intn(128))
			got := tr.Schedule(budget)
			if got < 0 || got > budget {
				t.Fatalf("Schedule 返回超界: got=%d budget=%d", got, budget)
			}
		}
		snap := tr.Snapshot()
		ref.syncDelivered(snap)
		assertMirrors(t, tr, ref)
	}

	// 排空：关闭节点的残留永久冻结，其余队列必须全部投递完。
	for i := 0; i < 5000; i++ {
		queued, _ := tr.Totals()
		if queued == 0 || tr.Schedule(256) == 0 {
			break
		}
	}
	snap := tr.Snapshot()
	ref.syncDelivered(snap)
	for id, info := range snap {
		if id == RootID {
			continue
		}
		switch {
		case info.Closed:
			// 关闭节点：已投递不超过载入，残留被冻结。
			if info.Queued < 0 || info.Delivered > ref.nodes[uint32(id)].loaded {
				t.Fatalf("关闭节点 %d 账面异常: queued=%d delivered=%d", id, info.Queued, info.Delivered)
			}
		case info.Blocked:
			// 阻塞节点的自身载荷允许残留，解除后应可排空。
		default:
			if info.Queued != 0 {
				t.Fatalf("存活节点 %d 排空后仍有队列 %d", id, info.Queued)
			}
		}
	}
	// 解除全部阻塞后必须彻底排空。
	for id := range snap {
		if id != RootID {
			_ = tr.SetBlocked(id, false)
		}
	}
	for i := 0; i < 5000; i++ {
		if tr.Schedule(256) == 0 {
			break
		}
	}
	snap = tr.Snapshot()
	ref.syncDelivered(snap)
	var liveQueued int64
	for id, info := range snap {
		if id == RootID {
			continue
		}
		if !info.Closed && info.Queued != 0 {
			t.Fatalf("解除阻塞后节点 %d 仍有队列 %d", id, info.Queued)
		}
		if !info.Closed {
			liveQueued += info.Queued
		}
	}
	if liveQueued != 0 {
		t.Fatalf("仍有存活队列未排空: %d", liveQueued)
	}
	assertMirrors(t, tr, ref)
}
