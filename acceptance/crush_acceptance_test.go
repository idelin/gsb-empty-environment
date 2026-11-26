package acceptance_test

import (
	"math"
	"testing"

	crush "prescreen.local/ps-7261f1735c"
)

type topology struct {
	root  *crush.Node
	racks []*crush.Node
	hosts []*crush.Node
	osds  []*crush.Node
}

func buildTopology() *topology {
	t := &topology{}
	t.root = crush.NewNode(1000, "root", 64)
	for r := 0; r < 4; r++ {
		rack := crush.NewNode(100+r, "rack", 16)
		t.racks = append(t.racks, rack)
		t.root.Children = append(t.root.Children, rack)
		for h := 0; h < 2; h++ {
			host := crush.NewNode(200+r*2+h, "host", 8)
			t.hosts = append(t.hosts, host)
			rack.Children = append(rack.Children, host)
			for d := 0; d < 2; d++ {
				osd := crush.NewOSD(300+r*4+h*2+d, 4)
				t.osds = append(t.osds, osd)
				host.Children = append(host.Children, osd)
			}
		}
	}
	return t
}

func rackOfOSD(topo *topology, osdID int) int {
	for _, rack := range topo.racks {
		for _, osd := range rackChildren(rack) {
			if osd.ID == osdID {
				return rack.ID
			}
		}
	}
	return -1
}

func rackChildren(rack *crush.Node) []*crush.Node {
	var osds []*crush.Node
	rack.Walk(func(n *crush.Node) {
		if n.NodeType() == "osd" {
			osds = append(osds, n)
		}
	})
	return osds
}

func testRule(mode crush.Mode) *crush.Rule {
	return &crush.Rule{
		Name: "rack-three",
		Mode: mode,
		Steps: []crush.Step{
			{Op: crush.OpTake, Take: 1000},
			{Op: crush.OpChooseLeaf, Mode: mode, Count: 3, Type: "rack", MaxAttempts: 128, LocalAttempts: 128},
			{Op: crush.OpEmit},
		},
	}
}

func TestFaultDomainsAndDeterminism(t *testing.T) {
	for _, mode := range []crush.Mode{crush.Indep, crush.FirstN} {
		t.Run(string(mode), func(t *testing.T) {
			topo := buildTopology()
			mapper := crush.NewMapper(topo.root, crush.SplitMix64, testRule(mode))

			for pg := uint64(0); pg < 5000; pg++ {
				first, err := mapper.MapPG(pg, 3, "rack-three")
				if err != nil {
					t.Fatalf("pg %d: %v", pg, err)
				}
				second, err := mapper.MapPG(pg, 3, "rack-three")
				if err != nil {
					t.Fatalf("pg %d: %v", pg, err)
				}
				if len(first.OSDs) != 3 || first.HasHole() {
					t.Fatalf("pg %d: expected three OSDs, got %v stats=%+v", pg, first.OSDs, first.Slots)
				}
				if !equalInts(first.OSDs, second.OSDs) {
					t.Fatalf("pg %d: nondeterministic mapping %v vs %v", pg, first.OSDs, second.OSDs)
				}

				seenOSD := map[int]bool{}
				seenRack := map[int]bool{}
				for _, id := range first.OSDs {
					if id < 0 || seenOSD[id] {
						t.Fatalf("pg %d: invalid/duplicate OSD %v", pg, first.OSDs)
					}
					seenOSD[id] = true
					rack := rackOfOSD(topo, id)
					if rack < 0 || seenRack[rack] {
						t.Fatalf("pg %d: rack constraint violated %v racks via %v", pg, first.OSDs, rackIDs(topo, first.OSDs))
					}
					seenRack[rack] = true
				}
			}
		})
	}
}

func TestIndepSingleOSDDownZeroDrift(t *testing.T) {
	topo := buildTopology()
	base := crush.NewMapper(topo.root.Clone(), crush.SplitMix64, testRule(crush.Indep))

	for _, downOSD := range topo.osds {
		downMapper := crush.NewMapper(topo.root.Clone(), crush.SplitMix64, testRule(crush.Indep))
		downMapper.SetDown(downOSD.ID, true)

		for pg := uint64(0); pg < 1000; pg++ {
			before, err := base.MapPG(pg, 3, "rack-three")
			if err != nil {
				t.Fatal(err)
			}
			after, err := downMapper.MapPG(pg, 3, "rack-three")
			if err != nil {
				t.Fatal(err)
			}
			if len(after.OSDs) != 3 || after.HasHole() {
				t.Fatalf("osd %d pg %d: missing after down: %v", downOSD.ID, pg, after.OSDs)
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
					t.Fatalf("osd %d pg %d slot %d drifted: before=%v after=%v changedSlot=%d",
						downOSD.ID, pg, slot, before.OSDs, after.OSDs, changedSlot)
				}
			}
			for _, id := range after.OSDs {
				if id == downOSD.ID {
					t.Fatalf("osd %d still mapped after down for pg %d: %v", downOSD.ID, pg, after.OSDs)
				}
			}
		}
	}
}

