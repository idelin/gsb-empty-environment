package netsec

import (
	"crypto/elliptic"
	"errors"
	"math/big"
	"testing"
)

func TestVerifyShareValid(t *testing.T) {
	m := genDealer("d1", 3)
	x := big.NewInt(7)
	s := m.shareAt(x)
	if err := VerifyShare(m.commit, x, s); err != nil {
		t.Fatalf("valid share rejected: %v", err)
	}
}

func TestVerifyShareTampered(t *testing.T) {
	m := genDealer("d1", 3)
	x := big.NewInt(7)
	s := new(big.Int).Add(m.shareAt(x), big.NewInt(1))
	s.Mod(s, Curve.Params().N)
	err := VerifyShare(m.commit, x, s)
	if !errors.Is(err, ErrShareMismatch) {
		t.Fatalf("want ErrShareMismatch, got %v", err)
	}
}

func TestVerifyShareRejectsBadInputs(t *testing.T) {
	m := genDealer("d1", 2)
	s := big.NewInt(11)
	cases := []struct {
		name string
		c    Commitment
		x    *big.Int
		s    *big.Int
		want error
	}{
		{"zero x", m.commit, big.NewInt(0), s, ErrZeroIndex},
		{"x equals N", m.commit, new(big.Int).Set(Curve.Params().N), s, ErrScalarOutOfRange},
		{"zero share", m.commit, big.NewInt(3), big.NewInt(0), ErrScalarOutOfRange},
		{"share equals N", m.commit, big.NewInt(3), new(big.Int).Set(Curve.Params().N), ErrScalarOutOfRange},
		{"nil share", m.commit, big.NewInt(3), nil, ErrScalarOutOfRange},
		{"empty commitment", Commitment{}, big.NewInt(3), s, ErrEmptyCommitment},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifyShare(tc.c, tc.x, tc.s); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestCommitmentValidate(t *testing.T) {
	g := generatorPoint()
	t.Run("identity constant", func(t *testing.T) {
		c := Commitment{Points: []Point{infinityPoint(), g}}
		if !errors.Is(c.Validate(), ErrInvalidCommitmentPoint) {
			t.Fatal("identity point not rejected")
		}
	})
	t.Run("off curve", func(t *testing.T) {
		c := Commitment{Points: []Point{{X: big.NewInt(1), Y: big.NewInt(2)}}}
		if !errors.Is(c.Validate(), ErrInvalidCommitmentPoint) {
			t.Fatal("off-curve point not rejected")
		}
	})
	t.Run("duplicate points", func(t *testing.T) {
		c := Commitment{Points: []Point{g, g}}
		if !errors.Is(c.Validate(), ErrDuplicateCommitmentPoint) {
			t.Fatal("duplicate point not rejected")
		}
	})
	t.Run("valid", func(t *testing.T) {
		m := genDealer("d", 2)
		if err := m.commit.Validate(); err != nil {
			t.Fatalf("valid commitment rejected: %v", err)
		}
	})
}

func TestCommitmentHashDeterministicAndBinding(t *testing.T) {
	m := genDealer("d", 3)
	h1 := m.commit.Hash()
	h2 := m.commit.Hash()
	if h1 != h2 {
		t.Fatal("hash not deterministic")
	}
	other := genDealer("d2", 3)
	if other.commit.Hash() == h1 {
		t.Fatal("distinct commitments hashed equal")
	}
	tampered := cloneCommit(m.commit)
	tampered.Points[1] = other.commit.Points[0]
	if m.commit.Hash() == tampered.Hash() {
		t.Fatal("tampered commitment hashed equal")
	}
	// Compressed encoding length sanity.
	if raw := elliptic.MarshalCompressed(Curve, m.commit.Points[0].X, m.commit.Points[0].Y); len(raw) != 33 {
		t.Fatalf("unexpected compressed length %d", len(raw))
	}
}

func TestEvalCommitmentMatchesDirectProduct(t *testing.T) {
	m := genDealer("d", 4)
	x := big.NewInt(13)
	n := Curve.Params().N

	var directX, directY *big.Int
	power := big.NewInt(1)
	for _, p := range m.commit.Points {
		px, py := Curve.ScalarMult(p.X, p.Y, power.Bytes())
		if directX == nil {
			directX, directY = px, py
		} else {
			directX, directY = Curve.Add(directX, directY, px, py)
		}
		power = new(big.Int).Mul(power, x)
		power.Mod(power, n)
	}
	got := evalCommitment(m.commit, x)
	if got.X.Cmp(directX) != 0 || got.Y.Cmp(directY) != 0 {
		t.Fatal("Horner evaluation disagrees with direct product")
	}
}
