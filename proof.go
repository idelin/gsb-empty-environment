package nsecproof

import (
	"errors"
	"fmt"
	"strings"
)

// Rcode values relevant to authenticated denial of existence.
const (
	RcodeNXDomain = "NXDOMAIN"
	RcodeNoData   = "NODATA"
)

// Sentinel errors allowing callers to classify verification failures.
var (
	ErrNoNSEC           = errors.New("nsec: proof contains no NSEC records")
	ErrOutsideZone      = errors.New("nsec: queried name is outside the zone")
	ErrUncoveredTarget  = errors.New("nsec: no NSEC interval covers the required target name")
	ErrWildcardMismatch = errors.New("nsec: wildcard expansion does not match the closest encloser")
	ErrNoWildcardProof  = errors.New("nsec: wildcard answer lacks an NSEC owned by the wildcard name")
	ErrQtypePresent     = errors.New("nsec: NODATA denied but the qtype is present in the NSEC type bitmap")
	ErrWildcardExists   = errors.New("nsec: NXDOMAIN contradicted by an existing wildcard at the closest encloser")
)

// CoverageError pinpoints the gap in which a required name falls without
// being covered. Gap names the interval immediately before the target, or the
// wrap gap after the last owner when the target exceeds every interval.
type CoverageError struct {
	Target  Name
	Gap     NSEC
	Wrapped bool
	// GapStart/GapEnd delimit the uncovered stretch that contains Target.
	GapStart Name
	GapEnd   Name
}

func (e *CoverageError) Error() string {
	if e.Wrapped {
		return fmt.Sprintf(
			"%s: target %s is not covered; record %s wraps to apex %s, leaving uncovered interval (%s, %s)",
			ErrUncoveredTarget, e.Target, e.Gap.Owner, e.GapEnd, e.GapStart, e.GapEnd)
	}
	return fmt.Sprintf(
		"%s: target %s is not covered; interval (%s, %s) stops before it, leaving uncovered interval (%s, %s)",
		ErrUncoveredTarget, e.Target, e.Gap.Owner, e.Gap.NextDomain, e.GapStart, e.GapEnd)
}

func (e *CoverageError) Unwrap() error { return ErrUncoveredTarget }

// Proof is a signed denial response reduced to the fields a validator needs:
// the zone apex, the queried name/type, the response rcode and the NSEC RR set
// quoted by the signature.
type Proof struct {
	Zone  Name
	Qname Name
	Qtype string
	Rcode string
	NSECs []NSEC
}

// Verify checks an authenticated denial-of-existence proof per RFC 4035
// section 3.1.3:
//
//  1. every NSEC interval must be well oriented (owner < next domain);
//  2. the next closer name below the closest encloser must be covered by an
//     NSEC interval, proving the qname does not exist;
//  3. a wildcard may only expand "*.<closest encloser>" — the expansion order
//     is checked against the encloser actually proven;
//  4. NODATA additionally requires the qtype to be absent from the bitmap of
//     the denying NSEC (owner NSEC or wildcard NSEC).
func (p Proof) Verify() error {
	if len(p.NSECs) == 0 {
		return ErrNoNSEC
	}
	for _, r := range p.NSECs {
		if err := r.Validate(p.Zone); err != nil {
			return err
		}
		if !r.Owner.IsSubDomain(p.Zone) || !r.NextDomain.IsSubDomain(p.Zone) {
			return fmt.Errorf("nsec: NSEC interval (%s, %s) is outside zone %s", r.Owner, r.NextDomain, p.Zone)
		}
	}
	if !p.Qname.IsSubDomain(p.Zone) {
		return fmt.Errorf("%w: qname %s", ErrOutsideZone, p.Qname)
	}

	encloser := p.closestEncloser()
	nextCloser := nextCloserName(p.Qname, encloser)

	// RFC 4035 3.1.3 steps 1-2: the next closer name must be proven absent.
	ncNSEC, covered := p.covering(nextCloser)
	if !covered {
		return p.uncoveredError(nextCloser)
	}

	// Expansion order check: every wildcard NSEC in the proof must live at
	// exactly "*.<closest encloser>"; a wildcard at any other depth could not
	// have synthesised this qname.
	wantWildcard := wildcardName(encloser)
	for _, r := range p.NSECs {
		if len(r.Owner.labels) > 0 && r.Owner.labels[0] == "*" && !r.Owner.Equal(wantWildcard) {
			return fmt.Errorf("%w: %s cannot expand %s under encloser %s",
				ErrWildcardMismatch, r.Owner, p.Qname, encloser)
		}
	}
	wildcardNSEC, wildcardExists := p.ownerRecord(wantWildcard)

	switch {
	case encloser.Equal(p.Qname):
		// Name exists: plain NODATA denied by its own owner NSEC.
		if p.Rcode != RcodeNoData {
			return nil
		}
		ownerNSEC, _ := p.ownerRecord(p.Qname)
		return denyType(ownerNSEC, p.Qtype)

	case wildcardExists:
		// A real wildcard at the proven encloser synthesises the name.
		if p.Rcode == RcodeNXDomain {
			return fmt.Errorf("%w: %s covers %s", ErrWildcardExists, wantWildcard, p.Qname)
		}
		return denyType(wildcardNSEC, p.Qtype)

	default:
		// Name genuinely does not exist and no wildcard can synthesise it.
		if p.Rcode == RcodeNXDomain {
			return nil
		}
		return denyType(ncNSEC, p.Qtype)
	}
}

