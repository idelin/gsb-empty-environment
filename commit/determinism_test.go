package commit_test

import (
	"fmt"
	"math/rand"
	"testing"

	"prescreen.local/ps-3378167e8b/commit"
)

func TestPlanIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	for iter := 0; iter < 40; iter++ {
		inst, _ := genInstance(rng, 1+rng.Intn(4))
		first, err := commit.BuildPlan(inst)
		if err != nil {
			continue // 无解实例跳过，确定性只对可行计划断言
		}
		for rep := 0; rep < 5; rep++ {
			next, err := commit.BuildPlan(inst)
			if err != nil {
				t.Fatalf("iter %d rep %d: %v", iter, rep, err)
			}
			if opsString(first.Ops) != opsString(next.Ops) {
				t.Fatalf("iter %d non-deterministic plan:\n%v\nvs\n%v",
					iter, first.Ops, next.Ops)
			}
			if first.ReadCost != next.ReadCost || first.Reads != next.Reads {
				t.Fatalf("iter %d metric drift", iter)
			}
		}
	}
}

func opsString(ops []commit.Op) string {
	out := ""
	for _, op := range ops {
		out += fmt.Sprintf("%s:%d@%d|", op.Kind, op.Sector, op.Cyl)
	}
	return out
}
