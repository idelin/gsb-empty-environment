package main

import "testing"

func TestTickAdvancesOwnDiagonal(t *testing.T) {
	c := NewMatrixClock("A", []string{"A", "B", "C"})
	if got := c.Tick(); got != 1 {
		t.Fatalf("Tick() = %d, want 1", got)
	}
	c.Tick()
	if c.rows["A"]["A"] != 2 {
		t.Fatalf("M[A][A] = %d, want 2", c.rows["A"]["A"])
	}
	if c.rows["A"]["B"] != 0 || c.rows["B"]["A"] != 0 {
		t.Fatal("Tick 不应影响其他分量")
	}
}

func TestMergeTakesElementWiseMax(t *testing.T) {
	c := NewMatrixClock("A", []string{"A", "B"})
	c.Tick() // M[A][A]=1

	remote := map[string]map[string]uint64{
		"A": {"A": 0, "B": 3},
		"B": {"A": 0, "B": 3},
	}
	c.Merge("B", remote)

	if c.rows["A"]["B"] != 3 || c.rows["B"]["B"] != 3 {
		t.Fatalf("合并后应学到 B=3，得到 %v", c.rows)
	}
	if c.rows["A"]["A"] != 2 {
		t.Fatalf("接收事件应推进自身分量 M[A][A]=2，得到 %d", c.rows["A"]["A"])
	}

	// 旧消息重放不得回退矩阵。
	stale := map[string]map[string]uint64{"B": {"A": 0, "B": 1}}
	c.Merge("B", stale)
	if c.rows["B"]["B"] != 3 {
		t.Fatalf("陈旧矩阵不应回退认知，M[B][B]=%d", c.rows["B"]["B"])
	}
}

func TestMinVectorIsColumnMinimum(t *testing.T) {
	c := NewMatrixClock("A", []string{"A", "B", "C"})
	c.rows["A"]["A"], c.rows["A"]["B"], c.rows["A"]["C"] = 5, 4, 6
	c.rows["B"]["A"], c.rows["B"]["B"], c.rows["B"]["C"] = 3, 4, 2
	c.rows["C"]["A"], c.rows["C"]["B"], c.rows["C"]["C"] = 5, 1, 6

	min := c.MinVector()
	want := map[string]uint64{"A": 3, "B": 1, "C": 2}
	for k, v := range want {
		if min[k] != v {
			t.Fatalf("MinVector()[%q] = %d, want %d", k, min[k], v)
		}
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	c := NewMatrixClock("A", []string{"A", "B"})
	c.Tick()
	snap := c.Snapshot()
	snap["A"]["A"] = 99
	if c.rows["A"]["A"] != 1 {
		t.Fatal("修改快照不应影响原矩阵")
	}
}
