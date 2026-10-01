package strdict

import (
	"encoding/binary"
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

// publishV1V2 builds two generations where identical indices carry different
// meanings, so tests can prove references are never reinterpreted.
func publishV1V2(t *testing.T) (*Registry, *Dictionary, *Dictionary) {
	t.Helper()
	reg := NewRegistry()

	b1 := NewBuilder()
	b1.Add("hello") // 0
	b1.Add("world") // 1
	b1.Add("")      // 2: empty string is poolable
	v1 := reg.Publish(b1)

	b2 := NewBuilder()
	b2.Add("world") // 0: same index, different word than v1
	b2.Add("HELLO") // 1
	b2.Add("")      // 2
	v2 := reg.Publish(b2)

	if v1.Version() != 1 || v2.Version() != 2 {
		t.Fatalf("unexpected versions: %d, %d", v1.Version(), v2.Version())
	}
	return reg, v1, v2
}

func feedAll(t *testing.T, d *Decoder, chunks ...[]byte) []string {
	t.Helper()
	var got []string
	for _, c := range chunks {
		out, err := d.Decode(c)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		got = append(got, out...)
	}
	return got
}

func TestRoundTripMixedRefsAndInline(t *testing.T) {
	reg, v1, _ := publishV1V2(t)
	enc := NewEncoder(v1, nil)
	want := []string{"hello", "inline-word", "world", "", "hello", "another inline"}
	for _, s := range want {
		enc.WriteString(s)
	}
	enc.WriteEnd()

	dec, err := NewDecoder(reg, DecoderOptions{Version: v1.Version()})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	got := feedAll(t, dec, enc.Bytes())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if !dec.Ended() {
		t.Fatal("decoder should report END")
	}
}

func TestRandomChunking(t *testing.T) {
	reg, v1, v2 := publishV1V2(t)
	rng := rand.New(rand.NewSource(42))

	enc := NewEncoder(v1, nil)
	var want []string
	pool := []string{"hello", "world", "", "inline-x", "inline-yy", "hello"}
	for i := 0; i < 200; i++ {
		if i == 100 {
			enc.Switch(v2)
		}
		var s string
		if i >= 100 && i%3 == 0 {
			s = []string{"world", "HELLO", ""}[rng.Intn(3)] // v2 refs
		} else {
			s = pool[rng.Intn(len(pool))]
		}
		want = append(want, s)
		enc.WriteString(s)
	}
	enc.WriteEnd()
	stream := enc.Bytes()

	for trial := 0; trial < 20; trial++ {
		dec, err := NewDecoder(reg, DecoderOptions{Version: v1.Version()})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for off := 0; off < len(stream); {
			n := 1 + rng.Intn(17) // random chunk sizes 1..17
			if off+n > len(stream) {
				n = len(stream) - off
			}
			out, err := dec.Decode(stream[off : off+n])
			if err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
			got = append(got, out...)
			off += n
		}
		dec.Close()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: mismatch\ngot  %q\nwant %q", trial, got, want)
		}
	}
}

func TestRecoveryByteByByteAndResume(t *testing.T) {
	reg, v1, _ := publishV1V2(t)
	enc := NewEncoder(v1, nil)
	want := []string{"hello", "inline", "world", ""}
	for _, s := range want {
		enc.WriteString(s)
	}
	enc.WriteEnd()
	stream := enc.Bytes()

	// Whole-buffer decode.
	decWhole, _ := NewDecoder(reg, DecoderOptions{Version: 1})
	gotWhole := feedAll(t, decWhole, stream)
	decWhole.Close()

	// Byte-by-byte decode must recover identical output.
	decByte, _ := NewDecoder(reg, DecoderOptions{Version: 1})
	var gotByte []string
	for i := 0; i < len(stream); i++ {
		out, err := decByte.Decode(stream[i : i+1])
		if err != nil {
			t.Fatal(err)
		}
		gotByte = append(gotByte, out...)
	}
	decByte.Close()
	if !reflect.DeepEqual(gotWhole, want) || !reflect.DeepEqual(gotByte, want) {
		t.Fatalf("whole=%q byte=%q want=%q", gotWhole, gotByte, want)
	}

	// A truncated stream yields nothing until the missing bytes arrive.
	decPart, _ := NewDecoder(reg, DecoderOptions{Version: 1})
	defer decPart.Close()
	cut := len(stream) - 3 // split inside the final records
	out1, err := decPart.Decode(stream[:cut])
	if err != nil {
		t.Fatal(err)
	}
	out2, err := decPart.Decode(stream[cut:])
	if err != nil {
		t.Fatal(err)
	}
	if got := append(out1, out2...); !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed decode got %q want %q", got, want)
	}
}