func TestEntireRackDownFallsBack(t *testing.T) {
	topo := buildTopology()
	downRack := topo.racks[0]
	downMapper := crush.NewMapper(topo.root.Clone(), crush.SplitMix64, testRule(crush.Indep))
	downRackClone := downMapper.Root().FindNode(downRack.ID)
	downRackClone.Walk(func(n *crush.Node) {
		downMapper.SetDown(n.ID, true)
	})

	healthyRacks := map[int]bool{topo.racks[1].ID: true, topo.racks[2].ID: true, topo.racks[3].ID: true}
	for pg := uint64(0); pg < 5000; pg++ {
		result, err := downMapper.MapPG(pg, 3, "rack-three")
		if err != nil {
			t.Fatal(err)
		}
		if result.HasHole() || len(result.OSDs) != 3 {
			t.Fatalf("pg %d: could not recover three OSDs: %+v", pg, result)
		}
		seenRack := map[int]bool{}
		for _, id := range result.OSDs {
			rack := rackOfOSD(topo, id)
			if rack == downRack.ID {
				t.Fatalf("pg %d mapped into down rack: %v", pg, result.OSDs)
			}
			if !healthyRacks[rack] || seenRack[rack] {
				t.Fatalf("pg %d: bad rack fallback %v", pg, rackIDs(topo, result.OSDs))
			}
			seenRack[rack] = true
		}
	}
}

func TestZeroWeightNeverSelected(t *testing.T) {
	topo := buildTopology()
	mapper := crush.NewMapper(topo.root, crush.SplitMix64, testRule(crush.FirstN))
	zeroID := topo.osds[0].ID
	mapper.SetWeight(zeroID, 0)
	mapper.SetReweight(topo.osds[1].ID, 0)

	for pg := uint64(0); pg < 5000; pg++ {
		result, err := mapper.MapPG(pg, 3, "rack-three")
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range result.OSDs {
			if id == zeroID || id == topo.osds[1].ID {
				t.Fatalf("pg %d selected zero-weight OSD %d: %v", pg, id, result.OSDs)
			}
		}
	}
}

func TestWeightedDistributionWithinTolerance(t *testing.T) {
	root := crush.NewNode(0, "root", 0)
	weights := []float64{1, 1, 2, 2, 3, 3, 4, 4}
	for i, w := range weights {
		root.Children = append(root.Children, crush.NewOSD(10+i, w))
	}
	rule := &crush.Rule{
		Name: "one",
		Steps: []crush.Step{
			crush.TakeStep(0),
			crush.ChooseStep(crush.FirstN, 1, "osd", 16),
			crush.EmitStep(),
		},
	}
	mapper := crush.NewMapper(root, crush.SplitMix64, rule)

	const n = 300000
	counts := make(map[int]int, len(weights))
	var totalWeight float64
	for _, w := range weights {
		totalWeight += w
	}
	for pg := uint64(0); pg < n; pg++ {
		result, err := mapper.MapPG(pg, 1, "one")
		if err != nil || len(result.OSDs) != 1 || result.OSDs[0] < 0 {
			t.Fatalf("pg %d: result=%v err=%v", pg, result, err)
		}
		counts[result.OSDs[0]]++
	}

	for i, w := range weights {
		id := 10 + i
		expected := w / totalWeight
		actual := float64(counts[id]) / n
		sigma := math.Sqrt(expected * (1 - expected) / n)
		if math.Abs(actual-expected) > 5*sigma+0.002 {
			t.Fatalf("osd %d weight %.1f: proportion %.5f want %.5f sigma %.5f count %d",
				id, w, actual, expected, sigma, counts[id])
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func rackIDs(topo *topology, osdIDs []int) []int {
	out := make([]int, len(osdIDs))
	for i, id := range osdIDs {
		out[i] = rackOfOSD(topo, id)
	}
	return out
}
