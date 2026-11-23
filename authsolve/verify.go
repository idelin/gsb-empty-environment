package authsolve

import (
	"fmt"
	"sort"
)

// Verify independently checks a witness against the complete problem:
// signatures, artifact binding, permanent-identity uniqueness, threshold
// policy, and robustness under every permitted clock-offset assignment.  It
// does not trust the solver's search state.  If the witness carries a
// coverage proof, the proof is checked as well.
func Verify(p Problem, w Witness) error {
	pp, err := prepareProblem(p)
	if err != nil {
		return err
	}
	ids := witnessIDs(w)
	if len(ids) == 0 {
		// An empty witness can only be valid for a policy that needs no
		// signer; still check the policy rather than returning early.
	}
	selected, err := credentialsByID(pp, ids)
	if err != nil {
		return err
	}

	seenIdentity := make(map[string]bool)
	for _, c := range selected {
		if seenIdentity[c.Identity] {
			return fmt.Errorf("%w: permanent identity %q selected more than once", ErrInvalidInput, c.Identity)
		}
		seenIdentity[c.Identity] = true
	}

	eligible := eligibleCredentials(pp)
	pt, err := buildPolicyTree(pp, eligible)
	if err != nil {
		return err
	}
	var mask uint64
	for _, c := range selected {
		i := credentialIndex(eligible, c.ID)
		if i < 0 {
			return fmt.Errorf("%w: credential %q is not eligible for artifact", ErrInvalidInput, c.ID)
		}
		mask |= uint64(1) << uint(i)
	}
	if !pt.satisfied(mask) {
		return fmt.Errorf("%w: witness does not satisfy threshold policy", ErrInvalidInput)
	}

	expectedSigners := make([]string, 0, len(selected))
	for _, c := range selected {
		expectedSigners = append(expectedSigners, c.Identity)
	}
	expectedSigners = sortedUniqueStrings(expectedSigners)
	if len(w.Signers) > 0 && compareStringSlices(sortedUniqueStrings(w.Signers), expectedSigners) != 0 {
		return fmt.Errorf("%w: witness signer list does not match selected credentials", ErrInvalidInput)
	}

	robust, _, err := witnessRobust(pp, selected)
	if err != nil {
		return err
	}
	if !robust {
		return fmt.Errorf("%w", ErrWitnessNotRobust)
	}

	proofIDs := append([]string(nil), w.Proof.Witness...)
	if len(proofIDs) > 0 {
		sort.Strings(proofIDs)
		if compareStringSlices(proofIDs, ids) != 0 {
			return fmt.Errorf("%w: proof witness does not match witness credentials", ErrInvalidInput)
		}
	}
	if len(w.Proof.Certificates) > 0 {
		if err := verifyCoverage(pp, selected, w.Proof); err != nil {
			return err
		}
	}
	return nil
}

// VerifyProof checks only the time-coverage part of a proof.  It rebuilds all
// difference-constraint systems from the problem and selected witness,
// verifies that every primitive failure atom is covered exactly once, and
// checks every negative-cycle certificate.  Policy and signatures are checked
// by Verify.
func VerifyProof(p Problem, proof Proof) error {
	pp, err := prepareProblem(p)
	if err != nil {
		return err
	}
	selected, err := credentialsByID(pp, proof.Witness)
	if err != nil {
		return err
	}
	return verifyCoverage(pp, selected, proof)
}

func verifyCoverage(p Problem, selected []Credential, proof Proof) error {
	w1, w2, err := windowsOf(p)
	if err != nil {
		return err
	}
	bounds, err := selectedDeviceBounds(p, selected)
	if err != nil {
		return err
	}
	atoms := enumerateFailureAtoms(bounds)
	all := make([]FailureAtom, 0, len(atoms.left)+len(atoms.gap)+len(atoms.right)+len(atoms.empty))
	all = append(all, atoms.left...)
	all = append(all, atoms.gap...)
	all = append(all, atoms.right...)
	all = append(all, atoms.empty...)

	expected := make(map[string]bool, len(all))
	for _, a := range all {
		expected[atomKey(a)] = true
	}
	seen := make(map[string]bool, len(proof.Certificates))
	for _, cert := range proof.Certificates {
		key := atomKey(cert.Atom)
		if !expected[key] {
			return fmt.Errorf("%w: proof contains certificate for unexpected atom %s", ErrInvalidInput, key)
		}
		if seen[key] {
			return fmt.Errorf("%w: duplicate certificate for atom %s", ErrInvalidInput, key)
		}
		seen[key] = true
		if err := verifyInfeasibilityCertificate(p, bounds, w1, w2, cert); err != nil {
			return err
		}
	}
	for _, atom := range all {
		if !seen[atomKey(atom)] {
			return fmt.Errorf("%w: missing certificate for failure atom %s", ErrInvalidInput, atomKey(atom))
		}
	}
	return nil
}

func witnessIDs(w Witness) []string {
	ids := w.Credentials
	if len(ids) == 0 {
		ids = w.CredentialIDs
	}
	if len(ids) == 0 {
		ids = w.Selected
	}
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func credentialsByID(p Problem, ids []string) ([]Credential, error) {
	byID := make(map[string]Credential, len(p.Credentials))
	for _, c := range p.Credentials {
		byID[c.ID] = c
	}
	out := make([]Credential, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("%w: duplicate credential %q in witness", ErrInvalidInput, id)
		}
		seen[id] = true
		c, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("%w: unknown credential %q in witness", ErrInvalidInput, id)
		}
		out = append(out, c)
	}
	return out, nil
}

func eligibleCredentials(p Problem) []Credential {
	out := make([]Credential, 0, len(p.Credentials))
	for _, c := range p.Credentials {
		if c.Artifact == p.Artifact {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Identity != out[j].Identity {
			return out[i].Identity < out[j].Identity
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func credentialIndex(creds []Credential, id string) int {
	for i := range creds {
		if creds[i].ID == id {
			return i
		}
	}
	return -1
}
