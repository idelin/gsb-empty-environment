package checksumdiff

import "testing"

func TestRowHashSensitiveToSingleField(t *testing.T) {
	base := Row{ID: 42, Name: "alice", Balance: 1000, UpdatedAt: 7, Version: 3}
	changed := base
	changed.Balance++ // 只改一个字段
	if base.Hash() == changed.Hash() {
		t.Fatal("单字段修改后行哈希未变化")
	}
}

func TestRollingXORCommutative(t *testing.T) {
	rows := []Row{
		{ID: 1, Name: "a", Balance: 10},
		{ID: 2, Name: "b", Balance: 20},
		{ID: 1025, Name: "c", Balance: 30}, // 与 ID=1 同桶
	}
	var a, b Index
	for _, r := range rows {
		a.Add(r.ID, r.Hash())
	}
	for i := len(rows) - 1; i >= 0; i-- { // 逆序插入
		b.Add(rows[i].ID, rows[i].Hash())
	}
	if a.Root() != b.Root() {
		t.Fatal("滚动 XOR 校验和与插入顺序有关")
	}
}

func TestRemoveRestoresSignature(t *testing.T) {
	var ix Index
	r := Row{ID: 7, Name: "x", Balance: 1}
	ix.Add(r.ID, r.Hash())
	ix.Remove(r.ID, r.Hash())
	if ix.Root() != (&Index{}).Root() {
		t.Fatal("增删抵消后签名未归零")
	}
	if got := ix.BucketSig(bucketOf(7)).Count; got != 0 {
		t.Fatalf("桶计数应为 0，实际 %d", got)
	}
}

func TestUpdateKeepsCount(t *testing.T) {
	var ix Index
	old := Row{ID: 9, Name: "n", Balance: 1}
	ix.Add(old.ID, old.Hash())
	newRow := old
	newRow.Balance = 2
	ix.Update(newRow.ID, old.Hash(), newRow.Hash())
	sig := ix.BucketSig(bucketOf(9))
	if sig.Count != 1 {
		t.Fatalf("更新不应改变桶计数，实际 %d", sig.Count)
	}
	if sig.XOR != newRow.Hash() {
		t.Fatal("更新后桶 XOR 应等于新行哈希")
	}
}
