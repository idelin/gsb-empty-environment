package bitlayout_test

import (
	"reflect"
	"testing"

	"bitlayout"
)

// encodeSensor produces the acceptance record bits used by chunk tests.
func encodeSensor(t *testing.T) (*bitlayout.Plan, []byte, int64, map[string]any) {
	t.Helper()
	plan := mustCompile(t, sensorSchema)
	rec := map[string]any{
		"tag": uint64(1), "level": int64(-300),
		"body": map[string]any{"count": uint64(3),
			"samples": []any{uint64(2748), uint64(291), uint64(4095)}},
	}
	nbits, err := plan.Validate(rec)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, (nbits+7)/8)
	if _, err := plan.Encode(rec, buf); err != nil {
		t.Fatal(err)
	}
	return plan, buf, nbits, rec
}

// sliceBits returns the sub-slice of the stream bits [from,to) packed
// into bytes starting at a fresh bit 0.
func sliceBits(buf []byte, order bitlayout.BitOrder, from, to int64) ([]byte, int64) {
	n := to - from
	out := make([]byte, (n+7)/8)
	for i := int64(0); i < n; i++ {
		var b bool
		if order == bitlayout.MSBFirst {
			b = buf[(from+i)/8]&(0x80>>uint((from+i)%8)) != 0
		} else {
			b = buf[(from+i)/8]&(1<<uint((from+i)%8)) != 0
		}
		if b {
			if order == bitlayout.MSBFirst {
				out[i/8] |= 0x80 >> uint(i%8)
			} else {
				out[i/8] |= 1 << uint(i%8)
			}
		}
	}
	return out, n
}

// TestAllTwoChunkSplits feeds the record split at every bit position.
func TestAllTwoChunkSplits(t *testing.T) {
	plan, buf, nbits, want := encodeSensor(t)
	full, _, err := plan.Decode(buf, nbits, nil)
	if err != nil {
		t.Fatal(err)
	}
	for cut := int64(0); cut <= nbits; cut++ {
		s := plan.NewSession(nil)
		head, hn := sliceBits(buf, plan.BitOrder, 0, cut)
		s.Feed(head, hn)
		if _, done, err := s.Try(); err != nil {
			t.Fatalf("cut %d: unexpected error after first feed: %v", cut, err)
		} else if done && cut < nbits {
			t.Fatalf("cut %d: decoded before all bits were fed", cut)
		}
		tail, tn := sliceBits(buf, plan.BitOrder, cut, nbits)
		s.Feed(tail, tn)
		got, done, err := s.Try()
		if err != nil || !done {
			t.Fatalf("cut %d: got done=%v err=%v", cut, done, err)
		}
		if !reflect.DeepEqual(got, full) {
			t.Fatalf("cut %d: record mismatch", cut)
		}
	}
	_ = want
}

// TestAllThreeChunkSplits feeds the record split at every pair of bit
// positions.
func TestAllThreeChunkSplits(t *testing.T) {
	plan, buf, nbits, _ := encodeSensor(t)
	full, _, err := plan.Decode(buf, nbits, nil)
	if err != nil {
		t.Fatal(err)
	}
	for c1 := int64(0); c1 <= nbits; c1++ {
		for c2 := c1; c2 <= nbits; c2++ {
			s := plan.NewSession(nil)
			for _, span := range [][2]int64{{0, c1}, {c1, c2}, {c2, nbits}} {
				chunk, n := sliceBits(buf, plan.BitOrder, span[0], span[1])
				s.Feed(chunk, n)
			}
			got, done, err := s.Try()
			if err != nil || !done {
				t.Fatalf("cuts %d,%d: done=%v err=%v", c1, c2, done, err)
			}
			if !reflect.DeepEqual(got, full) {
				t.Fatalf("cuts %d,%d: record mismatch", c1, c2)
			}
		}
	}
}

// TestTruncationNeedsMoreInput: every proper prefix must report
// need-more-input, never a hard error or a partial record.
func TestTruncationNeedsMoreInput(t *testing.T) {
	plan, buf, nbits, _ := encodeSensor(t)
	for cut := int64(0); cut < nbits; cut++ {
		_, _, err := plan.Decode(buf, cut, nil)
		if err == nil {
			t.Fatalf("cut %d: decoded from truncated input", cut)
		}
		e, ok := err.(*bitlayout.Error)
		if !ok || e.Kind != bitlayout.ErrNeedMoreInput {
			t.Fatalf("cut %d: want need-more-input, got %v", cut, err)
		}
	}
}

// TestErrorKinds checks the three hard error categories carry schema
// paths and bit offsets.
func TestErrorKinds(t *testing.T) {
	plan, buf, nbits, _ := encodeSensor(t)

	// reserved bits: set a padding bit (record is 53 bits + 3 pad + 8 crc)
	padded := append([]byte(nil), buf...)
	padded[6] |= 0x04 // bit 53..55 are padding; set bit 55
	_, _, err := plan.Decode(padded, nbits, nil)
	if e, ok := err.(*bitlayout.Error); !ok || e.Kind != bitlayout.ErrReservedBits {
		t.Fatalf("want reserved-bits error, got %v", err)
	} else if e.Path == "" || e.Bit < 0 {
		t.Fatalf("error missing path/bit: %v", e)
	}

	// illegal value: corrupt the checksum
	bad := append([]byte(nil), buf...)
	bad[7] ^= 0x01
	_, _, err = plan.Decode(bad, nbits, nil)
	if e, ok := err.(*bitlayout.Error); !ok || e.Kind != bitlayout.ErrValue {
		t.Fatalf("want illegal-value error, got %v", err)
	} else if e.Path != "/crc" {
		t.Fatalf("checksum error path = %q", e.Path)
	}

	// illegal value: undefined union tag (tag=2 has no case)
	badtag := append([]byte(nil), buf...)
	badtag[0] = badtag[0]&0x1F | 0x40 // tag = 010
	_, _, err = plan.Decode(badtag, nbits, nil)
	if e, ok := err.(*bitlayout.Error); !ok || e.Kind != bitlayout.ErrValue {
		t.Fatalf("want illegal-value error for tag, got %v", err)
	}
}
