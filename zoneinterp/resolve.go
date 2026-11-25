package zoneinterp

// Kind enumerates interpreter conclusions.
type Kind string

const (
	Positive Kind = "positive"
	Referral Kind = "referral"
	NoData   Kind = "nodata"
	NXDomain Kind = "nxdomain"
)

// Answer is the interpreter's conclusion for one query.
type Answer struct {
	Kind        Kind
	Records     []Record
	Cut         Name
	Closest     Name
	ViaWildcard bool
}

// Resolve answers query against the full materialized tree. It walks whole
// labels from the apex, stops at delegation cuts, applies the single-label
// wildcard rule at the closest existing ancestor, and distinguishes an
// existing empty name (NoData) from a missing name (NXDomain).
func (t *Tree) Resolve(query Name) *Answer {
	cur := t.nodes[""]
	answer := &Answer{Closest: Name{}}

	for depth := 1; depth <= len(query); depth++ {
		if cur.Delegate {
			cut := append(Name{}, query[:depth-1]...)
			answer.Kind = Referral
			answer.Closest = cut
			answer.Cut = append(Name{}, cut...)
			answer.Records = append([]Record(nil), cur.Records...)
			return answer
		}
		label := query[depth-1]
		next := cur.children[label]
		if next == nil {
			closestPath := append(Name{}, query[:depth-1]...)
			answer.Closest = closestPath
			// A wildcard covers exactly one missing label.
			if depth == len(query) {
				if wild := cur.children["*"]; wild != nil {
					answer.Kind = Positive
					answer.ViaWildcard = true
					answer.Records = append([]Record(nil), wild.Records...)
					return answer
				}
			}
			answer.Kind = NXDomain
			return answer
		}
		cur = next
	}

	answer.Closest = append(Name{}, query...)
	if cur.Delegate {
		answer.Kind = Referral
		answer.Cut = append(Name{}, query...)
		answer.Records = append([]Record(nil), cur.Records...)
		return answer
	}
	answer.Kind = NoData
	answer.Records = append([]Record(nil), cur.Records...)
	return answer
}

// ResolveType applies a record-type filter on top of Resolve: an existing
// name without a record of the requested type yields NoData, while wildcard
// synthesis and referral behavior are unchanged.
func (t *Tree) ResolveType(query Name, recordType string) *Answer {
	answer := t.Resolve(query)
	if answer.Kind != Positive && answer.Kind != NoData {
		return answer
	}
	filtered := make([]Record, 0, len(answer.Records))
	for _, rec := range answer.Records {
		if rec.Type == recordType {
			filtered = append(filtered, rec)
		}
	}
	if len(filtered) == 0 {
		return &Answer{
			Kind:        NoData,
			Closest:     append(Name{}, answer.Closest...),
			ViaWildcard: answer.ViaWildcard,
		}
	}
	answer.Records = filtered
	return answer
}
