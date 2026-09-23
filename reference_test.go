package featureneg

import (
	"math/rand"
	"testing"
)

// bruteForce enumerates every subset of the optional candidates and
// returns the best feasible score. It is the small-domain exhaustive
// reference used to cross-check the solver.
func bruteForce(ctx *negCtx, required []string) (score, bool) {
	base := selection{}
	for _, r := range required {
		base[r] = true
	}
	n := len(ctx.optional)
	var best score
	found := false
	for mask := 0; mask < (1 << n); mask++ {
		sel := base.clone()
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				sel[ctx.optional[i]] = true
			}
		}
		closed, ok := ctx.close(sel)
		if !ok {
			continue
		}
		sc := ctx.scoreOf(closed)
		if !found || sc.better(best) {
			found = true
			best = sc
		}
	}
	return best, found
}

func randomOffer(rng *rand.Rand, party string, names []string) *Offer {
	o := &Offer{
		Party:    party,
		Protocol: Range{uint32(rng.Intn(3)), uint32(2 + rng.Intn(3))},
		Features: map[string]Feature{},
	}
	for _, name := range names {
		if rng.Intn(4) == 0 {
			continue // party does not support this feature at all
		}
		lo := uint32(rng.Intn(3))
		f := Feature{Name: name, Versions: Range{lo, lo + uint32(rng.Intn(3))}}
		for _, other := range names {
			if other == name {
				continue
			}
			switch rng.Intn(12) {
			case 0:
				f.Requires = append(f.Requires, Dep{Name: other, MinVersion: uint32(rng.Intn(3))})
			case 1:
				f.Conflicts = append(f.Conflicts, other)
			}
		}
		o.Features[name] = f
		switch rng.Intn(3) {
		case 0:
			o.Required = append(o.Required, name)
		case 1:
			o.Optional = append(o.Optional, name)
		}
	}
	// Random preference order over a shuffled subset of optionals.
	pref := append([]string(nil), o.Optional...)
	rng.Shuffle(len(pref), func(i, j int) { pref[i], pref[j] = pref[j], pref[i] })
	o.Preference = pref
	return o
}

// For small domains the solver (with a generous budget) must agree with
// exhaustive enumeration on both feasibility and the optimal score.
func TestSolverMatchesExhaustiveReference(t *testing.T) {
	names := []string{"f0", "f1", "f2", "f3", "f4", "f5"}
	for seed := int64(0); seed < 400; seed++ {
		rng := rand.New(rand.NewSource(seed))
		local := randomOffer(rng, "a", names)
		remote := randomOffer(rng, "b", names)
		sess := &Session{Local: local, Remote: remote}
		out := sess.Negotiate(1 << 24)

		ctx := buildCtx(local, remote)
		required := unionSorted(local.Required, remote.Required)
		refScore, refOK := bruteForce(ctx, required)

		// Required features unsupported by either side are handled
		// before the solver; mirror that here.
		requiredOK := true
		for _, r := range required {
			if !ctx.negotiable[r] {
				requiredOK = false
			}
		}
		protoOK := false
		if _, ok := local.Protocol.Intersect(remote.Protocol); ok {
			protoOK = true
		}

		switch {
		case !protoOK || !requiredOK:
			if out.Status != Conflict {
				t.Fatalf("seed %d: status = %v, want conflict", seed, out.Status)
			}
		case !refOK:
			if out.Status != Conflict {
				t.Fatalf("seed %d: status = %v, want conflict (reference infeasible)", seed, out.Status)
			}
			// The reported core must itself be infeasible, and minimal:
			// removing any element must make it feasible.
			if ctx.feasibleRequired(out.ConflictCore) {
				t.Fatalf("seed %d: core %v is feasible", seed, out.ConflictCore)
			}
			for i := range out.ConflictCore {
				trial := append(append([]string{}, out.ConflictCore[:i]...), out.ConflictCore[i+1:]...)
				if !ctx.feasibleRequired(trial) {
					t.Fatalf("seed %d: core %v not minimal at %d", seed, out.ConflictCore, i)
				}
			}
		default:
			if out.Status != OK {
				t.Fatalf("seed %d: status = %v, reasons %v", seed, out.Status, out.Reasons)
			}
			sel := selection{}
			for _, s := range out.Selected {
				sel[s.Name] = true
			}
			gotScore := ctx.scoreOf(sel)
			if gotScore != refScore {
				t.Fatalf("seed %d: solver score %+v != reference %+v", seed, gotScore, refScore)
			}
			// The returned selection must be feasible and closed.
			if _, ok := ctx.close(sel.clone()); !ok {
				t.Fatalf("seed %d: returned selection is infeasible", seed)
			}
		}
	}
}
