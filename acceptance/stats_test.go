package acceptance_test

import (
	"testing"

	crush "prescreen.local/ps-7261f1735c"
)

func TestDownSlotRecordsFallback(t *testing.T) {
	topo := buildTopology()
	mapper := crush.NewMapper(topo.root.Clone(), crush.SplitMix64, testRule(crush.Indep))

	var pg uint64
	var downID int
	var slot int
	for candidate := uint64(0); candidate < 1000; candidate++ {
		result, err := mapper.MapPG(candidate, 3, "rack-three")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.OSDs) == 3 {
			pg = candidate
			downID = result.OSDs[0]
			slot = 0
			break
		}
	}

	mapper.SetDown(downID, true)
	result, err := mapper.MapPG(pg, 3, "rack-three")
	if err != nil {
		t.Fatal(err)
	}
	stat := result.Slots[slot]
	if stat.DownCount == 0 && !stat.Down {
		t.Fatalf("expected down diagnostic for slot %d: %+v", slot, stat)
	}
	if !stat.Fallback && stat.FallbackCount == 0 {
		t.Fatalf("expected fallback diagnostic for slot %d: %+v", slot, stat)
	}
}

func TestConflictStatsOccur(t *testing.T) {
	topo := buildTopology()
	mapper := crush.NewMapper(topo.root, crush.SplitMix64, testRule(crush.Indep))

	found := false
	for pg := uint64(0); pg < 20000; pg++ {
		result, err := mapper.MapPG(pg, 3, "rack-three")
		if err != nil {
			t.Fatal(err)
		}
		if result.ConflictCount > 0 {
			found = true
			if len(result.ConflictSlots) == 0 {
				t.Fatalf("conflict count without slots: %+v", result)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected at least one deterministic conflict retry in sample")
	}
}

func TestIndepExhaustionLeavesHole(t *testing.T) {
	root := crush.NewNode(0, "root", 2)
	root.Children = []*crush.Node{crush.NewOSD(1, 1), crush.NewOSD(2, 1)}
	rule := &crush.Rule{Steps: []crush.Step{
		crush.TakeStep(0),
		crush.ChooseStep(crush.Indep, 3, "osd", 4),
		crush.EmitStep(),
	}}
	mapper := crush.NewMapper(root, crush.SplitMix64, rule)
	result, err := mapper.MapPG(7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.OSDs) != 3 {
		t.Fatalf("expected fixed three slots, got %v", result.OSDs)
	}
	holes := 0
	for _, id := range result.OSDs {
		if id == -1 {
			holes++
		}
	}
	if holes == 0 {
		t.Fatalf("expected an independent hole after exhaustion, got %v", result.OSDs)
	}
	if result.MissingCount == 0 {
		t.Fatalf("missing stats not recorded: %+v", result)
	}
}
