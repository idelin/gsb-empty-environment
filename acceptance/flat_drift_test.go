package acceptance_test

import (
	"fmt"
	"testing"

	crush "prescreen.local/ps-7261f1735c"
)

func TestFlatIndepSingleOSDDownZeroDrift(t *testing.T) {
	root := crush.NewNode(0, "root", 16)
	for i := 0; i < 16; i++ {
		root.Children = append(root.Children, crush.NewOSD(i+1, 1))
	}
	rule := crush.NewRule("flat", crush.TakeStep(0), crush.ChooseStep(crush.Indep, 3, "osd", 32), crush.EmitStep())
	base := crush.NewMapper(root.Clone(), crush.SplitMix64, rule)
	drifts := 0
	examples := []string{}
	for down := 1; down <= 16; down++ {
		dm := crush.NewMapper(root.Clone(), crush.SplitMix64, rule).SetDown(down, true)
		for pg := uint64(0); pg < 500; pg++ {
			b, _ := base.MapPG(pg, 3, "flat")
			a, _ := dm.MapPG(pg, 3, "flat")
			changed := -1
			for s, id := range b.OSDs {
				if id == down {
					changed = s
				}
			}
			for s := range b.OSDs {
				if s == changed {
					continue
				}
				if b.OSDs[s] != a.OSDs[s] {
					drifts++
					if len(examples) < 5 {
						examples = append(examples, fmt.Sprintf("down=%d pg=%d slot=%d b=%v a=%v changed=%d", down, pg, s, b.OSDs, a.OSDs, changed))
					}
					break
				}
			}
		}
	}
	if drifts != 0 {
		t.Fatalf("found %d flat indep drifts; examples=%v", drifts, examples)
	}
}
