package dnssec

// DenialKind selects which denial-of-existence scenario is being validated.
type DenialKind int

const (
	// DenialNXDOMAIN validates the three-part NXDOMAIN proof.
	DenialNXDOMAIN DenialKind = iota
	// DenialNODATA validates that qname exists but qtype is absent.
	DenialNODATA
	// DenialWildcardAnswer validates the wildcard no-closer proof attached
	// to a synthesized positive answer.
	DenialWildcardAnswer
)

// DenialInput is everything needed to authenticate a denial proof.
type DenialInput struct {
	Kind  DenialKind
	Qname Name
	Qtype uint16
	Zone  Name

	// NSEC3 records with their RRSIGs and/or legacy NSEC records.
	NSEC3    []RR
	NSEC3Sig []RR
	NSEC     []RR
	NSECSig  []RR

	ZoneKeys []RR
}

// DenialResult is the validated outcome. A sound proof is Secure; an Opt-Out
// DS delegation is Insecure; every structural failure is Bogus.
type DenialResult struct {
	Status          Status
	Kind            DenialKind
	ClosestEncloser Name
	NextCloser      Name
	Wildcard        Name
	OptOut          bool
	Reason          string
}

// CoversName reports whether the authenticated proof demonstrates that name
// has no node. It satisfies the wildcard-answer no-closer rule.
func (r *DenialResult) CoversName(name Name) bool {
	if r == nil || (r.Status != StatusSecure && r.Status != StatusInsecure) {
		return false
	}
	return name.Equal(r.NextCloser) || name.Equal(r.Wildcard)
}

type nsec3Entry struct {
	rr        RR
	parsed    *NSEC3
	ownerHash []byte
}

// VerifyDenial authenticates an NSEC3 (preferred) or NSEC denial-of-existence
// proof, including wraparound interval handling (RFC 5155) and the Opt-Out
// insecure-delegation rule.
func (v *Validator) VerifyDenial(in *DenialInput) (*DenialResult, error) {
	if in == nil {
		return nil, ErrBogus("nil denial input")
	}
	if !in.Qname.IsSubdomain(in.Zone) {
		return nil, ErrBogus("query name is outside the claimed zone")
	}
	if len(in.NSEC3) > 0 {
		return v.verifyNSEC3Denial(in)
	}
	if len(in.NSEC) > 0 {
		return v.verifyNSECDenial(in)
	}
	return nil, ErrBogus("no NSEC/NSEC3 records supplied")
}

