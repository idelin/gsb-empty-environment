package secded

import "testing"

// TestExhaustiveSingleError flips every single bit of every codeword
// (all data values) for m = 2,3,4 and requires exact correction,
// including errors in the parity bits themselves.
func TestExhaustiveSingleError(t *testing.T) {
	for _, m := range []int{2, 3, 4} {
		c, err := New(m)
		if err != nil {
			t.Fatal(err)
		}
		for data := uint64(0); data < 1<<uint(c.K); data++ {
			cw := c.Encode(data)
			for pos := 1; pos <= c.N; pos++ {
				bad := cw ^ (1 << uint(pos-1))
				got, st, corr := c.Decode(bad)
				if pos == c.N {
					// overall parity bit itself flipped
					if st != StatusParityOnly {
						t.Fatalf("m=%d data=%d pos=%d: status %v, want parity-bit-only", m, data, pos, st)
					}
				} else if st != StatusCorrected {
					t.Fatalf("m=%d data=%d pos=%d: status %v, want corrected-single", m, data, pos, st)
				}
				if corr != pos {
					t.Fatalf("m=%d data=%d pos=%d: corrected pos %d", m, data, pos, corr)
				}
				if got != data {
					t.Fatalf("m=%d data=%d pos=%d: got data %d, want %d", m, data, pos, got, data)
				}
			}
		}
	}
}

// TestExhaustiveDoubleError flips every pair of bits of every codeword
// and requires the decoder to report StatusDouble - it must never
// claim a repair.
func TestExhaustiveDoubleError(t *testing.T) {
	for _, m := range []int{2, 3, 4} {
		c, _ := New(m)
		for data := uint64(0); data < 1<<uint(c.K); data++ {
			cw := c.Encode(data)
			for p1 := 1; p1 <= c.N; p1++ {
				for p2 := p1 + 1; p2 <= c.N; p2++ {
					bad := cw ^ (1 << uint(p1-1)) ^ (1 << uint(p2-1))
					_, st, _ := c.Decode(bad)
					if st != StatusDouble {
						t.Fatalf("m=%d data=%d pos=%d,%d: status %v, want double-error-uncorrectable",
							m, data, p1, p2, st)
					}
				}
			}
		}
	}
}

// TestExhaustiveErasures recovers every erasure pattern of size 1..3
// (the declared recoverable range) for every data word of the (8,4)
// code, including erasures of parity bits and the overall parity bit.
func TestExhaustiveErasures(t *testing.T) {
	c, _ := New(3)
	var subsets [][]int
	for size := 1; size <= 3; size++ {
		var rec func(start int, cur []int)
		rec = func(start int, cur []int) {
			if len(cur) == size {
				subsets = append(subsets, append([]int(nil), cur...))
				return
			}
			for p := start; p <= c.N; p++ {
				rec(p+1, append(cur, p))
			}
		}
		rec(1, nil)
	}
	if len(subsets) != 8+28+56 {
		t.Fatalf("expected 92 erasure patterns, got %d", len(subsets))
	}
	for data := uint64(0); data < 1<<uint(c.K); data++ {
		cw := c.Encode(data)
		for _, er := range subsets {
			got, _, err := c.DecodeWithErasures(cw, er)
			if err != nil {
				t.Fatalf("data=%d erasures=%v: %v", data, er, err)
			}
			if got != data {
				t.Fatalf("data=%d erasures=%v: got %d", data, er, got)
			}
		}
	}
}

// TestErasureBeyondCapability: 4 erasures exceed d_min-1 and are refused.
func TestErasureBeyondCapability(t *testing.T) {
	c, _ := New(3)
	cw := c.Encode(0b1011)
	if _, _, err := c.DecodeWithErasures(cw, []int{1, 2, 3, 4}); err != ErrTooManyErasures {
		t.Fatalf("want ErrTooManyErasures, got %v", err)
	}
}

