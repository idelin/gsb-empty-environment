package netsec

import "errors"

// Sentinel errors returned when an NSEC proof cannot establish the required
// closest-encloser / next-closer relationship.
var (
	// ErrNoClosestEncloserProof means no supplied NSEC has the closest
	// encloser as its owner, so the encloser's existence is not proven.
	ErrNoClosestEncloserProof = errors.New("netsec: missing NSEC for closest encloser")
	// ErrNoNextCloserProof means no supplied NSEC covers the next closer
	// name, so the absence of a name closer than the owner is not proven.
	ErrNoNextCloserProof = errors.New("netsec: missing NSEC covering next closer name")
)

// NSECRR is the subset of an NSEC record needed for no-existence proofs.
type NSECRR struct {
	// OwnerName is the NSEC record's own name.
	OwnerName string
	// NextName is the Next Domain Name field, the next existing name in
	// canonical order within the zone.
	NextName string
}

// canonicalCompare implements the canonical DNS name ordering of
// RFC 4034 section 6.1: labels are compared from the root upward, each as a
// case-insensitive octet string, with the shorter name sorting first.
// It returns -1, 0 or 1 as a < b, a == b or a > b.
func canonicalCompare(a, b string) (int, error) {
	la, err := splitLabels(a)
	if err != nil {
		return 0, err
	}
	lb, err := splitLabels(b)
	if err != nil {
		return 0, err
	}

	ca := lowerLabels(la)
	cb := lowerLabels(lb)

	// Compare from the most significant (rightmost) label toward the root
	// of each label array.
	i, j := len(ca)-1, len(cb)-1
	for i >= 0 && j >= 0 {
		switch {
		case ca[i] < cb[j]:
			return -1, nil
		case ca[i] > cb[j]:
			return 1, nil
		}
		i--
		j--
	}

	// All compared labels are equal; the name with fewer labels sorts first.
	switch {
	case len(ca) < len(cb):
		return -1, nil
	case len(ca) > len(cb):
		return 1, nil
	default:
		return 0, nil
	}
}

// canonicalEqual reports whether two names denote the same name regardless
// of case or a trailing dot.
func canonicalEqual(a, b string) (bool, error) {
	cmp, err := canonicalCompare(a, b)
	return cmp == 0, err
}

// Covers reports whether the NSEC interval (OwnerName, NextName) contains
// name. The interval is half-open: the owner itself is not covered, while
// a wrap-around record (NextName <= OwnerName) covers every name strictly
// greater than the owner, including names above the zone apex ordering.
func (n NSECRR) Covers(name string) (bool, error) {
	cmpOwner, err := canonicalCompare(name, n.OwnerName)
	if err != nil {
		return false, err
	}
	if cmpOwner <= 0 {
		return false, nil
	}

	cmpNext, err := canonicalCompare(name, n.NextName)
	if err != nil {
		return false, err
	}

	wraps, err := canonicalCompare(n.NextName, n.OwnerName)
	if err != nil {
		return false, err
	}

	if wraps <= 0 {
		// Wrap-around interval: everything strictly after OwnerName.
		return true, nil
	}
	return cmpNext < 0, nil
}

// VerifyNSECProof checks that records prove the two facts required for a
// wildcard answer per RFC 4035 section 8.2:
//
//   - an NSEC owned by closestEncloser proves the encloser exists; and
//   - an NSEC whose (owner, next) interval covers nextCloser proves that no
//     name one label closer to the answer exists.
//
// A single NSEC may satisfy both facts. The supplied NSECs are assumed by
// the caller to themselves be cryptographically validated.
func VerifyNSECProof(closestEncloser, nextCloser string, records []NSECRR) error {
	haveEncloser := false
	haveCloser := false

	for _, record := range records {
		if !haveEncloser {
			equal, err := canonicalEqual(record.OwnerName, closestEncloser)
			if err != nil {
				return err
			}
			if equal {
				haveEncloser = true
			}
		}

		if !haveCloser {
			covers, err := record.Covers(nextCloser)
			if err != nil {
				return err
			}
			if covers {
				haveCloser = true
			}
		}

		if haveEncloser && haveCloser {
			return nil
		}
	}

	if !haveEncloser {
		return ErrNoClosestEncloserProof
	}
	return ErrNoNextCloserProof
}

// AnalyzeAndVerifyWildcard classifies rr and, when it is a wildcard
// expansion, enforces the NSEC no-closer proof. On success the returned
// analysis carries the SignedName ("*.<closest encloser>") that the caller
// must use to re-verify the signature cryptographically. A non-wildcard
// answer is returned with its concrete signed name and records are ignored.
func AnalyzeAndVerifyWildcard(rr RRSIG, nsecs []NSECRR) (WildcardAnalysis, error) {
	analysis, err := AnalyzeRRSIG(rr)
	if err != nil {
		return WildcardAnalysis{}, err
	}
	if !analysis.WildcardExpanded {
		return analysis, nil
	}
	if err := VerifyNSECProof(analysis.ClosestEncloser, analysis.NextCloserName, nsecs); err != nil {
		return WildcardAnalysis{}, err
	}
	return analysis, nil
}
