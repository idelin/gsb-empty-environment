package netsec

import "testing"

var smallPrimes = []uint32{2, 3, 5, 7, 11, 13, 17}

// TestSacrificeExhaustiveHonestCheck enumerates every (a, b, c, a', b', r)
// with r != 0 for every prime p <= 17, using an honest check triple
// c' = a'*b'. In this case acceptance must be exactly c == a*b.
func TestSacrificeExhaustiveHonestCheck(t *testing.T) {
	for _, p := range smallPrimes {
		f := mustField(t, p)
		for a := uint32(0); a < p; a++ {
			for b := uint32(0); b < p; b++ {
				ab := f.Mul(a, b)
				for c := uint32(0); c < p; c++ {
					for aCheck := uint32(0); aCheck < p; aCheck++ {
						for bCheck := uint32(0); bCheck < p; bCheck++ {
							cCheck := f.Mul(aCheck, bCheck)
							for r := uint32(1); r < p; r++ {
								accepted, err := SacrificeAccepted(f, a, b, c, aCheck, bCheck, cCheck, r)
								if err != nil {
									t.Fatalf("p=%d accepted(%d,%d,%d,%d,%d,%d,%d): %v", p, a, b, c, aCheck, bCheck, cCheck, r, err)
								}
								if accepted != (c == ab) {
									t.Fatalf("p=%d a=%d b=%d c=%d a'=%d b'=%d r=%d: accepted=%v, want %v",
										p, a, b, c, aCheck, bCheck, r, accepted, c == ab)
								}
							}
						}
					}
				}
			}
		}
	}
}

// TestSacrificeExhaustiveGeneral enumerates the complete seven-tuple
// (a,b,c,a',b',c',r), including a corrupted check triple, for p <= 7. The
// acceptance predicate must be
//
//	(c - a*b) == r*(c' - a'*b')  (mod p).
func TestSacrificeExhaustiveGeneral(t *testing.T) {
	for _, p := range []uint32{2, 3, 5, 7} {
		f := mustField(t, p)
		for a := uint32(0); a < p; a++ {
			for b := uint32(0); b < p; b++ {
				for c := uint32(0); c < p; c++ {
					for aCheck := uint32(0); aCheck < p; aCheck++ {
						for bCheck := uint32(0); bCheck < p; bCheck++ {
							for cCheck := uint32(0); cCheck < p; cCheck++ {
								for r := uint32(1); r < p; r++ {
									accepted, err := SacrificeAccepted(f, a, b, c, aCheck, bCheck, cCheck, r)
									if err != nil {
										t.Fatalf("p=%d: %v", p, err)
									}
									lhs := f.Sub(c, f.Mul(a, b))
									rhs := f.Mul(r, f.Sub(cCheck, f.Mul(aCheck, bCheck)))
									want := lhs == rhs
									if accepted != want {
										t.Fatalf("p=%d a=%d b=%d c=%d a'=%d b'=%d c'=%d r=%d: accepted=%v want=%v tau=%d lhs=%d rhs=%d",
											p, a, b, c, aCheck, bCheck, cCheck, r, accepted, want,
											f.Sub(lhs, rhs), lhs, rhs)
									}
								}
							}
						}
					}
				}
			}
		}
	}
}

// TestSingleSacrificeIsProbabilistic pins the important caveat: one nonzero
// random r can accept two mutually consistent corrupted triples. With deltas
// both equal to 1, acceptance happens exactly at r == 1. This is why the
// protocol report must not claim a single sacrifice check is absolute.
func TestSingleSacrificeIsProbabilistic(t *testing.T) {
	f := mustField(t, 17)
	const a, b, aCheck, bCheck = uint32(3), uint32(4), uint32(5), uint32(6)
	c := f.Add(f.Mul(a, b), 1)
	cCheck := f.Add(f.Mul(aCheck, bCheck), 1)

	accepted, err := SacrificeAccepted(f, a, b, c, aCheck, bCheck, cCheck, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !accepted {
		t.Fatal("expected the correlated-error pair to pass at r=1")
	}
	for r := uint32(2); r < 17; r++ {
		accepted, err := SacrificeAccepted(f, a, b, c, aCheck, bCheck, cCheck, r)
		if err != nil {
			t.Fatal(err)
		}
		if accepted {
			t.Fatalf("unexpected acceptance at r=%d", r)
		}
	}
}
