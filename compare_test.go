package bgpsim

import "testing"

func baseRoute() Route {
	return Route{
		Prefix: "p", Egress: "e", RouterID: "1.1.1.1",
		NeighborAS: 100, MED: 100, LocalPref: 100, ASPathLen: 3, Origin: OriginIGP,
	}
}

func TestCompareStages(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(x, y *Route)
		cx, cy     int
		acme       bool
		wantBetter int
		wantStage  Stage
		wantIncomp bool
	}{
		{"localpref", func(x, y *Route) { x.LocalPref = 200 }, 0, 0, false, -1, StageLocalPref, false},
		{"aspath", func(x, y *Route) { y.ASPathLen = 5 }, 0, 0, false, -1, StageASPathLen, false},
		{"origin", func(x, y *Route) { y.Origin = OriginEGP }, 0, 0, false, -1, StageOrigin, false},
		{"same-as-med", func(x, y *Route) { x.MED = 10; y.MED = 20 }, 9, 1, false, -1, StageMED, false},
		{
			"different-as-skips-med-and-uses-igp",
			func(x, y *Route) { x.NeighborAS = 10; x.MED = 5; y.NeighborAS = 20; y.MED = 50 },
			9, 1, false, 1, StageIGPCost, true,
		},
		{
			"always-compare-med-across-as",
			func(x, y *Route) { x.NeighborAS = 10; x.MED = 5; y.NeighborAS = 20; y.MED = 50 },
			9, 1, true, -1, StageMED, false,
		},
		{"igp-before-routerid", func(x, y *Route) {}, 1, 2, false, -1, StageIGPCost, false},
		{
			"routerid-when-igp-ties",
			func(x, y *Route) {
				x.RouterID = "1.1.1.1"
				y.RouterID = "2.2.2.2"
				x.NeighborAS = 7
				y.NeighborAS = 7
				x.MED = y.MED
			},
			5, 5, false, -1, StageRouterID, false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x := baseRoute()
			y := baseRoute()
			if tc.mutate != nil {
				tc.mutate(&x, &y)
			}
			v := Compare(x, y, tc.cx, tc.cy, tc.acme)
			if v.Better != tc.wantBetter {
				t.Fatalf("Better=%d want %d (stage %s)", v.Better, tc.wantBetter, v.Stage)
			}
			if v.Stage != tc.wantStage {
				t.Fatalf("Stage=%s want %s", v.Stage, tc.wantStage)
			}
			if v.MEDIncomparable != tc.wantIncomp {
				t.Fatalf("MEDIncomparable=%v want %v", v.MEDIncomparable, tc.wantIncomp)
			}
		})
	}
}

func TestDefaultLocalPrefApplied(t *testing.T) {
	x := baseRoute()
	y := baseRoute()
	x.LocalPref = 0 // becomes DefaultLocalPref 100
	y.LocalPref = 100
	v := Compare(x, y, 0, 0, false)
	if v.Stage != StageTie || v.Better != 0 {
		t.Fatalf("zero LocalPref should default to 100, got stage=%s better=%d", v.Stage, v.Better)
	}
}
