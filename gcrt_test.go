package gcrt

import (
	"math/big"
	"math/rand"
	"testing"
)

func gcd64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// mod64 is always non-negative for positive m.
func mod64(x, m int64) int64 {
	r := x % m
	if r < 0 {
		r += m
	}
	return r
}

// enumerate returns every x in [0, period) satisfying all congruences.
func enumerate(cons [][2]int64, period int64) []int64 {
	var out []int64
	for x := int64(0); x < period; x++ {
		ok := true
		for _, c := range cons {
			if mod64(x, c[1]) != mod64(c[0], c[1]) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, x)
		}
	}
	return out
}

// TestMergeEnumeration checks generalized congruence merging against
// finite enumeration over one full period, including negative residues.
func TestMergeEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 5000; trial++ {
		m1 := int64(rng.Intn(16) + 1)
		m2 := int64(rng.Intn(16) + 1)
		r1 := int64(rng.Intn(61) - 30)
		r2 := int64(rng.Intn(61) - 30)
		merged, ok := Merge(Normalize(big.NewInt(r1), big.NewInt(m1)),
			Normalize(big.NewInt(r2), big.NewInt(m2)))
		period := m1 / gcd64(m1, m2) * m2
		want := enumerate([][2]int64{{r1, m1}, {r2, m2}}, period)
		if len(want) == 0 {
			if ok {
				t.Fatalf("m=(%d,%d) r=(%d,%d): merge succeeded but enumeration is empty", m1, m2, r1, r2)
			}
			continue
		}
		if !ok {
			t.Fatalf("m=(%d,%d) r=(%d,%d): merge failed but enumeration gives %v", m1, m2, r1, r2, want)
		}
		if merged.Mod.Cmp(big.NewInt(period)) != 0 {
			t.Fatalf("m=(%d,%d) r=(%d,%d): period %s, want %d", m1, m2, r1, r2, merged.Mod, period)
		}
		if merged.Res.Cmp(big.NewInt(want[0])) != 0 {
			t.Fatalf("m=(%d,%d) r=(%d,%d): residue %s, want %d", m1, m2, r1, r2, merged.Res, want[0])
		}
		for _, x := range want[1:] {
			if mod64(x, period) != want[0] {
				t.Fatalf("enumeration broken: %v", want)
			}
		}
	}
}

// TestSolveEnumeration checks whole systems solved by the store against
// brute-force enumeration over the lcm of all moduli.
func TestSolveEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for trial := 0; trial < 2000; trial++ {
		n := rng.Intn(5) + 1
		var cons [][2]int64
		period := int64(1)
		for i := 0; i < n; i++ {
			m := int64(rng.Intn(9) + 1)
			r := int64(rng.Intn(21) - 10)
			cons = append(cons, [2]int64{r, m})
			period = period / gcd64(period, m) * m
		}
		st := NewStore()
		tx := st.Begin()
		for _, c := range cons {
			if _, err := tx.Add(big.NewInt(c[1]), big.NewInt(c[0])); err != nil {
				t.Fatal(err)
			}
		}
		v, err := tx.Commit()
		if err != nil {
			t.Fatal(err)
		}
		sol, err := st.Solve(v)
		if err != nil {
			t.Fatal(err)
		}
		want := enumerate(cons, period)
		if len(want) == 0 {
			if sol.OK {
				t.Fatalf("%v: solved to %s mod %s but enumeration is empty", cons, sol.Res, sol.Period)
			}
			pair := [][2]int64{cons[sol.Conflict[0]-1], cons[sol.Conflict[1]-1]}
			if got := enumerate(pair, period); len(got) != 0 {
				t.Fatalf("%v: reported conflict pair %v is actually compatible", cons, sol.Conflict)
			}
			continue
		}
		if !sol.OK {
			t.Fatalf("%v: inconsistent but enumeration gives %v", cons, want)
		}
		if sol.Period.Cmp(big.NewInt(period)) != 0 || sol.Res.Cmp(big.NewInt(want[0])) != 0 {
			t.Fatalf("%v: got x≡%s mod %s, want x≡%d mod %d", cons, sol.Res, sol.Period, want[0], period)
		}
	}
}

