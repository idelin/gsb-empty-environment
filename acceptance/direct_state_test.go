package acceptance_test

import (
	"testing"

	crush "prescreen.local/ps-7261f1735c"
)

func TestDirectUpFieldDownZeroDrift(t *testing.T) {
	topo := buildTopology()
	mapper := crush.NewMapper(topo.root, crush.SplitMix64, testRule(crush.Indep))

	for pg := uint64(0); pg < 200; pg++ {
		before, err := mapper.MapPG(pg, 3, "rack-three")
		if err != nil {
			t.Fatal(err)
		}
		downOSD := topo.osds[int(pg)%len(topo.osds)]
		previousUp := downOSD.Up
		downOSD.Up = false

		after, err := mapper.MapPG(pg, 3, "rack-three")
		if err != nil {
			t.Fatal(err)
		}

		changedSlot := -1
		for slot, id := range before.OSDs {
			if id == downOSD.ID {
				changedSlot = slot
			}
		}
		for slot := range before.OSDs {
			if slot == changedSlot {
				continue
			}
			if before.OSDs[slot] != after.OSDs[slot] {
				t.Fatalf("pg %d osd %d slot %d drifted: before=%v after=%v", pg, downOSD.ID, slot, before.OSDs, after.OSDs)
			}
		}
		downOSD.Up = previousUp
	}
}
