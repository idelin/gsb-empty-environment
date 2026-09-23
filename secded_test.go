package secded

import (
	"math/rand"
	"testing"
)

// combinations yields all size-m subsets of [0,n).
func combinations(n, m int) [][]int {
	var out [][]int
	var cur []int
	var rec func(start int)
	rec = func(start int) {
		if len(cur) == m {
			cp := make([]int, m)
			copy(cp, cur)
			out = append(out, cp)
			return
		}
		for i := start; i < n; i++ {
			cur = append(cur, i)
			rec(i + 1)
			cur = cur[:len(cur)-1]
		}
	}
	rec(0)
	return out
}

func TestParamsTable(t *testing.T) {
	cases := []struct{ k, r, n int }{
		{4, 3, 8}, {8, 4, 13}, {11, 4, 16}, {26, 5, 32}, {57, 6, 64},
	}
	for _, c := range cases {
		p, err := NewParams(c.k)
		if err != nil {
			t.Fatalf("k=%d: %v", c.k, err)
		}
		if p.ParityBits != c.r || p.TotalBits != c.n {
			t.Errorf("k=%d: got r=%d n=%d, want r=%d n=%d", c.k, p.ParityBits, p.TotalBits, c.r, c.n)
		}
	}
	if _, err := NewParams(0); err == nil {
		t.Error("k=0 should fail")
	}
	if _, err := NewParams(MaxDataBits + 1); err == nil {
		t.Error("k>MaxDataBits should fail")
	}
}

// TestExhaustiveSingleBitFlip: every data word x every single-bit flip
// (data bits, Hamming parity bits AND the overall parity bit) must be
// corrected back to the exact original data.
func TestExhaustiveSingleBitFlip(t *testing.T) {
	for _, k := range []int{4, 11} {
		p, _ := NewParams(k)
		for data := uint64(0); data < (1 << uint(k)); data++ {
			cw := p.Encode(data)
			if _, st, fixed := p.Decode(cw); st != StatusClean || fixed != cw {
				t.Fatalf("k=%d data=%d: clean word not clean", k, data)
			}
			for bit := 0; bit < p.TotalBits; bit++ {
				got, st, fixed := p.Decode(cw ^ (1 << uint(bit)))
				if st != StatusCorrectedSingle {
					t.Fatalf("k=%d data=%d bit=%d: status %v, want corrected-single", k, data, bit, st)
				}
				if got != data || fixed != cw {
					t.Fatalf("k=%d data=%d bit=%d: got data=%d, want %d", k, data, bit, got, data)
				}
			}
		}
	}
}

// TestExhaustiveDoubleBitFlip: every double flip must be DETECTED and must
// never be reported as corrected/clean.
func TestExhaustiveDoubleBitFlip(t *testing.T) {
	p, _ := NewParams(4)
	for data := uint64(0); data < 16; data++ {
		cw := p.Encode(data)
		for _, pair := range combinations(p.TotalBits, 2) {
			bad := cw ^ (1 << uint(pair[0])) ^ (1 << uint(pair[1]))
			_, st, _ := p.Decode(bad)
			if st != StatusDoubleError {
				t.Fatalf("data=%d bits=%v: status %v, want double-error-detected", data, pair, st)
			}
		}
	}
}

// TestExhaustiveErasures: every erasure pattern of size 1..3 (declared
// recoverable) must recover the exact original data for every data word.
func TestExhaustiveErasures(t *testing.T) {
	p, _ := NewParams(4)
	for data := uint64(0); data < 16; data++ {
		cw := p.Encode(data)
		for size := 1; size <= 3; size++ {
			for _, set := range combinations(p.TotalBits, size) {
				erased := make([]bool, p.TotalBits)
				for _, pos := range set {
					erased[pos] = true
				}
				got, st, _ := p.DecodeWithErasures(cw, erased)
				if st != StatusRecoveredErasure || got != data {
					t.Fatalf("data=%d erasures=%v: status %v data %d, want recovered %d",
						data, set, st, got, data)
				}
			}
		}
	}
}

// TestExhaustiveErasurePlusError: 1 erasure + 1 unknown error is declared
// recoverable; verify exhaustively.
func TestExhaustiveErasurePlusError(t *testing.T) {
	p, _ := NewParams(4)
	for data := uint64(0); data < 16; data++ {
		cw := p.Encode(data)
		for epos := 0; epos < p.TotalBits; epos++ {
			for xpos := 0; xpos < p.TotalBits; xpos++ {
				if xpos == epos {
					continue
				}
				erased := make([]bool, p.TotalBits)
				erased[epos] = true
				got, st, _ := p.DecodeWithErasures(cw^(1<<uint(xpos)), erased)
				if st != StatusRecoveredErasure || got != data {
					t.Fatalf("data=%d erasure=%d error=%d: status %v data %d",
						data, epos, xpos, st, got)
				}
			}
		}
	}
}

// TestParityBitSelfError: errors in the Hamming parity bits and in the
// overall parity bit itself are corrected like any other bit.
func TestParityBitSelfError(t *testing.T) {
	p, _ := NewParams(11)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		data := rng.Uint64() & ((1 << 11) - 1)
		cw := p.Encode(data)
		for _, bit := range []int{0, 1, 2, 4, 8} { // overall + Hamming parity positions
			got, st, fixed := p.Decode(cw ^ (1 << uint(bit)))
			if st != StatusCorrectedSingle || got != data || fixed != cw {
				t.Fatalf("data=%d parity bit=%d: st=%v got=%d", data, bit, st, got)
			}
		}
	}
}