func TestEmptySystemAllIntegers(t *testing.T) {
	st := NewStore()
	sol, err := st.Solve(0)
	if err != nil {
		t.Fatal(err)
	}
	if !sol.OK || sol.Res.Sign() != 0 || sol.Period.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("empty system: got %+v, want x≡0 mod 1 (all integers)", sol)
	}
	// Removing everything must restore all integers.
	tx := st.Begin()
	id, _ := tx.Add(big.NewInt(7), big.NewInt(3))
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx = st.Begin()
	if err := tx.Remove(id); err != nil {
		t.Fatal(err)
	}
	v, _ := tx.Commit()
	sol, _ = st.Solve(v)
	if !sol.OK || sol.Res.Sign() != 0 || sol.Period.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("after removing all: got %+v, want all integers", sol)
	}
}

func TestNegativeResiduesAndBigIntegers(t *testing.T) {
	// Two large prime moduli and negative, larger-than-modulus residues.
	p, _ := new(big.Int).SetString("170141183460469231731687303715884105727", 10) // 2^127-1
	q, _ := new(big.Int).SetString("618970019642690137449562111", 10)             // 2^89-1
	r1raw, _ := new(big.Int).SetString("123456789012345678901234567890123456789012345678", 10)
	r1 := new(big.Int).Neg(r1raw)
	r2, _ := new(big.Int).SetString("99999999999999999999999999999999999999999", 10)

	st := NewStore()
	tx := st.Begin()
	if _, err := tx.Add(p, r1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Add(q, r2); err != nil {
		t.Fatal(err)
	}
	v, _ := tx.Commit()
	sol, err := st.Solve(v)
	if err != nil {
		t.Fatal(err)
	}
	if !sol.OK {
		t.Fatalf("coprime prime moduli must be consistent, got conflict %v", sol.Conflict)
	}
	if sol.Period.Cmp(new(big.Int).Mul(p, q)) != 0 {
		t.Fatalf("period = %s, want p*q", sol.Period)
	}
	if sol.Res.Sign() < 0 || sol.Res.Cmp(sol.Period) >= 0 {
		t.Fatalf("representative %s not in [0, period)", sol.Res)
	}
	if new(big.Int).Mod(new(big.Int).Sub(sol.Res, r1), p).Sign() != 0 {
		t.Fatalf("solution violates first congruence")
	}
	if new(big.Int).Mod(new(big.Int).Sub(sol.Res, r2), q).Sign() != 0 {
		t.Fatalf("solution violates second congruence")
	}
}

func TestEarliestConflictPair(t *testing.T) {
	st := NewStore()
	tx := st.Begin()
	tx.Add(big.NewInt(3), big.NewInt(2))          // x≡2 mod 3
	c2, _ := tx.Add(big.NewInt(4), big.NewInt(1)) // x≡1 mod 4
	c3, _ := tx.Add(big.NewInt(6), big.NewInt(2)) // x≡2 mod 6, conflicts with c2 only
	v, _ := tx.Commit()
	sol, _ := st.Solve(v)
	if sol.OK {
		t.Fatal("system must be inconsistent")
	}
	if sol.Conflict != [2]uint64{c2, c3} {
		t.Fatalf("conflict = %v, want [%d %d]", sol.Conflict, c2, c3)
	}
	// Removal of the conflicting constraint restores feasibility.
	tx = st.Begin()
	if err := tx.Remove(c3); err != nil {
		t.Fatal(err)
	}
	v, _ = tx.Commit()
	sol, _ = st.Solve(v)
	if !sol.OK || sol.Res.Cmp(big.NewInt(5)) != 0 || sol.Period.Cmp(big.NewInt(12)) != 0 {
		t.Fatalf("after removal: got %+v, want x≡5 mod 12", sol)
	}
}
