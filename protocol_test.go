package netsec

import (
	"crypto/rand"
	"errors"
	"fmt"
	"testing"
)

func TestHonestMultiplication(t *testing.T) {
	for _, p := range []uint32{2, 3, 5, 17, 65521} {
		p := p
		t.Run(fmt.Sprintf("p%d", p), func(t *testing.T) {
			f := mustField(t, p)
			provider, err := NewHonestProvider(p, rand.Reader)
			if err != nil {
				t.Fatalf("NewHonestProvider: %v", err)
			}
			candidate := mustHonestTriple(t, provider, "candidate")
			check := mustHonestTriple(t, provider, "check")

			h, err := NewHost(p)
			if err != nil {
				t.Fatalf("NewHost: %v", err)
			}
			x, err := f.Random(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			y, err := f.Random(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			xShares := mustShares(t, f, x)
			yShares := mustShares(t, f, y)

			checkRes, err := h.Sacrifice(candidate, check)
			if err != nil {
				t.Fatalf("Sacrifice: %v", err)
			}
			if !checkRes.Accepted || checkRes.Tau != 0 {
				t.Fatalf("honest sacrifice rejected: %+v", checkRes)
			}
			if checkRes.R == 0 || !f.Valid(checkRes.R) {
				t.Fatalf("invalid public r: %d", checkRes.R)
			}
			if !f.Valid(checkRes.Rho) || !f.Valid(checkRes.Sigma) {
				t.Fatalf("openings out of range: rho=%d sigma=%d", checkRes.Rho, checkRes.Sigma)
			}

			mul, err := h.Multiply(candidate, xShares, yShares)
			if err != nil {
				t.Fatalf("Multiply: %v", err)
			}
			if !mul.Produced {
				t.Fatal("multiplication did not produce output")
			}
			z, err := mul.Secret(f)
			if err != nil {
				t.Fatalf("Secret: %v", err)
			}
			want := f.Mul(x, y)
			if z != want {
				t.Fatalf("z=%d, want x*y=%d", z, want)
			}
			if err := f.ValidateShares(mul.Z); err != nil {
				t.Fatalf("output shares: %v", err)
			}

			a, _ := f.Recombine(candidate.A)
			b, _ := f.Recombine(candidate.B)
			if mul.D != f.Sub(x, a) || mul.E != f.Sub(y, b) {
				t.Fatalf("openings d=%d e=%d, want %d %d", mul.D, mul.E, f.Sub(x, a), f.Sub(y, b))
			}

			if status, err := h.TripleStatus(candidate.ID); err != nil || status != StatusConsumed {
				t.Fatalf("candidate status=%q err=%v, want consumed", status, err)
			}
			if status, err := h.TripleStatus(check.ID); err != nil || status != StatusBurned {
				t.Fatalf("check status=%q err=%v, want burned", status, err)
			}
		})
	}
}

func TestSacrificeHostMatchesPublicFormula(t *testing.T) {
	f := mustField(t, 17)
	scalars := []uint32{3, 7, 11, 2, 9, 4}
	candidate := mustTriple(t, f, "T", scalars[0], scalars[1], scalars[2])
	check := mustTriple(t, f, "Tprime", scalars[3], scalars[4], scalars[5])
	h, err := NewHost(17)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Sacrifice(candidate, check)
	if err != nil {
		t.Fatalf("Sacrifice: %v", err)
	}
	rho := f.Sub(scalars[0], f.Mul(res.R, scalars[3]))
	sigma := f.Sub(scalars[1], scalars[4])
	tau, err := SacrificeTau(f, scalars[0], scalars[1], scalars[2], scalars[3], scalars[4], scalars[5], res.R)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rho != rho || res.Sigma != sigma || res.Tau != tau {
		t.Fatalf("host openings (%d,%d,%d), want (%d,%d,%d)", res.Rho, res.Sigma, res.Tau, rho, sigma, tau)
	}
	want := res.Rho == rho && res.Sigma == sigma && tau == 0
	if res.Accepted != want {
		t.Fatalf("accepted=%v, want %v", res.Accepted, want)
	}
}

func TestDeriveSacrificeRandomNonZero(t *testing.T) {
	for _, p := range []uint32{2, 3, 5, 17, 65521} {
		f := mustField(t, p)
		seeds := [3][]byte{make([]byte, 32), make([]byte, 32), make([]byte, 32)}
		for i := range seeds {
			if _, err := rand.Read(seeds[i]); err != nil {
				t.Fatal(err)
			}
		}
		for round := RoundID(1); round <= 20; round++ {
			r, err := DeriveSacrificeRandom(f, round, TriplePair{First: "A", Second: "B"}, seeds)
			if err != nil {
				t.Fatalf("DeriveSacrificeRandom: %v", err)
			}
			if r == 0 || !f.Valid(r) {
				t.Fatalf("p=%d round=%d derived r=%d", p, round, r)
			}
		}
	}
}

func TestAdditiveShareRoundTrip(t *testing.T) {
	for _, p := range []uint32{2, 17, 65521} {
		f := mustField(t, p)
		for x := uint32(0); x < f.P; x++ {
			shares := mustShares(t, f, x)
			got, err := f.Recombine(shares)
			if err != nil || got != x {
				t.Fatalf("p=%d x=%d got=%d err=%v", p, x, got, err)
			}
		}
	}
}

func TestRejectedSacrificeBurnsBothIDs(t *testing.T) {
	f := mustField(t, 17)
	provider, err := NewHonestProvider(17, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	candidate := mustHonestTriple(t, provider, "candidate")
	// The check triple is honest; make the candidate's aggregate c wrong by 5.
	candidate.C[0] = f.Add(candidate.C[0], 5)
	check := mustHonestTriple(t, provider, "check")

	h, err := NewHost(17)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Sacrifice(candidate, check)
	if err != nil {
		t.Fatalf("Sacrifice: %v", err)
	}
	if res.Accepted || res.Tau == 0 {
		t.Fatalf("corrupt candidate accepted: %+v", res)
	}
	for _, id := range []string{candidate.ID, check.ID} {
		if status, _ := h.TripleStatus(id); status != StatusBurned {
			t.Fatalf("%s status=%q, want burned", id, status)
		}
	}
	x, y := mustShares(t, f, 4), mustShares(t, f, 6)
	if _, err := h.Multiply(candidate, x, y); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("Multiply after rejection: %v", err)
	}
}
