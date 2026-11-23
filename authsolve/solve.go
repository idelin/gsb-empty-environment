package authsolve

import (
	"fmt"
	"math/bits"
	"sort"
)

const maxAutomaticallyEnumeratedCredentials = 20

// Solve finds the minimum-cardinality fixed witness that both satisfies the
// threshold policy and is robust under every offset assignment permitted by
// the clock evidence.
//
// Ties are broken by the lexicographically smallest sorted permanent-ID
// sequence, then by the lexicographically smallest sorted credential-ID
// sequence, so the result is deterministic.
func Solve(p Problem) (Witness, error) {
	prepared, err := prepareProblem(p)
	if err != nil {
		return Witness{}, err
	}
	w, err := solvePrepared(prepared)
	if err != nil {
		return Witness{}, err
	}
	if err := Verify(prepared, w); err != nil {
		return Witness{}, fmt.Errorf("%w: solver produced invalid witness: %v", ErrInvalidInput, err)
	}
	return w, nil
}

// SolveResult is the proof-oriented spelling of Solve.
func SolveResult(p Problem) (Result, error) { return Solve(p) }

// SolveAuthorization is an alias kept for release-host integration code.
func SolveAuthorization(p Problem) (Witness, error) { return Solve(p) }

// Resolve is an alias for Solve.
func Resolve(p Problem) (Witness, error) { return Solve(p) }

// Authorize is an alias for Solve.
func Authorize(p Problem) (Witness, error) { return Solve(p) }

func prepareProblem(p Problem) (Problem, error) {
	p = normalizeProblem(p)
	if _, _, err := windowsOf(p); err != nil {
		return Problem{}, err
	}
	ref, err := referenceDevice(p)
	if err != nil {
		return Problem{}, err
	}
	// Copy slices that we may normalise.
	p.Devices = append([]Device(nil), p.Devices...)
	p.Credentials = append([]Credential(nil), p.Credentials...)
	p.Clock = append([]ClockConstraint(nil), p.Clock...)
	p.Windows = append([]Window(nil), p.Windows...)

	seenDevices := make(map[string]bool, len(p.Devices))
	for i := range p.Devices {
		if p.Devices[i].ID == "" {
			return Problem{}, fmt.Errorf("%w: device %d has an empty ID", ErrInvalidInput, i)
		}
		if seenDevices[p.Devices[i].ID] {
			return Problem{}, fmt.Errorf("%w: duplicate device ID %q", ErrInvalidInput, p.Devices[i].ID)
		}
		seenDevices[p.Devices[i].ID] = true
	}
	if _, ok := seenDevices[ref]; ref != "" && !ok {
		return Problem{}, fmt.Errorf("%w: reference device %q is not in Devices", ErrInvalidInput, ref)
	}

	seenCredentialIDs := make(map[string]bool, len(p.Credentials))
	for i := range p.Credentials {
		c := &p.Credentials[i]
		if c.ID == "" {
			c.ID = fmt.Sprintf("credential-%d", i)
		}
		if seenCredentialIDs[c.ID] {
			return Problem{}, fmt.Errorf("%w: duplicate credential ID %q", ErrInvalidInput, c.ID)
		}
		seenCredentialIDs[c.ID] = true
		if c.Identity == "" {
			c.Identity = c.ID
		}
		if !seenDevices[c.Device] {
			return Problem{}, fmt.Errorf("%w: credential %q uses unknown device %q", ErrInvalidInput, c.ID, c.Device)
		}
	}
	// Signature errors are a distinct failure class and must not be treated
	// as "this credential simply does not match".
	if err := checkAllSignatures(p); err != nil {
		return Problem{}, err
	}
	if err := checkClock(p); err != nil {
		return Problem{}, err
	}
	return p, nil
}

func deviceKeys(p Problem) map[string][]byte {
	out := make(map[string][]byte, len(p.Devices))
	for _, d := range p.Devices {
		if len(d.PublicKey) > 0 {
			out[d.ID] = append([]byte(nil), d.PublicKey...)
		}
	}
	return out
}

func checkAllSignatures(p Problem) error {
	keys := deviceKeys(p)
	for _, c := range p.Credentials {
		if c.Artifact != p.Artifact {
			continue
		}
		if err := c.VerifySignature(keys[c.Device]); err != nil {
			return err
		}
	}
	return nil
}

