package binom

import (
	"math/big"
	"testing"
)

// pascal builds the exact Pascal triangle rows 0..n with big integers.
func pascal(n int64) [][]*big.Int {
	rows := make([][]*big.Int, n+1)
	for i := int64(0); i <= n; i++ {
		row := make([]*big.Int, i+1)
		row[0] = big.NewInt(1)
		row[i] = big.NewInt(1)
		for j := int64(1); j < i; j++ {
			row[j] = new(big.Int).Add(rows[i-1][j-1], rows[i-1][j])
		}
		rows[i] = row
	}
	return rows
}

func TestExactAgainstPascalTriangle(t *testing.T) {
	rows := pascal(60)
	for n := int64(0); n <= 60; n++ {
		for k := int64(0); k <= n; k++ {
			got, err := Exact(n, k)
			if err != nil {
				t.Fatalf("Exact(%d, %d): %v", n, k, err)
			}
			if got.Cmp(rows[n][k]) != 0 {
				t.Fatalf("Exact(%d, %d) = %v, want %v", n, k, got, rows[n][k])
			}
		}
	}
}

func TestExactSymmetryAndKnownValues(t *testing.T) {
	// C(100, 50) is a well-known constant.
	want, _ := new(big.Int).SetString("100891344545564193334812497256", 10)
	got, err := Exact(100, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(want) != 0 {
		t.Fatalf("Exact(100, 50) = %v, want %v", got, want)
	}
	for _, n := range []int64{1, 2, 17, 63, 200} {
		for _, k := range []int64{0, 1, n / 3, n / 2, n - 1, n} {
			a, _ := Exact(n, k)
			b, _ := Exact(n, n-k)
			if a.Cmp(b) != 0 {
				t.Fatalf("asymmetry: C(%d,%d)=%v C(%d,%d)=%v", n, k, a, n, n-k, b)
			}
		}
	}
}

func TestKOutOfRange(t *testing.T) {
	for _, k := range []int64{-3, -1, 6, 100} {
		got, err := Exact(5, k)
		if err != nil {
			t.Fatalf("Exact(5, %d): %v", k, err)
		}
		if got.Sign() != 0 {
			t.Fatalf("Exact(5, %d) = %v, want 0", k, got)
		}
	}
	// k out of range also yields 0 modulo any m, including m = 1.
	for _, ms := range []string{"1", "7", "360"} {
		m, _ := new(big.Int).SetString(ms, 10)
		for _, k := range []int64{-1, 6} {
			got, err := Mod(5, k, m)
			if err != nil {
				t.Fatalf("Mod(5, %d, %s): %v", k, ms, err)
			}
			if got.Sign() != 0 {
				t.Fatalf("Mod(5, %d, %s) = %v, want 0", k, ms, got)
			}
		}
	}
}

func TestNZero(t *testing.T) {
	got, err := Exact(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("Exact(0, 0) = %v, want 1", got)
	}
	for _, ms := range []string{"1", "2", "999999999989"} {
		m, _ := new(big.Int).SetString(ms, 10)
		got, err := Mod(0, 0, m)
		if err != nil {
			t.Fatalf("Mod(0, 0, %s): %v", ms, err)
		}
		want := new(big.Int).Mod(big.NewInt(1), m)
		if got.Cmp(want) != 0 {
			t.Fatalf("Mod(0, 0, %s) = %v, want %v", ms, got, want)
		}
	}
	if _, err := Exact(-1, 0); err != ErrNegativeN {
		t.Fatalf("Exact(-1, 0) err = %v, want ErrNegativeN", err)
	}
	if _, err := Mod(-1, 0, big.NewInt(7)); err != ErrNegativeN {
		t.Fatalf("Mod(-1, 0, 7) err = %v, want ErrNegativeN", err)
	}
}

func TestModulusOne(t *testing.T) {
	one := big.NewInt(1)
	for _, nk := range [][2]int64{{0, 0}, {1, 1}, {10, 3}, {1000, 500}} {
		got, err := Mod(nk[0], nk[1], one)
		if err != nil {
			t.Fatalf("Mod(%d, %d, 1): %v", nk[0], nk[1], err)
		}
		if got.Sign() != 0 {
			t.Fatalf("Mod(%d, %d, 1) = %v, want 0", nk[0], nk[1], got)
		}
	}
}

func TestInvalidModulus(t *testing.T) {
	for _, ms := range []string{"0", "-7"} {
		m, _ := new(big.Int).SetString(ms, 10)
		if _, err := Mod(5, 2, m); err != ErrNonPositiveModulus {
			t.Fatalf("Mod(5, 2, %s) err = %v, want ErrNonPositiveModulus", ms, err)
		}
	}
}