func TestVersionSwitchSemantics(t *testing.T) {
	reg, v1, v2 := publishV1V2(t)

	enc := NewEncoder(v1, nil)
	enc.WriteString("hello") // REF 0 under v1
	enc.WriteString("world") // REF 1 under v1
	enc.Switch(v2)           // explicit switch point
	enc.WriteString("world") // REF 0 under v2
	enc.WriteString("HELLO") // REF 1 under v2
	enc.WriteEnd()

	dec, err := NewDecoder(reg, DecoderOptions{Version: v1.Version()})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	got := feedAll(t, dec, enc.Bytes())
	want := []string{"hello", "world", "world", "HELLO"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}

	// References emitted BEFORE the switch must not take on v2 meanings:
	// "hello" (v1#0) would decode as "world" if reinterpreted under v2.
	if got[0] != "hello" {
		t.Fatalf("pre-switch reference reinterpreted: %q", got[0])
	}
}

func TestForcedHashCollision(t *testing.T) {
	constant := func(string) uint64 { return 7 } // every string collides
	reg := NewRegistry()
	b := NewBuilderWithHash(constant)
	if i := b.Add("alpha"); i != 0 {
		t.Fatalf("alpha index %d", i)
	}
	if i := b.Add("beta"); i != 1 {
		t.Fatalf("beta index %d", i)
	}
	if i := b.Add("alpha"); i != 0 {
		t.Fatalf("alpha dedupe index %d", i)
	}
	if i := b.Add(""); i != 2 {
		t.Fatalf("empty index %d", i)
	}
	d := reg.Publish(b)

	for i, s := range []string{"alpha", "beta", ""} {
		got, ok := d.Lookup(i)
		if !ok || got != s {
			t.Fatalf("Lookup(%d) = %q,%v want %q", i, got, ok, s)
		}
		idx, ok := d.IndexOf(constant, s)
		if !ok || idx != i {
			t.Fatalf("IndexOf(%q) = %d,%v want %d", s, idx, ok, i)
		}
	}

	enc := NewEncoder(d, constant)
	enc.WriteString("beta")
	enc.WriteString("alpha")
	enc.WriteString("")
	enc.WriteEnd()
	dec, err := NewDecoder(reg, DecoderOptions{Version: d.Version()})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	if got := feedAll(t, dec, enc.Bytes()); !reflect.DeepEqual(got, []string{"beta", "alpha", ""}) {
		t.Fatalf("got %q", got)
	}
}

