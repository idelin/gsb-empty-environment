package mtusim

import "testing"

// For small lengths, the optimizer's joint (header, align) choice must match
// pure enumeration of every legal layout, and the resulting fragmentation
// must pass the independent coverage verifier.
func TestOptimizeMatchesExhaustiveEnumeration(t *testing.T) {
	for total := 0; total <= 48; total++ {
		for _, mtu := range []int{12, 16, 20, 24} {
			const hMin, hMax, aMax = 4, 10, 8
			plan, err := Optimize(total, mtu, hMin, hMax, aMax)
			want, ok := ExhaustiveOptimalCount(total, mtu, hMin, hMax, aMax)
			if !ok {
				if err == nil {
					t.Errorf("len=%d mtu=%d: Optimize found %d, enumeration says infeasible",
						total, mtu, plan.Count)
				}
				continue
			}
			if err != nil {
				t.Errorf("len=%d mtu=%d: Optimize failed: %v (enumeration: %d)", total, mtu, err, want)
				continue
			}
			if plan.Count != want {
				t.Errorf("len=%d mtu=%d: Optimize=%d, exhaustive=%d", total, mtu, plan.Count, want)
			}
			frags, ferr := plan.Params.Fragmentize(1, 1, total)
			if ferr != nil {
				t.Errorf("len=%d mtu=%d: Fragmentize: %v", total, mtu, ferr)
				continue
			}
			if len(frags) != plan.Count {
				t.Errorf("len=%d mtu=%d: Fragmentize made %d, plan says %d",
					total, mtu, len(frags), plan.Count)
			}
			if verr := VerifyCoverage(plan.Params, total, frags); verr != nil {
				t.Errorf("len=%d mtu=%d: coverage: %v", total, mtu, verr)
			}
		}
	}
}

// FragmentCount itself must equal exhaustive enumeration per parameter set.
func TestFragmentCountMatchesExhaustive(t *testing.T) {
	for total := 0; total <= 40; total++ {
		for _, p := range []FragParams{
			{4, 12, 1}, {4, 12, 2}, {4, 12, 4}, {6, 16, 3},
			{8, 20, 8}, {10, 24, 5}, {4, 16, 16},
		} {
			got, err := p.FragmentCount(total)
			want, ok := ExhaustiveMinFragments(p, total)
			if !ok {
				if err == nil {
					t.Errorf("len=%d %+v: count=%d but enumeration infeasible", total, p, got)
				}
				continue
			}
			if err != nil {
				t.Errorf("len=%d %+v: %v but enumeration gives %d", total, p, err, want)
			} else if got != want {
				t.Errorf("len=%d %+v: count=%d, exhaustive=%d", total, p, got, want)
			}
		}
	}
}

func TestOptimizeInfeasible(t *testing.T) {
	// MTU below every candidate header: nothing can be planned.
	if _, err := Optimize(100, 6, 8, 20, 8); KindOf(err) != ErrKindInfeasible {
		t.Errorf("got %v, want ErrKindInfeasible", err)
	}
	// MTU equals the header: zero payload budget, datagram exceeds one frame.
	if _, err := Optimize(100, 20, 20, 20, 8); KindOf(err) != ErrKindInfeasible {
		t.Errorf("got %v, want ErrKindInfeasible", err)
	}
}

func TestOptimizePrefersFewerFragments(t *testing.T) {
	plan, err := Optimize(300, 128, 8, 40, 16)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if plan.Count != 3 {
		t.Errorf("got %d fragments, want 3", plan.Count)
	}
	if plan.Params.HeaderLen != 8 {
		t.Errorf("got header %d, want minimal 8", plan.Params.HeaderLen)
	}
}
