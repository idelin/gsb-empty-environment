package entitymerge

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// genRecords 生成多来源、带噪声的企业记录：不同公司可能同名，
// 同一公司的记录存在大小写/空白差异，部分记录缺字段。
func genRecords(rng *rand.Rand, nCompanies int) []Record {
	var out []Record
	rid := 0
	for i := 0; i < nCompanies; i++ {
		name := fmt.Sprintf("acme%d", i%7)
		city := []string{"sh", "bj", "gz"}[rng.Intn(3)]
		taxid := fmt.Sprintf("T%d", i)
		phone := fmt.Sprintf("021-%d", 1000+i)
		n := 1 + rng.Intn(3)
		for s := 0; s < n; s++ {
			fields := map[string]string{"city": city, "taxid": taxid}
			if rng.Intn(2) == 0 {
				fields["name"] = name
			} else {
				fields["name"] = " " + name + " "
			}
			if rng.Intn(3) > 0 {
				fields["phone"] = phone
			}
			if rng.Intn(4) == 0 {
				delete(fields, "taxid")
			}
			out = append(out, rec(fmt.Sprintf("r%d", rid), fields))
			rid++
		}
	}
	return out
}

func TestIncrementalMatchesFullRecompute(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1024, 20261109} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runIncrementalVsFull(t, seed)
		})
	}
}

func runIncrementalVsFull(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	pool := genRecords(rng, 60)
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })

	rules := testRules()
	inc := New(rules...)

	var added []Record
	var retracted []Evidence
	pending := make([]Record, len(pool))
	copy(pending, pool)

	check := func(step int) {
		t.Helper()
		before := inc.Entities()
		inc.RecomputeAll()
		if after := inc.Entities(); !reflect.DeepEqual(before, after) {
			t.Fatalf("step %d: incremental != full recompute\nincremental: %v\nrecomputed: %v", step, before, after)
		}
	}

	for step := 0; step < 400 && (len(pending) > 0 || len(inc.Evidences()) > 0); step++ {
		if len(pending) > 0 && (len(inc.Evidences()) == 0 || rng.Intn(100) < 75) {
			r := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			inc.AddRecord(r)
			added = append(added, r)
		} else {
			evs := inc.Evidences()
			ev := evs[rng.Intn(len(evs))]
			if !inc.Retract(ev.Rule, ev.A, ev.B) {
				t.Fatalf("step %d: retract %+v failed", step, ev)
			}
			retracted = append(retracted, ev)
		}
		check(step)
	}

	// 独立对照：全新引擎按相同顺序重放记录与撤销，结果必须一致。
	fresh := New(rules...)
	for _, r := range added {
		fresh.AddRecord(r)
	}
	for _, ev := range retracted {
		fresh.Retract(ev.Rule, ev.A, ev.B)
	}
	if !reflect.DeepEqual(inc.Entities(), fresh.Entities()) {
		t.Fatalf("incremental != fresh replay\nincremental: %v\nfresh: %v", inc.Entities(), fresh.Entities())
	}
}
