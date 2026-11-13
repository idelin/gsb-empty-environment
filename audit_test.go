package netsec

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func testLocate(coords map[PhysicalPage]Location) func(PhysicalPage) (Location, bool) {
	return func(physical PhysicalPage) (Location, bool) {
		loc, ok := coords[physical]
		return loc, ok
	}
}

func testPage(id, physical int, role Role, movable bool) Page {
	return Page{
		ID:       id,
		Tenant:   fmt.Sprintf("tenant-%d", id%3),
		Role:     role,
		Physical: physical,
		Movable:  movable,
	}
}

func TestAuditCompleteCrossProduct(t *testing.T) {
	limits := Limits{Channels: 2, Banks: 3, Rows: 12}
	coords := map[PhysicalPage]Location{}
	for _, physical := range []int{0, 1} {
		coords[physical] = Location{Channel: 0, Bank: 0, Row: 5}
	}
	coords[2] = Location{Channel: 0, Bank: 0, Row: 4}
	coords[3] = Location{Channel: 0, Bank: 0, Row: 4}
	coords[4] = Location{Channel: 0, Bank: 0, Row: 6}
	coords[5] = Location{Channel: 0, Bank: 0, Row: 6}
	// Same rows but intentionally interleaved into another bank/channel.
	coords[6] = Location{Channel: 0, Bank: 1, Row: 4}
	coords[7] = Location{Channel: 1, Bank: 0, Row: 6}

	pages := []Page{
		testPage(10, 0, Sensitive, false),
		testPage(11, 1, Sensitive, false),
		testPage(20, 2, Attacker, true),
		testPage(21, 3, Attacker, true),
		testPage(30, 4, Attacker, true),
		testPage(31, 5, Attacker, true),
		testPage(40, 6, Attacker, true),
		testPage(41, 7, Attacker, true),
	}
	report, err := Audit(Layout{Pages: pages, Limits: limits, Locate: testLocate(coords)})
	if err != nil {
		t.Fatal(err)
	}
	if report.WitnessCount != 8 || len(report.Witnesses) != 8 {
		t.Fatalf("want 8 cross-product witnesses, got count=%d len=%d",
			report.WitnessCount, len(report.Witnesses))
	}
	seen := make(map[[3]int]bool)
	for _, w := range report.Witnesses {
		seen[[3]int{w.Sensitive, w.AttackerBelow, w.AttackerAbove}] = true
		if w.Channel != 0 || w.Bank != 0 || w.Row != 5 {
			t.Fatalf("unexpected witness geometry: %+v", w)
		}
	}
	for _, s := range []int{10, 11} {
		for _, below := range []int{20, 21} {
			for _, above := range []int{30, 31} {
				if !seen[[3]int{s, below, above}] {
					t.Errorf("missing witness (%d,%d,%d)", s, below, above)
				}
			}
		}
	}
}

func TestAuditBoundaryAndMappingFailure(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 2, Rows: 8}
	coords := map[PhysicalPage]Location{
		0: {Channel: 0, Bank: 0, Row: 0},
		1: {Channel: 0, Bank: 0, Row: 1},
		3: {Channel: 0, Bank: 0, Row: 7},
		4: {Channel: 0, Bank: 0, Row: 6},
		5: {Channel: 0, Bank: 0, Row: 1},
		6: {Channel: 0, Bank: 0, Row: 0},
		7: {Channel: 0, Bank: 0, Row: 2},
	}
	pages := []Page{
		testPage(1, 0, Sensitive, false),
		testPage(13, 1, Attacker, false),
		testPage(4, 3, Sensitive, false),
		testPage(5, 4, Attacker, false),
		// Row 1 with rows 0 and 2 is a valid non-boundary witness.
		testPage(7, 5, Sensitive, false),
		testPage(10, 6, Attacker, false),
		testPage(11, 7, Attacker, false),
		// This attacker would make the row-1 witness complete but is unmapped.
		testPage(8, 99, Attacker, false),
	}
	report, err := Audit(Layout{Pages: pages, Limits: limits, Locate: testLocate(coords)})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Witnesses) != 1 {
		t.Fatalf("want one non-boundary witness, got %+v", report.Witnesses)
	}
	w := report.Witnesses[0]
	if w.Sensitive != 7 || w.AttackerBelow != 10 || w.AttackerAbove != 11 || w.Row != 1 {
		t.Fatalf("unexpected boundary handling: %+v", w)
	}
	if report.MappedPages != 7 || report.UnmappedPages != 1 {
		t.Fatalf("mapping counts: %+v", report)
	}
}

func TestAuditRejectsOutOfRangeMapping(t *testing.T) {
	layout := Layout{
		Limits: Limits{Channels: 1, Banks: 1, Rows: 4},
		Locate: testLocate(map[PhysicalPage]Location{
			7: {Channel: 0, Bank: 0, Row: 4},
		}),
	}
	if _, err := Audit(layout); err == nil {
		t.Fatal("expected an out-of-range mapping error")
	}
}

