package antisync

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// spreadKey 把序号映射到整个键空间，避免测试数据聚集。
func spreadKey(i uint64) uint64 {
	return i * (uint64(1) << 56)
}

func shapeString(n *node) string {
	if n.leaf {
		return fmt.Sprintf("L%d:%d", n.depth, n.count)
	}
	return fmt.Sprintf("I%d(%s|%s)", n.depth, shapeString(n.children[0]), shapeString(n.children[1]))
}

func maxDepthOf(n *node) int {
	if n.leaf {
		return n.depth
	}
	l := maxDepthOf(n.children[0])
	r := maxDepthOf(n.children[1])
	if l > r {
		return l
	}
	return r
}

func mustGet(t *testing.T, r *Replica, key uint64, want string) {
	t.Helper()
	got, ok := r.Get(key)
	if !ok || string(got) != want {
		t.Fatalf("Get(%d) = %q, %v; want %q", key, got, ok, want)
	}
}

// 空范围：两个空副本只需一轮根摘要交换，零记录传输。
func TestSyncEmptyReplicas(t *testing.T) {
	a, b := NewReplica(), NewReplica()
	st := Sync(a, b)
	if st.SummaryExchanges != 1 || st.RecordsSent != 0 || st.RangesPruned != 1 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if a.RootHash() != b.RootHash() {
		t.Fatal("empty replicas must share root hash")
	}
}

// 一侧为空：全部记录流向空副本，双方收敛。
func TestSyncEmptyAgainstPopulated(t *testing.T) {
	a, b := NewReplica(), NewReplica()
	const n = 30
	for i := uint64(0); i < n; i++ {
		a.Put(spreadKey(i), []byte(fmt.Sprintf("v%d", i)), 1)
	}
	st := Sync(a, b)
	if st.RecordsSent != n {
		t.Fatalf("RecordsSent = %d, want %d", st.RecordsSent, n)
	}
	if a.RootHash() != b.RootHash() || b.Len() != n {
		t.Fatalf("not converged: hashEqual=%v b.Len=%d", a.RootHash() == b.RootHash(), b.Len())
	}
	for i := uint64(0); i < n; i++ {
		mustGet(t, b, spreadKey(i), fmt.Sprintf("v%d", i))
	}
}

// 倾斜键：大量键挤在极小范围内，树沿一侧分裂到最大深度，
// 同步仍然正确收敛。
func TestSkewedKeys(t *testing.T) {
	a, b := NewReplica(), NewReplica()
	const n = 100
	for i := uint64(0); i < n; i++ {
		a.Put(i, []byte(fmt.Sprintf("skew-%d", i)), 1)
	}
	if d := maxDepthOf(a.root); d != MaxDepth {
		t.Fatalf("skewed keys should force max depth %d, got %d", MaxDepth, d)
	}
	Sync(a, b)
	if a.RootHash() != b.RootHash() {
		t.Fatal("skewed replicas did not converge")
	}
	for i := uint64(0); i < n; i++ {
		mustGet(t, b, i, fmt.Sprintf("skew-%d", i))
	}
}

// 构建顺序不变性：相同键集合按不同顺序插入（含分裂与墓碑），
// 必须产生相同的树形与根摘要。
func TestBuildOrderInvariant(t *testing.T) {
	var keys []uint64
	for i := uint64(0); i < 30; i++ {
		keys = append(keys, spreadKey(i))
	}
	for i := uint64(0); i < 10; i++ {
		keys = append(keys, i) // 倾斜簇，迫使深层分裂
	}

	orders := [][]uint64{keys, reversed(keys), shuffled(keys, 42)}
	var wantHash uint64
	var wantShape string
	for idx, order := range orders {
		r := NewReplica()
		for _, k := range order {
			r.Put(k, []byte(fmt.Sprintf("v%d", k)), k%3+1)
		}
		// 相同集合的墓碑，插入顺序同样不同。
		for _, k := range order {
			if k%7 == 0 {
				r.Delete(k, 100)
			}
		}
		shape := shapeString(r.root)
		if idx == 0 {
			wantHash, wantShape = r.RootHash(), shape
			continue
		}
		if r.RootHash() != wantHash {
			t.Fatalf("order %d: root hash differs", idx)
		}
		if shape != wantShape {
			t.Fatalf("order %d: tree shape differs\n got: %s\nwant: %s", idx, shape, wantShape)
		}
	}
}

