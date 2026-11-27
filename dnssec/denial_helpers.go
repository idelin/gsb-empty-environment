package dnssec

// nextCloserName returns the name one label longer than ce on the path from
// ce towards qname (RFC 5155 section 1.3). ce must be a strict suffix of
// qname.
func nextCloserName(qname, ce Name) (Name, error) {
	if qname.LabelCount() <= ce.LabelCount() {
		return Name{}, ErrBogus("closest encloser is not shorter than the query name")
	}
	// The next closer keeps the rightmost (ce.LabelCount()+1) labels of
	// qname, i.e. inserts exactly one additional leftmost label.
	return qname.TrimToLabels(ce.LabelCount() + 1)
}

// findClosestEncloser walks from the full qname down to the zone apex and
// returns the longest existing name (the one with an exact NSEC3 match),
// together with its hash and matching entry. Per RFC 5155 the query name
// itself must not match in an NXDOMAIN proof.
func findClosestEncloser(
	qname, zone Name,
	hashOf func(Name) ([]byte, error),
	findMatch func([]byte) *nsec3Entry,
) (Name, []byte, *nsec3Entry, error) {
	for count := qname.LabelCount() - 1; count >= zone.LabelCount(); count-- {
		candidate, err := qname.TrimToLabels(count)
		if err != nil {
			return Name{}, nil, nil, err
		}
		h, err := hashOf(candidate)
		if err != nil {
			return Name{}, nil, nil, err
		}
		if entry := findMatch(h); entry != nil {
			return candidate, h, entry, nil
		}
	}
	// Fall back to the zone apex itself, which is always an existing node.
	apexHash, err := hashOf(zone)
	if err != nil {
		return Name{}, nil, nil, err
	}
	if entry := findMatch(apexHash); entry != nil {
		return zone, apexHash, entry, nil
	}
	return Name{}, nil, nil, ErrBogus("no exact-match NSEC3 for any encloser including the zone apex")
}
