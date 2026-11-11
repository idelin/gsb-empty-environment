package shareplan

import (
	"errors"
	"sort"
)

// Reconstruction describes how to rebuild the secret from one collection of
// participant shares. The root secret equals the sum over WitnessLeaves of
// LeafCoeff[share] times the share value. Coeff aggregates those
// coefficients per participant, which matters when one participant holds
// several independently identified leaf shares.
type Reconstruction struct {
	// Participants is the qualified participant set actually used, sorted.
	Participants []string
	// Coeff maps participant to aggregated reconstruction coefficient.
	Coeff map[string]int
	// WitnessLeaves is the exact set of independently identified leaf
	// shares used by this reconstruction.
	WitnessLeaves []*LeafShare
	// LeafCoeff maps each witness ShareID to its reconstruction coefficient.
	LeafCoeff map[string]int
}

// Secret evaluates the linear reconstruction against the supplied leaf
// shares, which must include every witness share.
func (r *Reconstruction) Secret(leaves []*LeafShare) (int, error) {
	values := make(map[string]int, len(leaves))
	for _, leaf := range leaves {
		if leaf == nil {
			return 0, ErrUnknownShare
		}
		if leaf.Y < 0 || leaf.Y >= P {
			return 0, ErrInvalidValue
		}
		values[leaf.ShareID] = leaf.Y
	}
	secret := 0
	for shareID, coeff := range r.LeafCoeff {
		y, ok := values[shareID]
		if !ok {
			return 0, ErrNotEnoughShares
		}
		secret = add(secret, mul(coeff, y))
	}
	return secret, nil
}

// Qualified reports whether the named participants satisfy the policy.
// Unknown participant names produce a false result. With at most
// MaxParticipants participants, evaluation is a linear tree walk.
func (p *Plan) Qualified(participants []string) bool {
	have := make(map[string]bool, len(participants))
	known := make(map[string]struct{}, len(p.idx.participants))
	for _, name := range p.idx.participants {
		known[name] = struct{}{}
	}
	for _, name := range participants {
		if _, ok := known[name]; !ok {
			return false
		}
		have[name] = true
	}

	var satisfied func(nodeID string) bool
	satisfied = func(nodeID string) bool {
		meta := p.idx.nodes[nodeID]
		if meta.leaf != nil {
			return have[meta.leaf.participant]
		}
		count := 0
		for _, childID := range meta.childIDs {
			if satisfied(childID) {
				count++
				if count >= meta.threshold {
					return true
				}
			}
		}
		return false
	}
	return satisfied("root")
}

// MinimalParticipantSets returns every inclusion-minimal subset of
// participants that satisfies the policy, each subset sorted. Enumerating
// all subsets is feasible because the participant count is capped at
// MaxParticipants.
func (p *Plan) MinimalParticipantSets() [][]string {
	all := p.idx.participants
	n := len(all)
	var minimal [][]string
	for mask := 1; mask < 1<<n; mask++ {
		set := maskParticipants(all, mask)
		if !p.Qualified(set) {
			continue
		}
		isMinimal := true
		for bit := 0; bit < n; bit++ {
			if mask&(1<<bit) != 0 && p.Qualified(maskParticipants(all, mask^(1<<bit))) {
				isMinimal = false
				break
			}
		}
		if isMinimal {
			minimal = append(minimal, set)
		}
	}
	sort.Slice(minimal, func(i, j int) bool { return lessSet(minimal[i], minimal[j]) })
	return minimal
}

func maskParticipants(all []string, mask int) []string {
	var out []string
	for bit, name := range all {
		if mask&(1<<bit) != 0 {
			out = append(out, name)
		}
	}
	return out
}

