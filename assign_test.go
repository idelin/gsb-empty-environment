package main

import (
	"math"
	"testing"
)

func testFlows(n int) []uint64 {
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	return ids
}

// 相同流 ID 分配结果稳定（同流的包保持同路径的前提）。
func TestAssignDeterministic(t *testing.T) {
	a := NewAssigner([]Path{{"p1", 1}, {"p2", 2}, {"p3", 3}})
	for f := uint64(1); f <= 1000; f++ {
		if a.Assign(f) != a.Assign(f) {
			t.Fatalf("flow %d 分配不稳定", f)
		}
	}
}

// 删除路径只迁移必要流：迁移集合 == 原在被删路径上的流
func TestRemovePathMigratesOnlyNecessary(t *testing.T) {
	ids := testFlows(20000)
	base := []Path{{"p1", 1}, {"p2", 2}, {"p3", 3}}
	as0 := NewAssigner(base).AssignAll(ids)
	as1 := NewAssigner([]Path{{"p1", 1}, {"p3", 3}}).AssignAll(ids)

	onP2 := 0
	for _, f := range ids {
		if as0[f] == "p2" {
			onP2++
			if as1[f] == "p2" {
				t.Fatalf("flow %d 仍留在已删除的 p2", f)
			}
		} else if as1[f] != as0[f] {
			t.Fatalf("flow %d 不在 p2 却被迁移: %s -> %s", f, as0[f], as1[f])
		}
	}
	moved, _ := Migration(as0, as1)
	if moved != onP2 {
		t.Fatalf("迁移数 %d != 原 p2 流数 %d", moved, onP2)
	}
}

// 新增路径：迁移的流全部迁入新路径，旧路径之间零互迁
func TestAddPathMinimalMigration(t *testing.T) {
	ids := testFlows(20000)
	as0 := NewAssigner([]Path{{"p1", 1}, {"p2", 1}, {"p3", 1}}).AssignAll(ids)
	as1 := NewAssigner([]Path{{"p1", 1}, {"p2", 1}, {"p3", 1}, {"p4", 1}}).AssignAll(ids)

	moved, ratio := Migration(as0, as1)
	for _, f := range ids {
		if as0[f] != as1[f] && as1[f] != "p4" {
			t.Fatalf("flow %d 在旧路径间迁移: %s -> %s", f, as0[f], as1[f])
		}
	}
	// 4 条等权路径，理论迁移比例 1/4，允许统计误差
	if math.Abs(ratio-0.25) > 0.03 {
		t.Fatalf("迁移比例 %.3f 偏离期望 0.25 过多", ratio)
	}
	_ = moved
}

// 权重 0 路径不接新流
func TestZeroWeightGetsNoFlows(t *testing.T) {
	ids := testFlows(5000)
	as := NewAssigner([]Path{{"p1", 1}, {"p2", 0}, {"p3", 2}}).AssignAll(ids)
	if n := LoadByPath(as)["p2"]; n != 0 {
		t.Fatalf("权重0路径 p2 分到 %d 条流", n)
	}
}

// 分配比例近似权重比（卡方式容差）
func TestWeightProportional(t *testing.T) {
	ids := testFlows(50000)
	paths := []Path{{"p1", 1}, {"p2", 2}, {"p3", 3}, {"p4", 4}}
	load := LoadByPath(NewAssigner(paths).AssignAll(ids))
	totalW := 10.0
	for _, p := range paths {
		got := float64(load[p.ID]) / float64(len(ids))
		want := p.Weight / totalW
		if math.Abs(got-want) > 0.02 {
			t.Fatalf("路径 %s 占比 %.3f，期望 %.3f", p.ID, got, want)
		}
	}
}