func (v *Validator) verifyNSEC3Denial(in *DenialInput) (*DenialResult, error) {
	// Every NSEC3 RRset must itself be signed by the zone. Group by owner.
	groups := map[string][]RR{}
	var order []string
	entries := make([]nsec3Entry, 0, len(in.NSEC3))

	var salt []byte
	var iterations uint16
	var hashAlg uint8

	for _, rr := range in.NSEC3 {
		n3, err := ParseNSEC3(rr.Rdata)
		if err != nil {
			return nil, ErrBogus("malformed NSEC3 RDATA: " + err.Error())
		}
		ownerHash, err := HashedOwnerName(rr, in.Zone)
		if err != nil {
			return nil, err
		}
		if len(ownerHash) != len(n3.NextHashed) {
			return nil, ErrInvalidHashLen
		}
		if len(entries) == 0 {
			salt, iterations, hashAlg = n3.Salt, n3.Iterations, n3.HashAlg
		} else if string(n3.Salt) != string(salt) || n3.Iterations != iterations || n3.HashAlg != hashAlg {
			return nil, ErrBogus("NSEC3 parameters differ across the proof")
		}
		entries = append(entries, nsec3Entry{rr: rr, parsed: n3, ownerHash: ownerHash})
		key := rr.Name.String()
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], rr)
	}

	for _, key := range order {
		rrset := groups[key]
		if err := v.VerifyAllRRSIGs(rrset, RRSIGsFor(in.NSEC3Sig, rrset[0].Name, TypeNSEC3), in.ZoneKeys); err != nil {
			return nil, err
		}
	}

	hashOf := func(n Name) ([]byte, error) {
		return NSEC3Hash(n, salt, iterations, hashAlg)
	}
	findMatch := func(h []byte) *nsec3Entry {
		for i := range entries {
			if NSEC3Matches(entries[i].ownerHash, h) {
				return &entries[i]
			}
		}
		return nil
	}
	findCover := func(h []byte) *nsec3Entry {
		for i := range entries {
			if NSEC3Covers(entries[i].ownerHash, entries[i].parsed.NextHashed, h) {
				return &entries[i]
			}
		}
		return nil
	}

	switch in.Kind {
	case DenialNODATA:
		// RFC 5155 section 8.6: first look for an exact match on qname. When
		// present, the bitmap alone decides and no triad proof is required.
		qHash, err := hashOf(in.Qname)
		if err != nil {
			return nil, err
		}
		if m := findMatch(qHash); m != nil {
			if m.parsed.HasType(in.Qtype) {
				return nil, ErrBogus("NODATA proof bitmap contains the queried type")
			}
			if m.parsed.HasType(TypeCNAME) {
				return nil, ErrBogus("NODATA proof bitmap contains CNAME")
			}
			if m.parsed.OptOut() && in.Qtype == TypeDS {
				return &DenialResult{
					Status: StatusInsecure,
					Kind:   in.Kind,
					OptOut: true,
				}, nil
			}
			return &DenialResult{
				Status: StatusSecure,
				Kind:   in.Kind,
			}, nil
		}
		// Otherwise fall through to the closest-encloser triad below.
	}

	// (a) Closest encloser: walk qname from the zone apex downward.
	ce, _, ceEntry, err := findClosestEncloser(in.Qname, in.Zone, hashOf, findMatch)
	if err != nil {
		return nil, err
	}
	if ceEntry == nil {
		return nil, ErrBogus("proof lacks an exact-match NSEC3 for the closest encloser")
	}

	nextCloser, err := nextCloserName(in.Qname, ce)
	if err != nil {
		return nil, err
	}
	ncHash, err := hashOf(nextCloser)
	if err != nil {
		return nil, err
	}
	// (b) Next closer coverage (proves the subtree does not exist).
	ncEntry := findCover(ncHash)
	if ncEntry == nil {
		return nil, ErrBogus("no NSEC3 interval covers the next closer name")
	}

	// (c) Wildcard name "*.<closest encloser>".
	wild, err := ce.PrefixLabel("*")
	if err != nil {
		return nil, err
	}
	wildHash, err := hashOf(wild)
	if err != nil {
		return nil, err
	}

	res := &DenialResult{
		Kind:            in.Kind,
		ClosestEncloser: ce,
		NextCloser:      nextCloser,
		Wildcard:        wild,
	}

	// Opt-Out: an unsigned delegation. Meaningful for DS queries when the
	// covering record carries the flag (RFC 5155 section 8.9).
	if ncEntry.parsed.OptOut() && in.Qtype == TypeDS {
		res.Status = StatusInsecure
		res.OptOut = true
		return res, nil
	}

	switch in.Kind {
	case DenialNXDOMAIN:
		wildEntry := findCover(wildHash)
		if wildEntry == nil {
			if m := findMatch(wildHash); m != nil {
				return nil, ErrBogus("wildcard exists as an exact node; NXDOMAIN cannot be proven")
			}
			return nil, ErrBogus("NXDOMAIN proof missing NSEC3 coverage of the wildcard name")
		}
		if wildEntry.parsed.OptOut() && in.Qtype == TypeDS {
			res.Status = StatusInsecure
			res.OptOut = true
			return res, nil
		}
		res.Status = StatusSecure
		return res, nil

	case DenialNODATA:
		// No exact node: the encloser/next-closer/wildcard triad must prove
		// that no wildcard can supply the answer either.
		wildEntry := findCover(wildHash)
		if wildEntry == nil {
			return nil, ErrBogus("NODATA proof missing wildcard coverage for the closest encloser")
		}
		if ncEntry.parsed.OptOut() || wildEntry.parsed.OptOut() {
			res.Status = StatusInsecure
			res.OptOut = true
			return res, nil
		}
		res.Status = StatusSecure
		return res, nil

	case DenialWildcardAnswer:
		if ncEntry.parsed.OptOut() {
			res.Status = StatusInsecure
			res.OptOut = true
			return res, nil
		}
		res.Status = StatusSecure
		return res, nil

	default:
		return nil, ErrBogus("unknown denial kind")
	}
}

