package padplan

import (
	"math/rand"
	"testing"
)

// TestRandomProperty 对大量随机小实例做三方一致性检查：
// 规划器、内置全分配枚举器（通过 PlanBuckets 的成功返回隐式保证）
// 与测试内独立暴力枚举必须得到相同的字典序最优值。
func TestRandomProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 100; iter++ {
		n := 1 + rng.Intn(6)
		k := 1 + rng.Intn(4)

		buckets := make([]int64, k)
		next := int64(1 + rng.Intn(5))
		for j := range buckets {
			next += int64(1 + rng.Intn(8))
			buckets[j] = next
		}

		objs := make([]ObjectLen, n)
		for i := range objs {
			objs[i] = ObjectLen{
				ID:     string(rune('a' + i)),
				Length: int64(rng.Intn(int(buckets[k-1]) + 2)),
			}
		}
		in := Input{
			Objects:       objs,
			BucketSizes:   buckets,
			MaxExtraBytes: int64(rng.Intn(60)),
		}

		plan, err := PlanBuckets(in)
		bfDistinct, bfPadding, bfFeasible := bruteForceOptimum(in)
		if err != nil {
			if bfFeasible {
				t.Fatalf("iter %d: planner failed (%v) but brute force found optimum (%d, %d); input=%+v",
					iter, err, bfDistinct, bfPadding, in)
			}
			continue
		}
		if !bfFeasible {
			t.Fatalf("iter %d: planner succeeded but brute force found nothing; input=%+v", iter, in)
		}
		if plan.DistinctBuckets != bfDistinct || plan.TotalPadding != bfPadding {
			t.Fatalf("iter %d: plan (%d, %d) != brute force (%d, %d); input=%+v",
				iter, plan.DistinctBuckets, plan.TotalPadding, bfDistinct, bfPadding, in)
		}
	}
}
