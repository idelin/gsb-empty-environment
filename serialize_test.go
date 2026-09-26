package efano

import (
	"encoding/binary"
	"hash/crc32"
	"math/rand/v2"
	"slices"
	"testing"
)

// fixCRC recomputes the CRC of a (possibly mutated) serialized buffer,
// so tests can corrupt specific fields while keeping the checksum valid.
func fixCRC(data []byte) {
	crc := crc32.NewIEEE()
	crc.Write(data[:44])
	crc.Write(data[headerSize:])
	binary.LittleEndian.PutUint32(data[44:], crc.Sum32())
}

func TestSerializeParseRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	for _, tc := range testCases(rng) {
		t.Run(tc.name, func(t *testing.T) {
			ef, err := EncodeAuto(tc.vals)
			if err != nil {
				t.Fatal(err)
			}
			data := ef.MarshalBinary()
			if uint64(len(data)) != ef.SerializedSize() {
				t.Fatalf("len(MarshalBinary) = %d, SerializedSize = %d", len(data), ef.SerializedSize())
			}
			back, err := Parse(data)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if err := back.Verify(); err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if !slices.Equal(back.Decode(), tc.vals) {
				t.Fatal("parsed index decodes differently from reference")
			}
			if !slices.Equal(back.MarshalBinary(), data) {
				t.Fatal("re-serialization is not byte-identical")
			}
		})
	}
}

func TestRandomBitMutationDetected(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	ref := randNonDecreasing(rng, 300, 1<<16)
	ef, err := EncodeAuto(ref)
	if err != nil {
		t.Fatal(err)
	}
	good := ef.MarshalBinary()
	if _, err := Parse(good); err != nil {
		t.Fatalf("clean parse failed: %v", err)
	}
	// CRC-32 detects every single-bit corruption; flip random bits all
	// over the buffer (header and payload) and require rejection.
	for trial := 0; trial < 500; trial++ {
		bad := slices.Clone(good)
		byteIdx := rng.Uint64() % uint64(len(bad))
		bad[byteIdx] ^= 1 << (rng.Uint64() % 8)
		if _, err := Parse(bad); err == nil {
			t.Fatalf("trial %d: single-bit flip at byte %d went undetected", trial, byteIdx)
		}
	}
	// Targeted structural tampering with a repaired CRC must still fail.
	tamper := func(mutate func([]byte)) {
		bad := slices.Clone(good)
		mutate(bad)
		fixCRC(bad)
		if _, err := Parse(bad); err == nil {
			t.Fatal("structural tampering accepted")
		}
	}
	tamper(func(b []byte) { b[4] = 99 })                                // version
	tamper(func(b []byte) { b[5] = 64 })                                // l out of range
	tamper(func(b []byte) { b[6] = 1 })                                 // reserved byte
	tamper(func(b []byte) { binary.LittleEndian.PutUint64(b[8:], 1) })  // n
	tamper(func(b []byte) { binary.LittleEndian.PutUint64(b[32:], 1) }) // lowBits != n*l
	// Truncation and extension are length errors.
	if _, err := Parse(good[:len(good)-1]); err == nil {
		t.Fatal("truncated input accepted")
	}
	if _, err := Parse(append(slices.Clone(good), 0)); err == nil {
		t.Fatal("extended input accepted")
	}
}

func TestByteLedgerMatchesFormat(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	for _, tc := range testCases(rng) {
		t.Run(tc.name, func(t *testing.T) {
			ef, err := EncodeAuto(tc.vals)
			if err != nil {
				t.Fatal(err)
			}
			st := ef.Stats()
			n := uint64(len(tc.vals))
			var wantHigh, wantLow uint64
			if n > 0 {
				wantHigh = n + tc.vals[n-1]>>ef.LowBits() // ones + unary zeros
			}
			wantLow = n * uint64(ef.LowBits())
			if st.HighBits != wantHigh {
				t.Errorf("HighBits = %d, want %d", st.HighBits, wantHigh)
			}
			if st.LowBits != wantLow {
				t.Errorf("LowBits = %d, want %d", st.LowBits, wantLow)
			}
			// Exact serialized ledger: header + byte-padded vectors.
			wantSer := uint64(headerSize) + (wantHigh+7)/8 + (wantLow+7)/8
			if st.SerializedBytes != wantSer {
				t.Errorf("SerializedBytes = %d, want %d", st.SerializedBytes, wantSer)
			}
			// In-memory ledger: allocated words + rank directory.
			wantRank := ((wantHigh+superblockBits-1)/superblockBits + 1) * 8
			if st.RankBytes != wantRank {
				t.Errorf("RankBytes = %d, want %d", st.RankBytes, wantRank)
			}
			wantIdx := ((wantHigh+63)/64+(wantLow+63)/64)*8 + wantRank
			if st.IndexBytes != wantIdx {
				t.Errorf("IndexBytes = %d, want %d", st.IndexBytes, wantIdx)
			}
			t.Logf("%-12s n=%5d l=%2d high=%7db low=%7db rank=%4dB index=%6dB raw=%7dB",
				tc.name, n, ef.LowBits(), wantHigh, wantLow, wantRank, st.IndexBytes, n*8)
		})
	}
}

func TestChooseLDerivationBound(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 12))
	for trial := 0; trial < 10000; trial++ {
		n := 1 + rng.Uint64()%(1<<20)
		u := n + rng.Uint64()%(1<<40) // universe >= n
		l := ChooseL(u, n)
		// zeros = floor((u-1)/2^l) must lie in [n, 2n) for u > n.
		zeros := (u - 1) >> l
		total := n*uint64(l) + n + zeros
		if u > n {
			if zeros < n || zeros >= 2*n {
				t.Fatalf("u=%d n=%d l=%d: zeros %d outside [n,2n)", u, n, l, zeros)
			}
			// T <= n*(log2(u/n) + 3): check against bit-length form.
			// log2(u/n) >= l, so n*l + 3n is a valid upper bound.
			if total > n*uint64(l)+3*n {
				t.Fatalf("u=%d n=%d l=%d: total %d exceeds n*l+3n", u, n, l, total)
			}
		}
	}
	// Degenerate parameters never divide by zero and give l = 0.
	for _, args := range [][2]uint64{{0, 0}, {0, 5}, {10, 10}, {7, 500}, {1, 1}} {
		if l := ChooseL(args[0], args[1]); l != 0 {
			t.Fatalf("ChooseL(%d,%d) = %d, want 0", args[0], args[1], l)
		}
	}
}