func (v *Validator) verifyNSECDenial(in *DenialInput) (*DenialResult, error) {
	groups := map[string][]RR{}
	var order []string
	type parsed struct {
		rr   RR
		nsec *NSEC
	}
	entries := make([]parsed, 0, len(in.NSEC))

	for _, rr := range in.NSEC {
		nsec, err := ParseNSEC(rr.Rdata)
		if err != nil {
			return nil, ErrBogus("malformed NSEC RDATA: " + err.Error())
		}
		entries = append(entries, parsed{rr: rr, nsec: nsec})
		key := rr.Name.String()
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], rr)
	}
	for _, key := range order {
		rrset := groups[key]
		if err := v.VerifyAllRRSIGs(rrset, RRSIGsFor(in.NSECSig, rrset[0].Name, TypeNSEC), in.ZoneKeys); err != nil {
			return nil, err
		}
	}

	findMatch := func(n Name) *parsed {
		for i := range entries {
			if NSECMatches(entries[i].rr.Name, n) {
				return &entries[i]
			}
		}
		return nil
	}
	findCover := func(n Name) *parsed {
		for i := range entries {
			if NSECCovers(entries[i].rr.Name, entries[i].nsec.NextName, n) {
				return &entries[i]
			}
		}
		return nil
	}

	res := &DenialResult{Kind: in.Kind}

	switch in.Kind {
	case DenialNXDOMAIN:
		cover := findCover(in.Qname)
		if cover == nil {
			return nil, ErrBogus("NSEC proof does not cover the query name")
		}
		// The covering record's owner is an existing ancestor on the path;
		// the closest encloser is that owner, and the next closer name is
		// the first label of qname below it.
		ce := cover.rr.Name.Parent()
		if !in.Qname.IsSubdomain(ce) {
			ce = cover.rr.Name
		}
		res.ClosestEncloser = ce
		nc, err := nextCloserName(in.Qname, ce)
		if err != nil {
			return nil, err
		}
		res.NextCloser = nc
		wild, err := ce.PrefixLabel("*")
		if err != nil {
			return nil, err
		}
		res.Wildcard = wild
		if findCover(wild) == nil && findMatch(wild) == nil {
			return nil, ErrBogus("NXDOMAIN proof missing NSEC coverage of the wildcard name")
		}
		res.Status = StatusSecure
		return res, nil

	case DenialNODATA:
		m := findMatch(in.Qname)
		if m == nil {
			return nil, ErrBogus("NODATA NSEC proof must exactly match the query name")
		}
		if m.nsec.HasType(in.Qtype) {
			return nil, ErrBogus("NODATA proof bitmap contains the queried type")
		}
		if m.nsec.HasType(TypeCNAME) {
			return nil, ErrBogus("NODATA proof bitmap contains CNAME")
		}
		res.Status = StatusSecure
		return res, nil

	case DenialWildcardAnswer:
		if findCover(in.Qname) == nil {
			return nil, ErrBogus("wildcard answer: NSEC does not cover the exact query name")
		}
		res.Status = StatusSecure
		return res, nil

	default:
		return nil, ErrBogus("unknown denial kind")
	}
}
