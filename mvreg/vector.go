package mvreg

// VersionVector is a causal clock keyed by replica ID.
type VersionVector map[string]int64

func cloneVector(v VersionVector) VersionVector {
	out := make(VersionVector, len(v))
	for replica, counter := range v {
		out[replica] = counter
	}
	return out
}

func mergeVectors(a, b VersionVector) VersionVector {
	out := cloneVector(a)
	for replica, counter := range b {
		if counter > out[replica] {
			out[replica] = counter
		}
	}
	return out
}

func compareVectors(a, b VersionVector) relation {
	aLess := false
	bLess := false

	for replica, aCounter := range a {
		if bCounter := b[replica]; aCounter < bCounter {
			aLess = true
		} else if aCounter > bCounter {
			bLess = true
		}
	}
	for replica, bCounter := range b {
		if _, exists := a[replica]; !exists && bCounter > 0 {
			aLess = true
		}
	}

	switch {
	case aLess && bLess:
		return concurrent
	case aLess:
		return before
	case bLess:
		return after
	default:
		return equal
	}
}

type relation int

const (
	equal relation = iota
	before
	after
	concurrent
)
