package netsec

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"testing"
)

// labelSetRef is the reference authorization model.
func labelSetRef(labels []string) map[string]bool {
	set := make(map[string]bool, len(labels))
	for _, l := range labels {
		set[l] = true
	}
	return set
}

// referenceMaskedAt answers the masking decision for one byte by checking
// every source range independently. A byte is hidden while any covering range
// carries a label the reader has not been granted.
func referenceMaskedAt(ranges []Range, off int64, auth map[string]bool) bool {
	for _, r := range ranges {
		if off >= r.Start && off < r.End && !auth[r.Label] {
			return true
		}
	}
	return false
}

// referenceView builds the expected full view byte by byte.
func referenceView(origin []byte, ranges []Range, auth map[string]bool, maskByte byte) []byte {
	view := make([]byte, len(origin))
	for i := range origin {
		if referenceMaskedAt(ranges, int64(i), auth) {
			view[i] = maskByte
		} else {
			view[i] = origin[i]
		}
	}
	return view
}

var labelPool = []string{"alpha", "bravo", "charlie", "delta", "echo"}

func randomRanges(rng *rand.Rand, n int, length int64) []Range {
	ranges := make([]Range, 0, n)
	for i := 0; i < n; i++ {
		start := rng.Int64N(length)
		span := rng.Int64N(length/4+1) + 1
		end := start + span
		if end > length {
			end = length
		}
		if end <= start {
			continue
		}
		ranges = append(ranges, Range{
			Start: start,
			End:   end,
			Label: labelPool[rng.IntN(len(labelPool))],
		})
	}
	return ranges
}

func rangesEqual(a, b []Range) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertView(t *testing.T, r *Reader, origin []byte, want string) {
	t.Helper()
	got := make([]byte, len(origin))
	if n, err := r.ReadAt(got, 0); err != nil || n != len(origin) {
		t.Fatalf("ReadAt full: n=%d err=%v", n, err)
	}
	if string(got) != want {
		t.Fatalf("view = %q want %q", got, want)
	}
}

// TestRandomizedAgainstReference compares whole-view, windowed reads and proof
// ranges against the independent byte-by-byte reference over many random,
// heavily overlapping range sets and label subsets.
func TestRandomizedAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x1278, 0x9abc))
	maskByte := byte('#')
	const length int64 = 300

	for iter := 0; iter < 400; iter++ {
		origin := make([]byte, length)
		for i := range origin {
			b := byte('A' + rng.IntN(26))
			if b == maskByte {
				b = 'Z'
			}
			origin[i] = b
		}
		ranges := randomRanges(rng, 1+rng.IntN(12), length)

		doc := NewDocument(origin, WithMaskByte(maskByte))
		v, err := doc.AddRanges(ranges...)
		if err != nil {
			t.Fatalf("iter %d: AddRanges: %v", iter, err)
		}

		for sub := 0; sub < 1<<len(labelPool); sub++ {
			var granted []string
			for i, label := range labelPool {
				if sub&(1<<i) != 0 {
					granted = append(granted, label)
				}
			}
			auth := labelSetRef(granted)
			want := referenceView(origin, v.Ranges(), auth, maskByte)

			r := v.Reader(granted...)

			got := make([]byte, length)
			if n, err := r.ReadAt(got, 0); err != nil || n != int(length) {
				t.Fatalf("iter %d sub %d: full ReadAt n=%d err=%v", iter, sub, n, err)
			}
			if !bytes.Equal(got, want) {
				if iter == 8 && sub == 0 {
					t.Logf("GOT:  %q", got)
					t.Logf("WANT: %q", want)
					t.Logf("segs: %+v", v.segs)
					t.Logf("masked: %+v", r.MaskedRanges())
				}
				t.Fatalf("iter %d sub %d: full view mismatch", iter, sub)
			}

			for off := int64(0); off < length; off++ {
				var one [1]byte
				if n, err := r.ReadAt(one[:], off); err != nil || n != 1 {
					t.Fatalf("iter %d sub %d off %d: n=%d err=%v", iter, sub, off, n, err)
				}
				if one[0] != want[off] {
					t.Fatalf("iter %d sub %d off %d: got %q want %q", iter, sub, off, one[0], want[off])
				}
			}

			for w := 0; w < 20; w++ {
				off := rng.Int64N(length)
				size := rng.IntN(40) + 1
				buf := make([]byte, size)
				n, err := r.ReadAt(buf, off)
				avail := size
				wantErr := error(nil)
				if off+int64(size) > length {
					avail = int(length - off)
					wantErr = io.EOF
				}
				if n != avail || !errors.Is(err, wantErr) {
					t.Fatalf("iter %d sub %d window: n=%d want %d err=%v wantErr %v",
						iter, sub, n, avail, err, wantErr)
				}
				if !bytes.Equal(buf[:avail], want[off:off+int64(avail)]) {
					t.Fatalf("iter %d sub %d window off=%d size=%d mismatch", iter, sub, off, size)
				}
			}

			proof := r.Proof()
			if proof.Version != v.Number() || proof.MaskByte != maskByte {
				t.Fatalf("iter %d sub %d: bad proof header %+v", iter, sub, proof)
			}
			assertProofMatchesReference(t, origin, v.Ranges(), auth, proof.Masked)
		}
	}
}

func assertProofMatchesReference(t *testing.T, origin []byte, ranges []Range, auth map[string]bool, masked []Range) {
	t.Helper()
	hidden := make([]bool, len(origin))
	for i := range origin {
		hidden[i] = referenceMaskedAt(ranges, int64(i), auth)
	}
	covered := make([]bool, len(origin))
	var prevEnd int64
	for idx, mr := range masked {
		if mr.Label != "" {
			t.Fatalf("proof range must not leak label: %+v", mr)
		}
		if mr.Start < 0 || mr.End <= mr.Start || mr.End > int64(len(origin)) {
			t.Fatalf("malformed proof range %+v", mr)
		}
		if mr.Start < prevEnd {
			t.Fatalf("proof ranges overlap/out of order: %+v after %d", mr, prevEnd)
		}
		if idx > 0 && mr.Start == prevEnd {
			t.Fatalf("adjacent proof ranges not coalesced at %d", mr.Start)
		}
		prevEnd = mr.End
		for off := mr.Start; off < mr.End; off++ {
			covered[off] = true
		}
	}
	for i, want := range hidden {
		if covered[i] != want {
			t.Fatalf("proof coverage mismatch at %d: proof=%v reference=%v", i, covered[i], want)
		}
	}
}
