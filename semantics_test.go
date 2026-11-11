package netsec

import "testing"

func TestAddCheckedOverflow(t *testing.T) {
	max := ^uint32(0)
	cases := []struct {
		name  string
		v     uint32
		delta int64
		ok    bool
		want  uint32
	}{
		{"positive safe", 10, 5, true, 15},
		{"negative safe", 10, -5, true, 5},
		{"positive overflow", max - 1, 10, false, 0},
		{"negative underflow", 2, -5, false, 0},
		{"boundary exact max", max - 10, 10, true, max},
		{"boundary exact zero", 10, -10, true, 0},
		{"zero delta", 42, 0, true, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := addChecked(tc.v, tc.delta)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Fatalf("addChecked(%d, %d) = (%d, %v), want (%d, %v)", tc.v, tc.delta, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestShiftIntervalSplitsOverflow(t *testing.T) {
	iv := Interval{Lo: 10, Hi: 20}
	safe, bad := shiftInterval(iv, -15)
	if len(safe) != 1 || safe[0] != (Interval{Lo: 0, Hi: 5}) {
		t.Fatalf("unexpected safe pieces: %+v", safe)
	}
	if len(bad) != 1 || bad[0] != (Interval{Lo: 10, Hi: 14}) {
		t.Fatalf("unexpected overflow pieces: %+v", bad)
	}
	// Every individual value must agree with the concrete check.
	for v := iv.Lo; v <= iv.Hi; v++ {
		_, concreteOK := addChecked(v, -15)
		symbolicSafe := false
		for _, piece := range safe {
			if piece.Contains(v - 15) {
				symbolicSafe = true
			}
		}
		symbolicBad := false
		for _, piece := range bad {
			if piece.Contains(v) {
				symbolicBad = true
			}
		}
		if concreteOK != symbolicSafe {
			t.Fatalf("mismatch at %d: concrete=%v symbolic=%v", v, concreteOK, symbolicSafe)
		}
		if concreteOK == symbolicBad {
			t.Fatalf("bad-piece classification wrong at %d: concrete=%v symbolicBad=%v", v, concreteOK, symbolicBad)
		}
		if v == iv.Hi {
			break
		}
	}
}

func TestFilterFirstMatchAndDefaultAllow(t *testing.T) {
	node := &Node{
		ID: 1,
		Filters: []FilterRule{
			{Name: "drop-low", Field: Src, Match: Interval{Lo: 0, Hi: 9}, Allow: false},
			{Name: "allow-ten", Field: Src, Match: Interval{Lo: 10, Hi: 10}, Allow: true},
			{Name: "drop-high", Field: Src, Match: Interval{Lo: 10, Hi: 100}, Allow: false},
		},
	}
	if allow, rule := applyFilter(node, Packet{Src: 10, Dst: 1}); !allow || rule != "allow-ten" {
		t.Fatalf("first match must win: allow=%v rule=%q", allow, rule)
	}
	if allow, _ := applyFilter(node, Packet{Src: 50, Dst: 1}); allow {
		t.Fatal("packet in drop-high must be dropped")
	}
	if allow, _ := applyFilter(node, Packet{Src: 200, Dst: 1}); !allow {
		t.Fatal("no matching rule must default to allow")
	}
}

func TestStepRectMatchesConcreteSemantics(t *testing.T) {
	node := &Node{
		ID: 7,
		Filters: []FilterRule{
			{Name: "drop", Field: Dst, Match: Interval{Lo: 0, Hi: 4}, Allow: false},
		},
		Translates: []TranslateRule{
			{
				Name:   "snat",
				Target: Src,
				Match:  Interval{Lo: 0, Hi: 50},
				Delta:  100,
			},
			{
				Name:        "guard-dnat",
				Target:      Dst,
				Match:       Interval{Lo: 5, Hi: 20},
				GuardActive: true,
				GuardField:  Src,
				Guard:       Interval{Lo: 100, Hi: 200},
				Delta:       -5,
			},
		},
	}
	in := Rect{S: Interval{Lo: 0, Hi: 80}, D: Interval{Lo: 0, Hi: 30}}
	survivors, filtered, overflow := stepRect(node, in)
	if len(overflow) != 0 {
		t.Fatalf("unexpected overflow: %+v", overflow)
	}
	if len(filtered) != 1 || filtered[0].D != (Interval{Lo: 0, Hi: 4}) {
		t.Fatalf("bad filtered region: %+v", filtered)
	}
	// Exhaustive point-level check that symbolic survivors exactly reproduce
	// the concrete transfer function.
	for s := uint32(0); s <= 80; s++ {
		for d := uint32(0); d <= 30; d++ {
			p := Packet{Src: s, Dst: d}
			next, outcome, _ := stepPacket(node, p)
			if outcome != OutcomePassed {
				continue
			}
			if !anyRectContains(survivors, next) {
				t.Fatalf("concrete result %+v missing from symbolic survivors %+v", next, survivors)
			}
		}
	}
}

func anyRectContains(rects []Rect, p Packet) bool {
	for _, r := range rects {
		if r.containsPacket(p) {
			return true
		}
	}
	return false
}

func TestRectSubtractAndCanonicalize(t *testing.T) {
	r := Rect{S: Interval{Lo: 0, Hi: 10}, D: Interval{Lo: 0, Hi: 10}}
	o := Rect{S: Interval{Lo: 4, Hi: 6}, D: Interval{Lo: 4, Hi: 6}}
	parts := r.subtract(o)
	// Corners inside o must be absent from the difference.
	if anyRectContains(parts, Packet{Src: 5, Dst: 5}) {
		t.Fatal("subtraction left an interior point")
	}
	if !anyRectContains(parts, Packet{Src: 0, Dst: 0}) {
		t.Fatal("subtraction removed an exterior corner")
	}
	merged := canonicalize([]Rect{
		{S: Interval{Lo: 0, Hi: 1}, D: Interval{Lo: 0, Hi: 0}},
		{S: Interval{Lo: 2, Hi: 3}, D: Interval{Lo: 0, Hi: 0}},
	})
	if len(merged) != 1 || merged[0].S != (Interval{Lo: 0, Hi: 3}) {
		t.Fatalf("adjacent rectangles should merge: %+v", merged)
	}
}
