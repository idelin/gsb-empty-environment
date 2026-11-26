package acceptance_test

import (
	"testing"

	crush "prescreen.local/ps-7261f1735c"
)

func TestOmittedReweightDefaultsToOne(t *testing.T) {
	root := &crush.Node{ID: 0, Type: "root", Weight: 4, Up: true}
	for i := 0; i < 4; i++ {
		rack := &crush.Node{ID: 10 + i, Type: "rack", Weight: 1, Up: true}
		root.Children = append(root.Children, rack)
		for j := 0; j < 2; j++ {
			osd := &crush.Node{ID: 20 + i*2 + j, Type: "osd", Weight: 1, Up: true}
			rack.Children = append(rack.Children, osd)
		}
	}
	rule := crush.NewRule("r", crush.TakeStep(0), crush.ChooseLeafStep(crush.Indep, 3, "rack", 32), crush.EmitStep())
	mapper := crush.NewMapper(root, crush.SplitMix64, rule)
	before, err := mapper.MapPG(42, 3)
	if err != nil || before.HasHole() {
		t.Fatalf("before=%v err=%v init=%v", before, err, mapper)
	}

	mapper.SetDown(before.OSDs[0], true)
	after, err := mapper.MapPG(42, 3)
	if err != nil || after.HasHole() {
		t.Fatalf("after=%v err=%v", after, err)
	}
	for slot := 1; slot < 3; slot++ {
		if before.OSDs[slot] != after.OSDs[slot] {
			t.Fatalf("slot %d drifted: before=%v after=%v", slot, before.OSDs, after.OSDs)
		}
	}
}
