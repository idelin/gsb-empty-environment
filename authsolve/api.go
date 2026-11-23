package authsolve

// CheckClockEvidence checks only whether the offset evidence is internally
// consistent.  It is useful when the host wants to report clock acquisition
// problems before doing policy or signature work.
func CheckClockEvidence(p Problem) error {
	p = normalizeProblem(p)
	return checkClock(p)
}

// CheckSignatures verifies every eligible credential's Ed25519 signature.
func CheckSignatures(p Problem) error {
	p = normalizeProblem(p)
	return checkAllSignatures(p)
}

// CheckWitness is an alias for Verify.
func CheckWitness(p Problem, w Witness) error { return Verify(p, w) }

// CheckCoverage is an alias for VerifyProof.
func CheckCoverage(p Problem, proof Proof) error { return VerifyProof(p, proof) }

// Robust evaluates the universal clock condition for an explicit fixed set of
// credential IDs.  Policy and signatures are not considered.
func Robust(p Problem, credentialIDs []string) (bool, error) {
	p = normalizeProblem(p)
	if err := checkClock(p); err != nil {
		return false, err
	}
	if _, _, err := windowsOf(p); err != nil {
		return false, err
	}
	selected, err := credentialsByID(p, sortedUniqueStrings(credentialIDs))
	if err != nil {
		return false, err
	}
	ok, _, err := witnessRobust(p, selected)
	return ok, err
}