func lessSet(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

type leafTerm struct {
	share *LeafShare
	coeff int
}

type witness struct {
	coeffs map[string]int // participant -> aggregated coefficient
	terms  []leafTerm     // leaf share -> coefficient (may repeat a share ID)
}

// ReconstructionFor returns recovery coefficients for a qualified
// participant set by selecting a concrete threshold witness at every node.
// It returns ErrNotEnoughShares when the set does not satisfy the policy.
func (p *Plan) ReconstructionFor(participants []string) (*Reconstruction, error) {
	if !p.Qualified(participants) {
		return nil, ErrNotEnoughShares
	}

	have := make(map[string]bool, len(participants))
	for _, name := range participants {
		have[name] = true
	}
	leafIDs := p.idx.leafShareIDs()

	var build func(nodeID string) (witness, bool)
	build = func(nodeID string) (witness, bool) {
		meta := p.idx.nodes[nodeID]
		if meta.leaf != nil {
			if !have[meta.leaf.participant] {
				return witness{}, false
			}
			share := p.leafByID[leafIDs[nodeID]]
			return witness{
				coeffs: map[string]int{meta.leaf.participant: 1},
				terms:  []leafTerm{{share: share, coeff: 1}},
			}, true
		}

		type chosenChild struct {
			nodeID string
			child  witness
		}
		var chosen []chosenChild
		for _, childID := range meta.childIDs {
			child, ok := build(childID)
			if !ok {
				continue
			}
			chosen = append(chosen, chosenChild{nodeID: childID, child: child})
			if len(chosen) == meta.threshold {
				break
			}
		}
		if len(chosen) < meta.threshold {
			return witness{}, false
		}

		points := make([]point, len(chosen))
		for i, entry := range chosen {
			points[i] = point{x: childX(p, nodeID, entry.nodeID)}
		}
		_, lagrange := interpolateAtZero(points)

		aggregated := map[string]int{}
		var terms []leafTerm
		for i, entry := range chosen {
			scaling := lagrange[i]
			for participant, coeff := range entry.child.coeffs {
				aggregated[participant] = add(aggregated[participant], mul(scaling, coeff))
			}
			for _, term := range entry.child.terms {
				terms = append(terms, leafTerm{share: term.share, coeff: mul(scaling, term.coeff)})
			}
		}
		return witness{coeffs: aggregated, terms: terms}, true
	}

	w, ok := build("root")
	if !ok {
		return nil, ErrNotEnoughShares
	}

	leafCoeff := map[string]int{}
	shareByID := map[string]*LeafShare{}
	for _, term := range w.terms {
		leafCoeff[term.share.ShareID] = add(leafCoeff[term.share.ShareID], term.coeff)
		shareByID[term.share.ShareID] = term.share
	}

	names := make([]string, 0, len(w.coeffs))
	for name := range w.coeffs {
		names = append(names, name)
	}
	sort.Strings(names)

	recon := &Reconstruction{
		Participants: names,
		Coeff:        w.coeffs,
		LeafCoeff:    leafCoeff,
	}
	shareIDs := make([]string, 0, len(leafCoeff))
	for shareID := range leafCoeff {
		shareIDs = append(shareIDs, shareID)
	}
	sort.Strings(shareIDs)
	for _, shareID := range shareIDs {
		recon.WitnessLeaves = append(recon.WitnessLeaves, shareByID[shareID])
	}
	return recon, nil
}

// VerifyRecovery exhaustively checks the plan against every participant
// subset (at most 2^MaxParticipants). Recover succeeds exactly for qualified
// subsets; every qualified subset also reconstructs secret via the linear
// coefficients returned by ReconstructionFor. It finally checks that direct
// node-level Combine reconstructs every internal node secret using only the
// shares produced by that same node.
func (p *Plan) VerifyRecovery(secret int) error {
	if secret < 0 || secret >= P {
		return ErrInvalidSecret
	}

	all := p.idx.participants
	n := len(all)
	leafIDs := p.idx.leafShareIDs()

	for mask := 0; mask < 1<<n; mask++ {
		set := maskParticipants(all, mask)
		qualified := p.Qualified(set)

		have := make(map[string]bool, len(set))
		for _, name := range set {
			have[name] = true
		}
		var presented []*LeafShare
		for _, nodeID := range p.idx.leaves {
			meta := p.idx.nodes[nodeID]
			if have[meta.leaf.participant] {
				presented = append(presented, p.leafByID[leafIDs[nodeID]])
			}
		}

		got, err := p.Recover(presented)
		switch {
		case qualified && (err != nil || got != secret):
			return errors.New("shareplan: qualified subset failed recovery")
		case !qualified && err == nil:
			return errors.New("shareplan: unqualified subset recovered the secret")
		}

		if qualified {
			recon, err := p.ReconstructionFor(set)
			if err != nil {
				return err
			}
			linear, err := recon.Secret(presented)
			if err != nil || linear != secret {
				return errors.New("shareplan: reconstruction coefficients do not recover the secret")
			}
			if !coeffMatchesLeaves(recon) {
				return errors.New("shareplan: aggregated coefficients disagree with leaf coefficients")
			}
		}
	}

	if err := p.verifyNodeSecrets(secret); err != nil {
		return err
	}
	return p.verifyMinimalSets()
}

func coeffMatchesLeaves(recon *Reconstruction) bool {
	summed := map[string]int{}
	for _, leaf := range recon.WitnessLeaves {
		summed[leaf.Participant] = add(summed[leaf.Participant], recon.LeafCoeff[leaf.ShareID])
	}
	if len(summed) != len(recon.Coeff) {
		return false
	}
	for participant, coeff := range recon.Coeff {
		if summed[participant] != coeff {
			return false
		}
	}
	return true
}

// verifyNodeSecrets reconstructs every internal node directly from its own
// child shares, asserting same-node combining recovers each node secret.
func (p *Plan) verifyNodeSecrets(rootSecret int) error {
	var walk func(nodeID string, expected int) error
	walk = func(nodeID string, expected int) error {
		meta := p.idx.nodes[nodeID]
		if meta.leaf != nil {
			return nil
		}
		shares := p.InternalShares(nodeID)
		got, _, err := p.Combine(nodeID, shares[:meta.threshold])
		if err != nil || got != expected {
			return errors.New("shareplan: node-level combine failed")
		}
		for _, childID := range meta.childIDs {
			if err := walk(childID, p.nodeSecrets[childID]); err != nil {
				return err
			}
		}
		return nil
	}
	return walk("root", rootSecret)
}

func (p *Plan) verifyMinimalSets() error {
	all := p.idx.participants
	var brute [][]string
	for mask := 1; mask < 1<<len(all); mask++ {
		set := maskParticipants(all, mask)
		if !p.Qualified(set) {
			continue
		}
		isMinimal := true
		for bit := range all {
			if mask&(1<<bit) != 0 && p.Qualified(maskParticipants(all, mask^(1<<bit))) {
				isMinimal = false
				break
			}
		}
		if isMinimal {
			brute = append(brute, set)
		}
	}

	sort.Slice(brute, func(i, j int) bool { return lessSet(brute[i], brute[j]) })
	got := p.MinimalParticipantSets()
	if len(brute) != len(got) {
		return errors.New("shareplan: minimal participant set mismatch")
	}
	for i := range brute {
		if lessSet(brute[i], got[i]) || lessSet(got[i], brute[i]) {
			return errors.New("shareplan: minimal participant set mismatch")
		}
	}
	return nil
}
