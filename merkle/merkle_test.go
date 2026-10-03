package merkle

import (
	"fmt"
	"testing"
)

func makeRecords(n int) []Record {
	recs := make([]Record, n)
	for i := range recs {
		recs[i] = Record{
			Key:   fmt.Sprintf("key-%07d", i),
			Value: []byte(fmt.Sprintf("value-%07d", i)),
		}
	}
	return recs
}

// 根相同则秒级判定全等：不展开任何节点。
func TestEqualTreesInstant(t *testing.T) {
	recs := makeRecords(1000000)
	a := Build(recs)
	b := Build(makeRecords(1000000))
	if a.Hash != b.Hash {
		t.Fatal("相同记录的根哈希应当一致")
	}
	var stats Stats
	changes := Diff(a, b, &stats)
	if len(changes) != 0 {
		t.Fatalf("全等树不应有差异, 得到 %d 条", len(changes))
	}
	if stats.NodesExpanded != 0 {
		t.Fatalf("全等判定不应展开节点, 展开了 %d 个", stats.NodesExpanded)
	}
}

// 验收边界：100 万条记录中仅 3 条被修改，
// 展开不到 50 个节点即精准定位 3 条变动明细。
func TestMillionRecordsThreeChanges(t *testing.T) {
	const total = 1000000
	oldRecs := makeRecords(total)
	newRecs := makeRecords(total)
	modified := []int{0, 500000, 999999}
	for _, i := range modified {
		newRecs[i].Value = []byte(fmt.Sprintf("tampered-%07d", i))
	}

	a := Build(oldRecs)
	b := Build(newRecs)
	if a.Hash == b.Hash {
		t.Fatal("记录被修改后根哈希应当不同")
	}

	var stats Stats
	changes := Diff(a, b, &stats)

	if len(changes) != len(modified) {
		t.Fatalf("应定位 %d 条变动, 得到 %d 条", len(modified), len(changes))
	}
	for i, ch := range changes {
		want := modified[i]
		if ch.Index != want {
			t.Errorf("第 %d 条变动索引应为 %d, 得到 %d", i, want, ch.Index)
		}
		if ch.Old == nil || string(ch.Old.Value) != fmt.Sprintf("value-%07d", want) {
			t.Errorf("第 %d 条变动旧值不符", i)
		}
		if ch.New == nil || string(ch.New.Value) != fmt.Sprintf("tampered-%07d", want) {
			t.Errorf("第 %d 条变动新值不符", i)
		}
	}
	if stats.NodesExpanded >= 50 {
		t.Fatalf("展开节点数应小于 50, 实际 %d", stats.NodesExpanded)
	}
	t.Logf("100 万条记录 / 3 条变更: 展开 %d 个节点即定位全部变动", stats.NodesExpanded)
}

// 不同位置的变更都能被定位。
func TestDiffVariousPositions(t *testing.T) {
	oldRecs := makeRecords(256)
	newRecs := makeRecords(256)
	for _, i := range []int{0, 1, 15, 16, 128, 255} {
		newRecs[i].Value = []byte("changed")
	}
	var stats Stats
	changes := Diff(Build(oldRecs), Build(newRecs), &stats)
	if len(changes) != 6 {
		t.Fatalf("应有 6 条变动, 得到 %d", len(changes))
	}
	wantIdx := []int{0, 1, 15, 16, 128, 255}
	for i, ch := range changes {
		if ch.Index != wantIdx[i] {
			t.Errorf("变动 %d 索引应为 %d, 得到 %d", i, wantIdx[i], ch.Index)
		}
	}
}

// 记录数不同（新增记录）也能正确回溯。
func TestDiffAppendedRecords(t *testing.T) {
	oldRecs := makeRecords(100)
	newRecs := makeRecords(103)
	var stats Stats
	changes := Diff(Build(oldRecs), Build(newRecs), &stats)
	if len(changes) != 3 {
		t.Fatalf("应有 3 条新增, 得到 %d", len(changes))
	}
	for i, ch := range changes {
		if ch.Index != 100+i {
			t.Errorf("新增记录索引应为 %d, 得到 %d", 100+i, ch.Index)
		}
		if ch.Old != nil {
			t.Errorf("新增记录的 Old 应为 nil")
		}
		if ch.New == nil || ch.New.Key != fmt.Sprintf("key-%07d", 100+i) {
			t.Errorf("新增记录内容不符")
		}
	}
}

// 全部记录不同也能完整列出（退化为叶子级比对，但不漏报）。
func TestDiffAllChanged(t *testing.T) {
	oldRecs := makeRecords(64)
	newRecs := makeRecords(64)
	for i := range newRecs {
		newRecs[i].Value = []byte("x")
	}
	var stats Stats
	changes := Diff(Build(oldRecs), Build(newRecs), &stats)
	if len(changes) != 64 {
		t.Fatalf("应有 64 条变动, 得到 %d", len(changes))
	}
}

func BenchmarkDiffThreeChanges(b *testing.B) {
	oldRecs := makeRecords(1000000)
	newRecs := makeRecords(1000000)
	newRecs[7].Value = []byte("a")
	newRecs[500000].Value = []byte("b")
	newRecs[999999].Value = []byte("c")
	ta := Build(oldRecs)
	tb := Build(newRecs)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var stats Stats
		Diff(ta, tb, &stats)
	}
}