// buildH independently constructs the parity-check matrix of the extended
// Hamming code from its definition: column j (1..n-1) is the binary of j,
// plus a final all-ones row for the overall parity (column of bit 0 is 0
// in the Hamming rows, 1 in the overall row).
func buildH(p Params) [][]int {
	rows := p.ParityBits + 1
	H := make([][]int, rows)
	for r := 0; r < rows; r++ {
		H[r] = make([]int, p.TotalBits)
	}
	for pos := 1; pos < p.TotalBits; pos++ {
		for r := 0; r < p.ParityBits; r++ {
			H[r][pos] = (pos >> uint(r)) & 1
		}
	}
	for pos := 0; pos < p.TotalBits; pos++ {
		H[p.ParityBits][pos] = 1
	}
	return H
}

// TestIndependentParityCheckMatrix verifies the library's syndrome against
// a from-scratch H*x matrix product over GF(2), and that every encoded
// codeword satisfies H*c == 0.
func TestIndependentParityCheckMatrix(t *testing.T) {
	for _, k := range []int{4, 8, 11, 26, 57} {
		p, _ := NewParams(k)
		H := buildH(p)
		rng := rand.New(rand.NewSource(int64(k)))
		for trial := 0; trial < 200; trial++ {
			data := rng.Uint64() & ((1 << uint(k)) - 1)
			cw := p.Encode(data)
			// Valid codeword: H*c == 0.
			checkSyndrome(t, H, p, cw, 0, 0)
			// Random corruptions: library syndrome must equal H*r.
			for flips := 1; flips <= 3; flips++ {
				bad := cw
				for _, pos := range rng.Perm(p.TotalBits)[:flips] {
					bad ^= 1 << uint(pos)
				}
				syn, par := p.Syndrome(bad)
				checkSyndrome(t, H, p, bad, syn, par)
			}
		}
	}
}

func checkSyndrome(t *testing.T, H [][]int, p Params, cw uint64, wantSyn uint64, wantPar int) {
	t.Helper()
	var syn uint64
	par := 0
	for r := 0; r < p.ParityBits; r++ {
		acc := 0
		for pos := 0; pos < p.TotalBits; pos++ {
			acc ^= H[r][pos] * int((cw>>uint(pos))&1)
		}
		syn |= uint64(acc) << uint(r)
	}
	for pos := 0; pos < p.TotalBits; pos++ {
		par ^= H[p.ParityBits][pos] * int((cw>>uint(pos))&1)
	}
	if syn != wantSyn || par != wantPar {
		t.Fatalf("k=%d cw=%b: matrix syndrome (%b,%d) != library (%b,%d)",
			p.DataBits, cw, syn, par, wantSyn, wantPar)
	}
}

// TestTripleErrorsAreNotClaimedCorrect documents the honesty contract:
// 3 bit flips always alias to a single-error syndrome (min distance 4),
// so the decoder reports "corrected-single" while the data is actually
// wrong. The API never claims otherwise: a corrected status is only a
// syndrome-consistent outcome. This test pins that behavior so the
// limitation is explicit, not accidental.
func TestTripleErrorsAreNotClaimedCorrect(t *testing.T) {
	p, _ := NewParams(4)
	miscorrected := 0
	total := 0
	for data := uint64(0); data < 16; data++ {
		cw := p.Encode(data)
		for _, set := range combinations(p.TotalBits, 3) {
			bad := cw
			for _, pos := range set {
				bad ^= 1 << uint(pos)
			}
			got, st, _ := p.Decode(bad)
			total++
			if st == StatusDoubleError || st == StatusUncorrectable {
				continue // detected: honest outcome
			}
			if st == StatusCorrectedSingle && got != data {
				miscorrected++ // aliased: documented limitation
			}
		}
	}
	if miscorrected == 0 {
		t.Fatal("expected some triple errors to alias; min-distance property broken?")
	}
	t.Logf("triple flips: %d/%d silently mis-corrected (documented, never claimed reliable)", miscorrected, total)
}

// TestQuadrupleErrorsCanBeUndetectable documents that 4 flips can land on
// another valid codeword and report "clean" with wrong data.
func TestQuadrupleErrorsCanBeUndetectable(t *testing.T) {
	p, _ := NewParams(4)
	undetected := 0
	for data := uint64(0); data < 16; data++ {
		cw := p.Encode(data)
		for _, set := range combinations(p.TotalBits, 4) {
			bad := cw
			for _, pos := range set {
				bad ^= 1 << uint(pos)
			}
			got, st, _ := p.Decode(bad)
			if st == StatusClean && got != data {
				undetected++
			}
		}
	}
	if undetected == 0 {
		t.Fatal("expected some quadruple errors to be undetectable")
	}
	t.Logf("quadruple flips: %d patterns undetectable (reported clean with wrong data)", undetected)
}

// TestRoundTripRandom sweeps random data through encode/decode for all
// supported parameter sizes.
func TestRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, k := range []int{1, 2, 3, 4, 7, 8, 11, 16, 26, 57} {
		p, _ := NewParams(k)
		for i := 0; i < 200; i++ {
			data := rng.Uint64() & ((1 << uint(k)) - 1)
			cw := p.Encode(data)
			if syn, par := p.Syndrome(cw); syn != 0 || par != 0 {
				t.Fatalf("k=%d data=%d: encoded word invalid", k, data)
			}
			got, st, _ := p.Decode(cw)
			if st != StatusClean || got != data {
				t.Fatalf("k=%d data=%d: got %d st=%v", k, data, got, st)
			}
		}
	}
}
