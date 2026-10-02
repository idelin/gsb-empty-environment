package merkle

import (
	"strconv"
	"testing"
)

func makeRecords(n int) [][]byte {
	recs := make([][]byte, n)
	for i := range recs {
		b := []byte("record-")
		b = strconv.AppendInt(b, int64(i), 10)
		recs[i] = b
	}
	return recs
}

// 根相同 → 秒级判定全等，不展开任何节点。
func TestDiffIdenticalSnapshots(t *testing.T) {
	a := Build(makeRecords(100000))
	b := Build(makeRecords(100000))
	if a.Root() != b.Root() {
		t.Fatal("相同记录的根哈希应当一致")
	}
	res := Diff(a, b)
	if len(res.Changes) != 0 {
		t.Fatalf("全等快照不应有差异，得到 %d 条", len(res.Changes))
	}
	if res.Stats.NodesExpanded != 0 {
		t.Fatalf("全等快照不应展开节点，展开了 %d 个", res.Stats.NodesExpanded)
	}
}

// 验收边界：100 万条记录中仅 3 条被修改，
// 展开不到 50 个节点即精准定位 3 条变动明细。
func TestDiffThreeModifiedAmongMillion(t *testing.T) {
	const n = 1000000
	recsA := makeRecords(n)
	treeA := Build(recsA)

	recsB := make([][]byte, n)
	copy(recsB, recsA)
	targets := []uint64{42, 500001, 999999}
	for _, i := range targets {
		b := []byte("record-")
		b = strconv.AppendInt(b, int64(i), 10)
		recsB[i] = append(b, []byte("-UPDATED")...)
	}
	treeB := Build(recsB)

	if treeA.Root() == treeB.Root() {
		t.Fatal("记录被修改后根哈希应当变化")
	}

	res := Diff(treeA, treeB)

	if len(res.Changes) != len(targets) {
		t.Fatalf("应定位出 %d 条变动，得到 %d 条", len(targets), len(res.Changes))
	}
	for i, ch := range res.Changes {
		if ch.Index != targets[i] {
			t.Errorf("第 %d 条变动下标应为 %d，得到 %d", i, targets[i], ch.Index)
		}
		if ch.Kind != Modified {
			t.Errorf("下标 %d 的变动类型应为 MODIFIED，得到 %s", ch.Index, ch.Kind)
		}
		if ch.Old == Zero || ch.New == Zero || ch.Old == ch.New {
			t.Errorf("下标 %d 的修改前后哈希异常", ch.Index)
		}
	}
	if res.Stats.NodesExpanded >= 50 {
		t.Fatalf("展开节点数应小于 50，实际 %d", res.Stats.NodesExpanded)
	}
	t.Logf("100 万条记录 / 3 条修改：展开节点 %d 个，下载哈希 %d 个（全量扫描需比对 %d 片叶子）",
		res.Stats.NodesExpanded, res.Stats.HashesFetched, n)
}

// 新增与删除记录应被识别为 ADDED / REMOVED。
func TestDiffAddedAndRemoved(t *testing.T) {
	recs := makeRecords(100)

	oldTree := Build(recs[:100])
	newTree := Build(recs[:99]) // 删除最后一条
	res := Diff(oldTree, newTree)
	if len(res.Changes) != 1 || res.Changes[0].Kind != Removed || res.Changes[0].Index != 99 {
		t.Fatalf("删除场景结果错误: %+v", res.Changes)
	}

	res = Diff(newTree, oldTree) // 反向即新增
	if len(res.Changes) != 1 || res.Changes[0].Kind != Added || res.Changes[0].Index != 99 {
		t.Fatalf("新增场景结果错误: %+v", res.Changes)
	}
}

// 树高差异很大时（100 万条 vs 3 条）仍能正确枚举全部差异。
func TestDiffVeryDifferentHeights(t *testing.T) {
	recsA := makeRecords(100000)
	recsB := makeRecords(3)
	res := Diff(Build(recsA), Build(recsB))
	// 3 条内容不同（record-0..2 相同，实际相等）+ 99997 条删除
	want := 100000 - 3
	if len(res.Changes) != want {
		t.Fatalf("应有 %d 条变动，得到 %d 条", want, len(res.Changes))
	}
	for _, ch := range res.Changes {
		if ch.Kind != Removed {
			t.Fatalf("下标 %d 应为 REMOVED，得到 %s", ch.Index, ch.Kind)
		}
	}
}

// 空树与边界条件。
func TestDiffEmptyAndEdgeCases(t *testing.T) {
	empty1, empty2 := Build(nil), Build(nil)
	if res := Diff(empty1, empty2); len(res.Changes) != 0 {
		t.Fatal("两棵空树应全等")
	}
	one := Build(makeRecords(1))
	res := Diff(empty1, one)
	if len(res.Changes) != 1 || res.Changes[0].Kind != Added {
		t.Fatalf("空树 vs 单记录树结果错误: %+v", res.Changes)
	}
	if res := Diff(one, empty1); len(res.Changes) != 1 || res.Changes[0].Kind != Removed {
		t.Fatalf("单记录树 vs 空树结果错误: %+v", res.Changes)
	}
}

// 随机扰动场景：与暴力逐条比对的结果交叉验证。
func TestDiffMatchesBruteForce(t *testing.T) {
	const n = 5000
	recsA := makeRecords(n)
	recsB := make([][]byte, n)
	copy(recsB, recsA)
	// 确定性伪随机地修改、删除尾部、新增若干条
	modified := map[int]bool{}
	seed := uint64(12345)
	next := func() int { // xorshift
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(n))
	}
	for i := 0; i < 37; i++ {
		idx := next()
		if !modified[idx] {
			modified[idx] = true
			recsB[idx] = []byte("tampered-" + strconv.Itoa(idx))
		}
	}
	treeA := Build(recsA)
	treeB := Build(recsB[:n-11]) // 再删掉尾部 11 条

	res := Diff(treeA, treeB)
	got := make(map[uint64]ChangeKind, len(res.Changes))
	for _, ch := range res.Changes {
		got[ch.Index] = ch.Kind
	}
	if len(got) != len(modified)+11 {
		t.Fatalf("变动条数应为 %d，得到 %d", len(modified)+11, len(got))
	}
	for idx := range modified {
		if got[uint64(idx)] != Modified {
			t.Errorf("下标 %d 应为 MODIFIED，得到 %s", idx, got[uint64(idx)])
		}
	}
	for i := n - 11; i < n; i++ {
		if got[uint64(i)] != Removed {
			t.Errorf("下标 %d 应为 REMOVED，得到 %s", i, got[uint64(i)])
		}
	}
}
