package netsec

import "errors"

// Sentinel errors returned by AnalyzeRRSIG.
var (
	// ErrLabelsOverflow means the RRSIG Labels field names more labels than
	// the owner name actually has. Such an RRSIG cannot validate and is
	// treated as forged.
	ErrLabelsOverflow = errors.New("netsec: RRSIG labels exceeds owner label count")
	// ErrBogusStarOwner means Labels equals the owner label count while the
	// owner is a wildcard name. A signature over a literal wildcard owner
	// must exclude the "*" label from its Labels count.
	ErrBogusStarOwner = errors.New("netsec: wildcard owner with RRSIG labels equal to label count")
	// ErrWildcardLabelsMismatch means Labels is below the minimum required
	// for the owner's wildcard shape, so the signature cannot correspond to
	// any legitimate wildcard synthesis of the owner.
	ErrWildcardLabelsMismatch = errors.New("netsec: RRSIG labels inconsistent with wildcard owner")
)

// RRSIG is the subset of an RRSIG resource record needed to decide whether
// the covered RRset is a wildcard expansion.
type RRSIG struct {
	// OwnerName is the owner name of the covered RRset (the answer name).
	OwnerName string
	// Labels is the RRSIG Labels field as carried on the wire.
	Labels int
}

// WildcardAnalysis is the result of classifying an RRSIG against its owner.
type WildcardAnalysis struct {
	// OwnerLabels is the owner name label count, excluding the root and
	// counting any literal "*" label as an ordinary label.
	OwnerLabels int
	// Labels is the RRSIG Labels field, echoed for convenience.
	Labels int
	// WildcardExpanded is true when the covered RRset is the result of
	// wildcard synthesis rather than a concrete owner.
	WildcardExpanded bool
	// ClosestEncloser is the closest name that provably exists. For an
	// expansion it is the owner truncated to its last Labels labels.
	ClosestEncloser string
	// NextCloserName is the name one label longer than the closest
	// encloser along the path to the owner; its non-existence must be
	// proven by NSEC. It is empty for non-wildcard answers.
	NextCloserName string
	// SignedName is the name whose wildcard form must be used to verify
	// the signature. For an expansion it is "*.<closest encloser>";
	// otherwise it is the owner name itself.
	SignedName string
}

// AnalyzeRRSIG applies the RFC 4034 section 3.1 Labels decision.
//
// Let N be the owner name label count (root excluded). The RRSIG Labels
// field cannot exceed N: Labels > N is a forged signature. When Labels == N
// the owner must not be a literal wildcard name ("*.example."). When
// Labels < N the answer is a wildcard expansion: the signature was created
// over "*.<closest encloser>", where the closest encloser is the owner
// truncated to its last Labels labels, and the validator must additionally
// demand an NSEC proof that the next closer name does not exist.
func AnalyzeRRSIG(rr RRSIG) (WildcardAnalysis, error) {
	ownerLabels, err := LabelCount(rr.OwnerName)
	if err != nil {
		return WildcardAnalysis{}, err
	}
	if ownerLabels == 0 {
		return WildcardAnalysis{}, ErrRootName
	}
	if rr.Labels < 0 {
		return WildcardAnalysis{}, ErrLabelsOverflow
	}

	labels, err := splitLabels(rr.OwnerName)
	if err != nil {
		return WildcardAnalysis{}, err
	}

	starOwner := labels[0] == "*"

	// Labels > owner label count: no name with that many labels can be the
	// signer, so the RRSIG is forged.
	if rr.Labels > ownerLabels {
		return WildcardAnalysis{}, ErrLabelsOverflow
	}

	// A literal wildcard owner is itself a concrete record in the zone. Its
	// RRSIG Labels count excludes the leading "*", so it must equal
	// ownerLabels-1. Anything else is inconsistent.
	if starOwner {
		switch {
		case rr.Labels == ownerLabels:
			return WildcardAnalysis{}, ErrBogusStarOwner
		case rr.Labels != ownerLabels-1:
			return WildcardAnalysis{}, ErrWildcardLabelsMismatch
		}
		// The RRSIG covers the wildcard RRset under its own name; this is
		// not a synthesized expansion and needs no NSEC no-closer proof.
		return WildcardAnalysis{
			OwnerLabels:      ownerLabels,
			Labels:           rr.Labels,
			WildcardExpanded: false,
			SignedName:       joinName(labels),
		}, nil
	}

	result := WildcardAnalysis{
		OwnerLabels: ownerLabels,
		Labels:      rr.Labels,
	}

	switch {
	case rr.Labels < ownerLabels:
		// Wildcard expansion. The closest encloser is the suffix of the
		// owner containing Labels labels (Labels == 0 means the root).
		var encloserLabels []string
		if rr.Labels > 0 {
			encloserLabels = labels[ownerLabels-rr.Labels:]
		}
		encloser := joinName(encloserLabels)

		// The next closer name prepends the owner's immediately enclosing
		// label to the closest encloser.
		var nextCloser []string
		if rr.Labels == 0 {
			nextCloser = []string{labels[0]}
		} else {
			nextCloser = append([]string{labels[ownerLabels-rr.Labels-1]}, encloserLabels...)
		}

		var signedLabels []string
		if rr.Labels == 0 {
			signedLabels = []string{"*"}
		} else {
			signedLabels = append([]string{"*"}, encloserLabels...)
		}

		result.WildcardExpanded = true
		result.ClosestEncloser = encloser
		result.NextCloserName = joinName(nextCloser)
		result.SignedName = joinName(signedLabels)
	default:
		// Labels == ownerLabels and the owner is not a wildcard: a normal
		// signature over the concrete owner name.
		result.SignedName = joinName(labels)
	}

	return result, nil
}
