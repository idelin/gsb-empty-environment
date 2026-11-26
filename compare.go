package bgpsim

// Stage identifies the best-path step that decided a pairwise comparison.
type Stage string

const (
	StageLocalPref Stage = "LocalPref"
	StageASPathLen Stage = "ASPathLen"
	StageOrigin    Stage = "Origin"
	StageMED       Stage = "MED"
	StageIGPCost   Stage = "IGPCost"
	StageRouterID  Stage = "RouterID"
	StageTie       Stage = "Tie"
)

// Verdict is the result of comparing exactly two candidate routes using
// the standard selection order configured for the simulation.
type Verdict struct {
	// Better is -1 if x is preferred, +1 if y is preferred and 0 on tie.
	Better int `json:"better"`

	// Stage is the first step that strictly differentiated the routes,
	// or StageTie when every step tied.
	Stage Stage `json:"stage"`

	// MEDCompared reports whether MED was actually compared.
	MEDCompared bool `json:"med_compared"`

	// MEDIncomparable reports that the MED step was skipped because the
	// routes came from different neighboring AS numbers while
	// AlwaysCompareMED was disabled. This is the source of the
	// non-transitive preference relation.
	MEDIncomparable bool `json:"med_incomparable,omitempty"`

	// XIGPCost and YIGPCost are the IGP costs used at StageIGPCost.
	XIGPCost int `json:"x_igp_cost"`
	YIGPCost int `json:"y_igp_cost"`
}

// Compare compares two routes according to:
//
//	LocalPref desc, ASPathLen asc, Origin asc,
//	MED asc (only when NeighborAS is equal unless alwaysCompareMED),
//	IGP cost to the egress asc, carried RouterID asc.
//
// xCost/yCost are the selecting node's IGP costs to x.Egress/y.Egress.
// Cluster lists and advertising peers intentionally do not take part in
// the metric order; identical route copies are disambiguated by the
// engine using local-origin and propagation-state tie breaks.
func Compare(x, y Route, xCost, yCost int, alwaysCompareMED bool) Verdict {
	v := Verdict{XIGPCost: xCost, YIGPCost: yCost}

	if x.localPref() != y.localPref() {
		if x.localPref() > y.localPref() {
			v.Better = -1
		} else {
			v.Better = 1
		}
		v.Stage = StageLocalPref
		return v
	}

	if x.ASPathLen != y.ASPathLen {
		if x.ASPathLen < y.ASPathLen {
			v.Better = -1
		} else {
			v.Better = 1
		}
		v.Stage = StageASPathLen
		return v
	}

	if x.Origin != y.Origin {
		if x.Origin < y.Origin {
			v.Better = -1
		} else {
			v.Better = 1
		}
		v.Stage = StageOrigin
		return v
	}

	sameNeighborAS := x.NeighborAS == y.NeighborAS
	switch {
	case alwaysCompareMED:
		v.MEDCompared = true
		if x.MED != y.MED {
			if x.MED < y.MED {
				v.Better = -1
			} else {
				v.Better = 1
			}
			v.Stage = StageMED
			return v
		}
	case sameNeighborAS:
		v.MEDCompared = true
		if x.MED != y.MED {
			if x.MED < y.MED {
				v.Better = -1
			} else {
				v.Better = 1
			}
			v.Stage = StageMED
			return v
		}
	default:
		// Different adjacent AS: MED values must not be compared. The
		// selection falls through to IGP cost, which is exactly where
		// MED-induced non-transitivity enters.
		v.MEDIncomparable = true
	}

	if xCost != yCost {
		if xCost < yCost {
			v.Better = -1
		} else {
			v.Better = 1
		}
		v.Stage = StageIGPCost
		return v
	}

	if c := CompareRouterID(x.RouterID, y.RouterID); c != 0 {
		v.Better = c
		v.Stage = StageRouterID
		return v
	}

	v.Stage = StageTie
	v.Better = 0
	return v
}