func TestLayoutValidation(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 8}
	coords := map[PhysicalPage]Location{
		1: {Channel: 0, Bank: 0, Row: 3},
		2: {Channel: 0, Bank: 0, Row: 4},
	}
	cases := []Layout{
		{
			Pages: []Page{
				testPage(1, 1, Sensitive, false),
				testPage(1, 2, Attacker, false),
			},
			Limits: limits,
			Locate: testLocate(coords),
		},
		{
			Pages: []Page{
				testPage(1, 1, Sensitive, false),
				testPage(2, 1, Attacker, false),
			},
			Limits: limits,
			Locate: testLocate(coords),
		},
		{
			Pages:  []Page{{ID: 1, Physical: 1, Role: Role(99)}},
			Limits: limits,
			Locate: testLocate(coords),
		},
		{
			Pages:     []Page{testPage(1, 1, Sensitive, false)},
			FreePages: []PhysicalPage{1},
			Limits:    limits,
			Locate:    testLocate(coords),
		},
		{
			Limits: Limits{Channels: 0, Banks: 1, Rows: 8},
			Locate: testLocate(coords),
		},
	}
	for i, layout := range cases {
		if _, err := Audit(layout); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

// referenceWitnessSet is deliberately independent of enumerateWitnesses.  It
// loops over the complete ordered sensitive/below/above triple space.
func referenceWitnessSet(s *scenario) map[[3]int]bool {
	out := make(map[[3]int]bool)
	for i, sensitive := range s.pages {
		if sensitive.Role != RoleSensitive || !s.mapped[s.initial[i]] {
			continue
		}
		centre := s.location[s.initial[i]]
		for j, below := range s.pages {
			if j == i || below.Role != RoleAttacker || !s.mapped[s.initial[j]] {
				continue
			}
			belowLoc := s.location[s.initial[j]]
			if belowLoc.Channel != centre.Channel || belowLoc.Bank != centre.Bank ||
				belowLoc.Row+1 != centre.Row {
				continue
			}
			for k, above := range s.pages {
				if k == i || k == j || above.Role != RoleAttacker || !s.mapped[s.initial[k]] {
					continue
				}
				aboveLoc := s.location[s.initial[k]]
				if aboveLoc.Channel == centre.Channel && aboveLoc.Bank == centre.Bank &&
					aboveLoc.Row == centre.Row+1 {
					out[[3]int{sensitive.ID, below.ID, above.ID}] = true
				}
			}
		}
	}
	return out
}

func TestAuditAgainstCompleteTripleEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(0x51a7))
	for iteration := 0; iteration < 100; iteration++ {
		limits := Limits{Channels: 2, Banks: 3, Rows: 6}
		coords := make(map[PhysicalPage]Location)
		pages := make([]Page, 0, 14)
		for id := 0; id < 14; id++ {
			if rng.Intn(5) == 0 {
				continue // unmapped page
			}
			coords[id] = Location{
				Channel: rng.Intn(limits.Channels),
				Bank:    rng.Intn(limits.Banks),
				Row:     rng.Intn(limits.Rows),
			}
			role := RoleOrdinary
			switch rng.Intn(4) {
			case 1, 2:
				role = RoleAttacker
			case 3:
				role = RoleSensitive
			}
			pages = append(pages, testPage(id, id, role, rng.Intn(2) == 0))
		}
		layout := Layout{Pages: pages, Limits: limits, Locate: testLocate(coords)}
		s, err := buildScenario(layout, false)
		if err != nil {
			t.Fatal(err)
		}
		want := referenceWitnessSet(s)
		got := make(map[[3]int]bool)
		for _, w := range s.witnesses {
			got[[3]int{w.Sensitive, w.AttackerBelow, w.AttackerAbove}] = true
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("iteration %d mismatch\nwant=%v\ngot=%v", iteration, want, got)
		}
	}
}

func TestWitnessOrdering(t *testing.T) {
	limits := Limits{Channels: 1, Banks: 1, Rows: 5}
	coords := map[PhysicalPage]Location{
		1: {Channel: 0, Bank: 0, Row: 2},
		2: {Channel: 0, Bank: 0, Row: 1},
		3: {Channel: 0, Bank: 0, Row: 3},
		4: {Channel: 0, Bank: 0, Row: 2},
	}
	pages := []Page{
		testPage(9, 1, Sensitive, false),
		testPage(3, 4, Sensitive, false),
		testPage(8, 2, Attacker, false),
		testPage(7, 3, Attacker, false),
	}
	report, err := Audit(Layout{Pages: pages, Limits: limits, Locate: testLocate(coords)})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Witnesses) != 2 {
		t.Fatal(report.Witnesses)
	}
	if report.Witnesses[0].Sensitive != 3 || report.Witnesses[1].Sensitive != 9 {
		t.Fatalf("witnesses are not sorted by sensitive ID: %+v", report.Witnesses)
	}
}