// TestErasurePlusError: 1 erasure + 1 unknown error is within
// 2t+e <= 3 and must decode; 2 erasures + 1 error must be refused.
func TestErasurePlusError(t *testing.T) {
	c, _ := New(4)
	for data := uint64(0); data < 1<<uint(c.K); data += 97 {
		cw := c.Encode(data)
		// 1 erasure + 1 error at a different position
		bad := cw ^ (1 << 6) // error at position 7
		got, _, err := c.DecodeWithErasures(bad, []int{3})
		if err != nil || got != data {
			t.Fatalf("data=%d: 1 erasure + 1 error: got=%d err=%v", data, got, err)
		}
		// 2 erasures + 1 error: beyond capability, must not guess
		if _, _, err := c.DecodeWithErasures(bad, []int{3, 5}); err != ErrErasureAmbiguous {
			t.Fatalf("data=%d: 2 erasures + 1 error: want ErrErasureAmbiguous, got %v", data, err)
		}
	}
}

// TestParityCheckMatrixCrossVerify validates Syndrome() against the
// independently constructed H matrix: H*cw^T == 0 for every codeword,
// and the syndrome of every single-bit error equals that column of H.
func TestParityCheckMatrixCrossVerify(t *testing.T) {
	for _, m := range []int{2, 3, 4} {
		c, _ := New(m)
		h := c.ParityCheckMatrix()
		if len(h) != m+1 {
			t.Fatalf("m=%d: H has %d rows", m, len(h))
		}
		for data := uint64(0); data < 1<<uint(c.K); data++ {
			cw := c.Encode(data)
			for r, bit := range c.HSyndrome(cw) {
				if bit != 0 {
					t.Fatalf("m=%d data=%d: H*cw row %d = 1", m, data, r)
				}
			}
			for pos := 1; pos <= c.N; pos++ {
				bad := cw ^ (1 << uint(pos-1))
				hs := c.HSyndrome(bad)
				// Independent expectation: syndrome bits = column pos of H.
				for r := 0; r <= c.M; r++ {
					want := int(h[r]>>uint(pos-1)) & 1
					if hs[r] != want {
						t.Fatalf("m=%d data=%d pos=%d row=%d: H-syndrome %d, want %d",
							m, data, pos, r, hs[r], want)
					}
				}
				// And Syndrome() must agree with the H-based view.
				s, p := c.Syndrome(bad)
				if p != hs[0] {
					t.Fatalf("m=%d data=%d pos=%d: overall parity %d != H row0 %d", m, data, pos, p, hs[0])
				}
				sy := 0
				for j := 0; j < c.M; j++ {
					sy |= hs[j+1] << uint(j)
				}
				if s != sy {
					t.Fatalf("m=%d data=%d pos=%d: syndrome %d != H rows %d", m, data, pos, s, sy)
				}
			}
		}
	}
}

// TestTripleErrorMayMiscorrect documents the code's limit: EVERY
// weight-3 error pattern aliases to a single-error syndrome (odd
// weight flips the overall parity, and d_min = 4 means 3 distinct
// columns never sum to zero), so it is "corrected" to a WRONG
// codeword. The test pins this behavior so the honesty contract
// (StatusCorrected is maximum-likelihood, never proven) stays true.
func TestTripleErrorMayMiscorrect(t *testing.T) {
	c, _ := New(3)
	miscorrected, total := 0, 0
	for data := uint64(0); data < 1<<uint(c.K); data++ {
		cw := c.Encode(data)
		for p1 := 1; p1 <= c.N; p1++ {
			for p2 := p1 + 1; p2 <= c.N; p2++ {
				for p3 := p2 + 1; p3 <= c.N; p3++ {
					total++
					bad := cw ^ (1 << uint(p1-1)) ^ (1 << uint(p2-1)) ^ (1 << uint(p3-1))
					got, st, _ := c.Decode(bad)
					if st == StatusDouble {
						t.Fatalf("weight-3 error reported as double error (impossible for d_min=4)")
					}
					if got != data {
						miscorrected++
					}
				}
			}
		}
	}
	if miscorrected == 0 {
		t.Fatal("expected weight-3 errors to be miscorrected; decoder changed?")
	}
	t.Logf("weight-3 errors: %d/%d miscorrected (aliased to single error) - "+
		"this is why StatusCorrected is never a proof of correctness", miscorrected, total)
}

// TestCleanRoundTrip: untouched codewords decode as StatusClean.
func TestCleanRoundTrip(t *testing.T) {
	c, _ := New(6)
	for data := uint64(0); data < 1<<20; data += 7919 {
		cw := c.Encode(data)
		got, st, _ := c.Decode(cw)
		if st != StatusClean || got != data {
			t.Fatalf("data=%d: st=%v got=%d", data, st, got)
		}
	}
}