func TestEmptyStringPooledUnknownIndexRejected(t *testing.T) {
	reg, v1, _ := publishV1V2(t)
	enc := NewEncoder(v1, nil)
	enc.WriteString("") // REF 2
	enc.WriteEnd()
	dec, err := NewDecoder(reg, DecoderOptions{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := feedAll(t, dec, enc.Bytes())
	dec.Close()
	if !reflect.DeepEqual(got, []string{""}) {
		t.Fatalf("empty-string ref decoded as %q", got)
	}

	// Unknown index must error, never decode as "".
	var buf []byte
	buf = append(buf, opRef)
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], 99)
	buf = append(buf, tmp[:n]...)
	dec2, err := NewDecoder(reg, DecoderOptions{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer dec2.Close()
	out, err := dec2.Decode(buf)
	if err == nil {
		t.Fatalf("unknown index accepted, out=%q", out)
	}
}

func TestReclaimWithLeases(t *testing.T) {
	reg, v1, _ := publishV1V2(t)
	lease, err := reg.Acquire(v1.Version())
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Reclaim(v1.Version()); err == nil {
		t.Fatal("reclaim succeeded despite active lease")
	}
	lease.Release()
	if err := reg.Reclaim(v1.Version()); err != nil {
		t.Fatalf("reclaim after release: %v", err)
	}
	if _, err := reg.Acquire(v1.Version()); err == nil {
		t.Fatal("acquired reclaimed version")
	}
}

func TestBatchCancellation(t *testing.T) {
	reg, v1, _ := publishV1V2(t)

	dec, err := NewDecoder(reg, DecoderOptions{Version: v1.Version()})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	reg.CancelAll() // batch cancel every generation

	// Existing session: next REF lookup fails through the invalidated lease.
	enc := NewEncoder(v1, nil)
	enc.WriteString("hello")
	if _, err := dec.Decode(enc.Bytes()); !errors.Is(err, ErrLeaseInvalid) {
		t.Fatalf("decode after cancel: %v", err)
	}
	// New sessions cannot start.
	if _, err := NewDecoder(reg, DecoderOptions{Version: v1.Version()}); err == nil {
		t.Fatal("new decoder acquired cancelled version")
	}
	// Reclaim still waits for the outstanding lease to be released.
	if err := reg.Reclaim(v1.Version()); err == nil {
		t.Fatal("reclaim succeeded while cancelled lease outstanding")
	}
}

func TestBudgetIncludesDictionaryAndStaging(t *testing.T) {
	reg, v1, _ := publishV1V2(t)
	dictBytes := v1.Bytes() // "hello"+"world"+"" = 10

	// Budget smaller than the dictionary itself: session cannot start.
	if _, err := NewDecoder(reg, DecoderOptions{Version: 1, MaxBytes: dictBytes - 1}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected budget error, got %v", err)
	}

	// Budget fits the dictionary but not one decoded "hello" (5 bytes).
	dec, err := NewDecoder(reg, DecoderOptions{Version: 1, MaxBytes: dictBytes + 4})
	if err != nil {
		t.Fatal(err)
	}
	enc := NewEncoder(v1, nil)
	enc.WriteString("hello")
	if _, err := dec.Decode(enc.Bytes()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected budget error, got %v", err)
	}
	// Sticky: the session refuses any further expansion.
	if _, err := dec.Decode([]byte{opEnd}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("sticky budget error expected, got %v", err)
	}
	dec.Close()

	// Staging counts too: bytes of an incomplete record hold budget while
	// buffered. An unterminated REF varint lets staging grow byte by byte.
	dec2, err := NewDecoder(reg, DecoderOptions{Version: 1, MaxBytes: dictBytes + 3})
	if err != nil {
		t.Fatal(err)
	}
	defer dec2.Close()
	if _, err := dec2.Decode([]byte{opRef, 0x80, 0x80}); err != nil {
		t.Fatalf("3 staged bytes fit the budget, got %v", err)
	}
	if _, err := dec2.Decode([]byte{0x80}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("staging growth should exceed budget, got %v", err)
	}

	// A declared inline length that cannot fit fails on the header alone,
	// before any payload bytes are buffered (see TestLengthValidatedBeforeAllocation).
	dec3, err := NewDecoder(reg, DecoderOptions{Version: 1, MaxBytes: dictBytes + 3})
	if err != nil {
		t.Fatal(err)
	}
	defer dec3.Close()
	if _, err := dec3.Decode([]byte{opInline, 0x05}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("declared length beyond budget should fail on header, got %v", err)
	}
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	reg, v1, _ := publishV1V2(t)
	dictBytes := v1.Bytes()

	// A gigantic declared length fails on the header alone, before any
	// payload bytes (or allocation for them) arrive.
	dec, err := NewDecoder(reg, DecoderOptions{Version: 1, MaxBytes: dictBytes + 64})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	var huge [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(huge[:], 1<<40)
	hdr := append([]byte{opInline}, huge[:n]...)
	if _, err := dec.Decode(hdr); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("huge length should fail on header, got %v", err)
	}

	// Per-record cap triggers even with an unlimited total budget.
	dec2, err := NewDecoder(reg, DecoderOptions{Version: 1, MaxRecordBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer dec2.Close()
	n = binary.PutUvarint(huge[:], 9)
	hdr = append([]byte{opInline}, huge[:n]...)
	if _, err := dec2.Decode(hdr); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("per-record cap expected, got %v", err)
	}
}

func TestBadOpcodeAndStreamEnd(t *testing.T) {
	reg, _, _ := publishV1V2(t)
	dec, err := NewDecoder(reg, DecoderOptions{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode([]byte{0x7f}); !errors.Is(err, ErrBadOpcode) {
		t.Fatalf("bad opcode: %v", err)
	}
	dec.Close()

	dec2, err := NewDecoder(reg, DecoderOptions{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer dec2.Close()
	if _, err := dec2.Decode([]byte{opEnd}); err != nil {
		t.Fatal(err)
	}
	if _, err := dec2.Decode([]byte{opEnd}); !errors.Is(err, ErrStreamEnded) {
		t.Fatalf("decode after END: %v", err)
	}
}
