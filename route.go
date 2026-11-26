package bgpsim

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Origin is the BGP ORIGIN attribute encoded using the standard values:
// IGP=0, EGP=1, INCOMPLETE=2. Lower is preferred.
type Origin int

const (
	// OriginIGP is ORIGIN IGP (i).
	OriginIGP Origin = 0
	// OriginEGP is ORIGIN EGP (e).
	OriginEGP Origin = 1
	// OriginIncomplete is ORIGIN INCOMPLETE (?).
	OriginIncomplete Origin = 2
)

// ParseOrigin parses the usual origin names (case-insensitive):
// "igp", "egp" or "incomplete" (also "?").
func ParseOrigin(s string) (Origin, bool) {
	switch normalizeToken(s) {
	case "igp", "i":
		return OriginIGP, true
	case "egp", "e":
		return OriginEGP, true
	case "incomplete", "?", "inc":
		return OriginIncomplete, true
	default:
		return OriginIGP, false
	}
}

// UnmarshalJSON accepts either the numeric BGP origin code (0/1/2) or the
// usual names ("igp", "egp", "incomplete" / "?").
func (o *Origin) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case float64:
		*o = Origin(int(t))
		return nil
	case string:
		s := strings.TrimSpace(t)
		if n, err := strconv.Atoi(s); err == nil {
			*o = Origin(n)
			return nil
		}
		if parsed, ok := ParseOrigin(s); ok {
			*o = parsed
			return nil
		}
	}
	return fmt.Errorf("bgpsim: invalid Origin %s", string(data))
}

func normalizeToken(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+'a'-'A')
		case r == ' ', r == '_':
			out = append(out, '-')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

// DefaultLocalPref is the LocalPref applied when an ExternalRoute leaves
// LocalPref at its zero value.
const DefaultLocalPref uint32 = 100

// Route is an IPv4 (or generic prefix) BGP route together with the
// simulation-only propagation state attached to advertisements.
//
// External inputs only need the selection attributes plus Egress (the
// border router that received the route). ClusterList, LearnedFrom and
// Local are maintained by the engine.
type Route struct {
	// Prefix is the destination prefix. The engine simulates every
	// distinct prefix independently but in the same synchronous rounds.
	Prefix string `json:"prefix"`

	// Egress is the border router that originally received the external
	// route. The IGP cost used by best-path selection is the cost from
	// the selecting node to this egress.
	Egress NodeID `json:"egress"`

	// RouterID is the final tie-break attribute carried by the route.
	// When empty it defaults to the egress node's configured RouterID.
	RouterID RouterID `json:"router_id,omitempty"`

	// NeighborAS is the first/adjacent AS in AS_PATH; MED is only
	// compared between routes with the same NeighborAS unless
	// Options.AlwaysCompareMED is set.
	NeighborAS uint32 `json:"neighbor_as"`

	MED       uint32 `json:"med,omitempty"`
	LocalPref uint32 `json:"local_pref,omitempty"`
	ASPathLen int    `json:"as_path_len,omitempty"`
	Origin    Origin `json:"origin,omitempty"`

	// ClusterList is the RFC 4456 CLUSTER_LIST carried through
	// reflection. It is empty for freshly injected external routes.
	ClusterList []ClusterID `json:"cluster_list,omitempty"`

	// LearnedFrom is the peer that advertised the currently selected
	// copy. It is empty for locally injected routes.
	LearnedFrom NodeID `json:"learned_from,omitempty"`

	// Local marks a route injected at the selecting node itself.
	Local bool `json:"local,omitempty"`
}

// ExternalRoute is an externally received route supplied in Config.
type ExternalRoute = Route

func (r Route) localPref() uint32 {
	if r.LocalPref == 0 {
		return DefaultLocalPref
	}
	return r.LocalPref
}

func (r Route) clone() Route {
	cp := r
	if r.ClusterList != nil {
		cp.ClusterList = append([]ClusterID(nil), r.ClusterList...)
	}
	return cp
}

func containsCluster(list []ClusterID, id ClusterID) bool {
	for _, c := range list {
		if c == id {
			return true
		}
	}
	return false
}

func appendCluster(list []ClusterID, id ClusterID) []ClusterID {
	out := append([]ClusterID(nil), list...)
	return append(out, id)
}

// canonicalCandidateOrder deterministically orders candidate copies before
// the sequential best-path fold. Ordering by the carried RouterID (then
// egress, advertising peer and cluster list length) makes the result
// independent of Go map iteration and input slice order.
func canonicalCandidateOrder(cs []*candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i].route, cs[j].route
		if c := CompareRouterID(a.RouterID, b.RouterID); c != 0 {
			return c < 0
		}
		if a.Egress != b.Egress {
			return a.Egress < b.Egress
		}
		if a.LearnedFrom != b.LearnedFrom {
			return a.LearnedFrom < b.LearnedFrom
		}
		if len(a.ClusterList) != len(b.ClusterList) {
			return len(a.ClusterList) < len(b.ClusterList)
		}
		for k := range a.ClusterList {
			if a.ClusterList[k] != b.ClusterList[k] {
				return a.ClusterList[k] < b.ClusterList[k]
			}
		}
		return false
	})
}