func solvePrepared(p Problem) (Witness, error) {
	var eligible []Credential
	for _, c := range p.Credentials {
		if c.Artifact == p.Artifact {
			eligible = append(eligible, c)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].Identity != eligible[j].Identity {
			return eligible[i].Identity < eligible[j].Identity
		}
		return eligible[i].ID < eligible[j].ID
	})

	n := len(eligible)
	if n > maxAutomaticallyEnumeratedCredentials {
		return Witness{}, fmt.Errorf("%w: %d eligible credentials exceeds exhaustive-search limit %d",
			ErrInvalidInput, n, maxAutomaticallyEnumeratedCredentials)
	}
	// The specification caps candidate signers at twelve.  The search is over
	// credential subsets, so keep the explicit guard small and predictable.
	identities := make(map[string]bool)
	for _, c := range eligible {
		identities[c.Identity] = true
	}
	if len(identities) > 12 {
		return Witness{}, fmt.Errorf("%w: %d candidate signers exceeds limit 12", ErrInvalidInput, len(identities))
	}

	pt, err := buildPolicyTree(p, eligible)
	if err != nil {
		return Witness{}, err
	}
	var best Witness
	haveBest := false
	for count := 0; count <= n; count++ {
		foundAtCount := false
		var searchErr error
		forEachCombination(n, count, func(mask uint64) bool {
			if searchErr != nil {
				return false
			}
			if duplicateIdentity(mask, eligible) || !pt.satisfied(mask) {
				return true
			}
			selected := selectCredentials(mask, eligible)
			robust, certs, err := witnessRobust(p, selected)
			if err != nil {
				searchErr = err
				return false
			}
			if !robust {
				return true
			}
			candidate := makeWitness(mask, eligible, certs)
			if !haveBest || witnessBetter(candidate, best, count, count) {
				best = candidate
				haveBest = true
				foundAtCount = true
			}
			return true
		})
		if searchErr != nil {
			return Witness{}, searchErr
		}
		if haveBest && foundAtCount {
			return best, nil
		}
	}
	if !haveBest {
		return Witness{}, fmt.Errorf("%w: %w", ErrNoWitness, ErrWitnessNotRobust)
	}
	return best, nil
}

func forEachCombination(n, k int, fn func(uint64) bool) {
	if k < 0 || k > n {
		return
	}
	if k == 0 {
		fn(0)
		return
	}
	var walk func(start int, left int, mask uint64) bool
	walk = func(start int, left int, mask uint64) bool {
		if left == 0 {
			return fn(mask)
		}
		for i := start; i <= n-left; i++ {
			if !walk(i+1, left-1, mask|(uint64(1)<<uint(i))) {
				return false
			}
		}
		return true
	}
	walk(0, k, 0)
}

func duplicateIdentity(mask uint64, creds []Credential) bool {
	seen := make(map[string]bool)
	for i, c := range creds {
		if mask&(uint64(1)<<uint(i)) == 0 {
			continue
		}
		if seen[c.Identity] {
			return true
		}
		seen[c.Identity] = true
	}
	return false
}

func selectCredentials(mask uint64, creds []Credential) []Credential {
	out := make([]Credential, 0, bits.OnesCount64(mask))
	for i, c := range creds {
		if mask&(uint64(1)<<uint(i)) != 0 {
			out = append(out, c)
		}
	}
	return out
}

func makeWitness(mask uint64, creds []Credential, certs []InfeasibilityCertificate) Witness {
	var signers []string
	var ids []string
	for i, c := range creds {
		if mask&(uint64(1)<<uint(i)) == 0 {
			continue
		}
		signers = append(signers, c.Identity)
		ids = append(ids, c.ID)
	}
	signers = sortedUniqueStrings(signers)
	ids = sortedUniqueStrings(ids)
	return Witness{
		Signers:       signers,
		Credentials:   ids,
		CredentialIDs: append([]string(nil), ids...),
		Proof: Proof{
			Version:      1,
			Witness:      append([]string(nil), ids...),
			Certificates: certs,
		},
	}
}

func witnessBetter(a, b Witness, aCount, bCount int) bool {
	if aCount != bCount {
		return aCount < bCount
	}
	if cmp := compareStringSlices(a.Signers, b.Signers); cmp != 0 {
		return cmp < 0
	}
	return compareStringSlices(a.Credentials, b.Credentials) < 0
}

func compareStringSlices(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