func reversed(in []uint64) []uint64 {
	out := make([]uint64, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

func shuffled(in []uint64, seed int64) []uint64 {
	out := append([]uint64(nil), in...)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

// 墓碑同步：删除通过墓碑传播到对端；墓碑参与摘要，
// 从未见过该键的副本也会收下墓碑。
func TestTombstoneSync(t *testing.T) {
	a, b := NewReplica(), NewReplica()
	a.Put(spreadKey(1), []byte("x"), 1)
	Sync(a, b)
	mustGet(t, b, spreadKey(1), "x")

	a.Delete(spreadKey(1), 2)
	Sync(a, b)
	if _, ok := b.Get(spreadKey(1)); ok {
		t.Fatal("tombstone did not propagate")
	}
	rec, ok := b.LookupRaw(spreadKey(1))
	if !ok || !rec.Deleted || rec.Version != 2 {
		t.Fatalf("want tombstone v2, got %+v ok=%v", rec, ok)
	}
	if a.RootHash() != b.RootHash() {
		t.Fatal("replicas diverged after tombstone sync")
	}

	// 墓碑对从未见过该键的副本同样生效。
	c, d := NewReplica(), NewReplica()
	c.Put(spreadKey(2), []byte("y"), 1)
	c.Delete(spreadKey(2), 2)
	Sync(c, d)
	if d.Len() != 1 {
		t.Fatalf("tombstone must sync to fresh replica, d.Len() = %d", d.Len())
	}
	if _, ok := d.Get(spreadKey(2)); ok {
		t.Fatal("deleted key must not be visible")
	}
	if c.RootHash() != d.RootHash() {
		t.Fatal("tombstone-only replicas diverged")
	}
}

// 数据完全一致的两个副本：只交换一轮根摘要，零记录传输。
func TestIdenticalReplicasCheapSync(t *testing.T) {
	a, b := NewReplica(), NewReplica()
	for i := uint64(0); i < 50; i++ {
		rec := Record{Key: spreadKey(i), Value: []byte(fmt.Sprintf("v%d", i)), Version: 1}
		a.Apply(rec)
		b.Apply(rec)
	}
	st := Sync(a, b)
	if st.RecordsSent != 0 || st.SummaryExchanges != 1 || st.RangesPruned != 1 {
		t.Fatalf("identical replicas should cost one summary round: %+v", st)
	}
}

// 随机分叉：双方各自独立写入后同步，收敛到“逐键取最高版本”的并集。
func TestDivergentReplicasConverge(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	a, b := NewReplica(), NewReplica()
	want := make(map[uint64]Record)
	for i := 0; i < 200; i++ {
		key := spreadKey(uint64(rng.Intn(60)))
		rec := Record{Key: key, Version: uint64(rng.Intn(10) + 1)}
		if rng.Intn(5) == 0 {
			rec.Deleted = true
		} else {
			rec.Value = []byte(fmt.Sprintf("v%d", i))
		}
		if rng.Intn(2) == 0 {
			a.Apply(rec)
		} else {
			b.Apply(rec)
		}
		if cur, ok := want[key]; !ok || wins(rec, cur) {
			want[key] = rec
		}
	}
	Sync(a, b)
	if a.RootHash() != b.RootHash() {
		t.Fatal("divergent replicas did not converge")
	}
	if a.Len() != len(want) {
		t.Fatalf("Len = %d, want %d", a.Len(), len(want))
	}
	for key, rec := range want {
		got, ok := a.LookupRaw(key)
		if !ok || got.Key != rec.Key || got.Version != rec.Version || got.Deleted != rec.Deleted || string(got.Value) != string(rec.Value) {
			t.Fatalf("key %d: got %+v ok=%v, want %+v", key, got, ok, rec)
		}
		if _, ok := b.LookupRaw(key); !ok {
			t.Fatalf("key %d missing on b", key)
		}
	}
}

// 通信计数：仅一个叶范围存在差异时，其余子树被摘要剪枝，
// 记录传输只发生在差异叶上。
func TestPartialDivergencePruning(t *testing.T) {
	a, b := NewReplica(), NewReplica()
	for i := uint64(0); i < 40; i++ {
		rec := Record{Key: spreadKey(i), Value: []byte("common"), Version: 1}
		a.Apply(rec)
		b.Apply(rec)
	}
	base := spreadKey(10)
	for i := uint64(1); i <= 3; i++ {
		a.Put(base+i, []byte("extra"), 1)
	}
	st := Sync(a, b)
	if a.RootHash() != b.RootHash() {
		t.Fatal("replicas did not converge")
	}
	if st.RangesPruned == 0 {
		t.Fatal("unchanged subtrees should be pruned by summary")
	}
	if st.RecordsSent > 12 {
		t.Fatalf("record exchange should be confined to the divergent leaf, got %d", st.RecordsSent)
	}
	for i := uint64(1); i <= 3; i++ {
		mustGet(t, b, base+i, "extra")
	}
}

// 叶摘要冲突必须交换真实记录：同一叶范围内键集合不同，
// 仅靠摘要无法定位，需要逐条比较合并。
func TestLeafConflictExchangesRecords(t *testing.T) {
	a, b := NewReplica(), NewReplica()
	// 两个键落在同一深层叶范围内，但内容不同。
	a.Put(1, []byte("from-a"), 1)
	b.Put(2, []byte("from-b"), 1)
	st := Sync(a, b)
	if st.RecordsSent != 2 {
		t.Fatalf("leaf conflict must exchange real records, got %d", st.RecordsSent)
	}
	mustGet(t, a, 2, "from-b")
	mustGet(t, b, 1, "from-a")
	if !strings.Contains(shapeString(a.root), "L") {
		t.Fatal("sanity: tree should have leaves")
	}
}