func denyType(r NSEC, qtype string) error {
	if r.HasType(qtype) {
		return fmt.Errorf("%w: %s listed on NSEC %s", ErrQtypePresent, strings.ToUpper(qtype), r.Owner)
	}
	return nil
}

// closestEncloser returns the longest suffix of Qname whose name is asserted
// to exist by an NSEC owner (the zone apex always exists).
func (p Proof) closestEncloser() Name {
	for _, suffix := range suffixes(p.Qname) {
		if _, ok := p.ownerRecord(suffix); ok {
			return suffix
		}
	}
	return p.Zone
}

func (p Proof) covering(target Name) (NSEC, bool) {
	for _, r := range p.NSECs {
		if r.Covers(target, p.Zone) {
			return r, true
		}
	}
	return NSEC{}, false
}

func (p Proof) ownerRecord(name Name) (NSEC, bool) {
	for _, r := range p.NSECs {
		if r.Owner.Equal(name) {
			return r, true
		}
	}
	return NSEC{}, false
}

// uncoveredError reports the specific gap that fails to cover target: the
// interval of the greatest owner below target, or the wrap gap after the
// greatest owner overall when target is beyond every interval.
func (p Proof) uncoveredError(target Name) error {
	var best NSEC
	found := false
	for _, r := range p.NSECs {
		if compareNames(r.Owner, target) < 0 && (!found || compareNames(r.Owner, best.Owner) > 0) {
			best = r
			found = true
		}
	}
	if !found {
		return &CoverageError{
			Target:   target,
			Gap:      NSEC{Owner: p.Zone, NextDomain: p.Zone},
			Wrapped:  true,
			GapStart: p.Zone,
			GapEnd:   p.Zone,
		}
	}
	if best.NextDomain.Equal(p.Zone) {
		return &CoverageError{
			Target:   target,
			Gap:      best,
			Wrapped:  true,
			GapStart: best.Owner,
			GapEnd:   p.Zone,
		}
	}
	return &CoverageError{
		Target:   target,
		Gap:      best,
		GapStart: best.NextDomain,
		GapEnd:   target,
	}
}

// suffixes returns every suffix of n including n itself, longest first.
func suffixes(n Name) []Name {
	out := make([]Name, 0, len(n.labels)+1)
	for start := 0; start <= len(n.labels); start++ {
		out = append(out, Name{labels: n.labels[start:], abs: n.abs})
	}
	return out
}

// nextCloserName prepends exactly one qname label to the encloser, producing
// the shortest name that would have to exist for qname to exist.
func nextCloserName(qname, encloser Name) Name {
	keep := len(qname.labels) - len(encloser.labels)
	if keep < 1 {
		return qname
	}
	return Name{labels: qname.labels[keep-1:], abs: qname.abs}
}

// wildcardName returns "*.<encloser>", the only wildcard name allowed to
// expand names one level below the encloser.
func wildcardName(encloser Name) Name {
	labels := make([]string, 0, len(encloser.labels)+1)
	labels = append(labels, "*")
	labels = append(labels, encloser.labels...)
	return Name{labels: labels, abs: encloser.abs}
}
