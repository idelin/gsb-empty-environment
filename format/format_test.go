package format_test

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"reflect"
	"testing"

	"pkv/format"
	"pkv/gen"
)

func TestRoundTripAndDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		m := gen.ValidMessage(r)
		if !format.InScope(m) {
			t.Fatalf("iter %d: generator produced out-of-scope message", i)
		}
		b1, err := format.Encode(m)
		if err != nil {
			t.Fatalf("iter %d: encode: %v", i, err)
		}
		b2, _ := format.Encode(m)
		if !bytes.Equal(b1, b2) {
			t.Fatalf("iter %d: encode not deterministic", i)
		}
		dec, err := format.Decode(b1)
		if err != nil {
			t.Fatalf("iter %d: decode: %v", i, err)
		}
		if !reflect.DeepEqual(dec, m) {
			t.Fatalf("iter %d: round-trip mismatch:\n got %+v\nwant %+v", i, dec, m)
		}
		re, _ := format.Encode(dec)
		if !bytes.Equal(re, b1) {
			t.Fatalf("iter %d: encode(decode(x)) != x", i)
		}
		if len(b1) > format.MaxMessageSize {
			t.Fatalf("iter %d: in-scope message encoded over budget (%d bytes)", i, len(b1))
		}
	}
}

func TestMutationsRejectedWithExpectedClass(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 200; i++ {
		m := gen.ValidMessage(r)
		canon, err := format.Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, mut := range gen.Mutations() {
			mt := mut.Apply(m, canon)
			if mt.Expect == format.ClassNone {
				continue // valid mutant covered by its own test
			}
			_, err := format.Decode(mt.Data)
			if err == nil {
				t.Fatalf("iter %d mutation %s: expected rejection, got accept", i, mt.Name)
			}
			if got := format.ClassOf(err); got != mt.Expect {
				t.Fatalf("iter %d mutation %s: class=%s, want %s", i, mt.Name, got, mt.Expect)
			}
		}
	}
}

func TestValidMutationStillAccepted(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		m := gen.ValidMessage(r)
		canon, _ := format.Encode(m)
		for _, mut := range gen.Mutations() {
			mt := mut.Apply(m, canon)
			if mt.Expect != format.ClassNone {
				continue
			}
			dec, err := format.Decode(mt.Data)
			if err != nil {
				t.Fatalf("iter %d: still-valid mutant %s rejected: %v", i, mt.Name, err)
			}
			if !reflect.DeepEqual(dec, mt.Want) {
				t.Fatalf("iter %d: valid mutant decoded to %+v, want %+v", i, dec, mt.Want)
			}
		}
	}
}

// Length prefixes must be validated before allocation/copying; absurd
// lengths in tiny buffers must produce classified errors, not panics.
func TestLengthValidatedBeforeAllocation(t *testing.T) {
	m := &format.Message{Records: []format.Record{{Key: "k", Value: "v"}}}
	canon, _ := format.Encode(m)

	hugeKey := append([]byte(nil), canon...)
	binary.BigEndian.PutUint16(hugeKey[6:], 0xFFFF)
	if _, err := format.Decode(hugeKey); format.ClassOf(err) != format.ClassKeyLen {
		t.Fatalf("huge klen: got %v", err)
	}

	hugeVal := append([]byte(nil), canon...)
	binary.BigEndian.PutUint16(hugeVal[6+2+1:], 0xFFFF)
	if _, err := format.Decode(hugeVal); format.ClassOf(err) != format.ClassValueLen {
		t.Fatalf("huge vlen: got %v", err)
	}

	if _, err := format.Decode(canon[:7]); format.ClassOf(err) != format.ClassTruncated {
		t.Fatalf("truncated: got %v", err)
	}
	if _, err := format.Decode(make([]byte, format.MaxMessageSize+1)); format.ClassOf(err) != format.ClassTooLarge {
		t.Fatalf("too large: got %v", err)
	}
}

func TestDecodeRejectsGarbageWithoutPanic(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for i := 0; i < 2000; i++ {
		buf := make([]byte, r.Intn(64))
		r.Read(buf)
		_, _ = format.Decode(buf) // must not panic
	}
}
