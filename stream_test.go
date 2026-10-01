package strdict

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"reflect"
	"testing"
)

func decodeAll(t *testing.T, dec *Decoder) ([]string, error) {
	t.Helper()
	var out []string
	for {
		s, err := dec.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, s)
	}
}

func TestRoundtripMixed(t *testing.T) {
	reg := NewRegistry()
	d := frozenDict(t, 1, "", "alpha", "beta")
	if err := reg.Publish(d); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf, d)
	want := []string{"alpha", "inline-one", "", "beta", "inline-two"}
	for _, s := range want {
		if err := enc.String(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.End(); err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(bytes.NewReader(buf.Bytes()), DecoderConfig{Registry: reg, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	got, err := decodeAll(t, dec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// chunkReader delivers data in random-sized pieces to prove the decoder is
// truly streaming and indifferent to chunk boundaries.
type chunkReader struct {
	data []byte
	max  int
	rng  *rand.Rand
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := 1 + c.rng.Intn(c.max)
	if n > len(c.data) {
		n = len(c.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func TestRandomChunkingAndVersionSwitches(t *testing.T) {
	reg := NewRegistry()
	dicts := map[uint32]*Dictionary{
		1: frozenDict(t, 1, "", "red", "green", "blue"),
		2: frozenDict(t, 2, "blue", "red", "yellow"),
		3: frozenDict(t, 3, "cyan"),
	}
	for _, d := range dicts {
		if err := reg.Publish(d); err != nil {
			t.Fatal(err)
		}
	}
	versions := []uint32{1, 2, 3}
	pool := []string{"", "red", "green", "blue", "yellow", "cyan", "loose-1", "loose-2", "日本語"}

	for seed := int64(1); seed <= 25; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var buf bytes.Buffer
		initial := versions[rng.Intn(len(versions))]
		cur := initial
		enc := NewEncoder(&buf, dicts[cur])
		var want []string
		for i := 0; i < 300; i++ {
			switch rng.Intn(10) {
			case 0: // explicit version switch point
				cur = versions[rng.Intn(len(versions))]
				if err := enc.SwitchTo(dicts[cur]); err != nil {
					t.Fatal(err)
				}
			case 1, 2, 3: // inline
				s := pool[rng.Intn(len(pool))]
				if err := enc.Inline(s); err != nil {
					t.Fatal(err)
				}
				want = append(want, s)
			default: // reference into the current generation
				d := dicts[cur]
				idx := uint32(rng.Intn(d.Len()))
				if err := enc.Ref(idx); err != nil {
					t.Fatal(err)
				}
				s, _ := d.Lookup(idx)
				want = append(want, s)
			}
		}
		if err := enc.End(); err != nil {
			t.Fatal(err)
		}

		// Independent reference check: decode the whole stream through a
		// randomly chunked reader and compare against the recorded sequence.
		cr := &chunkReader{data: buf.Bytes(), max: 9, rng: rand.New(rand.NewSource(seed * 7))}
		dec, err := NewDecoder(cr, DecoderConfig{Registry: reg, Version: initial})
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeAll(t, dec)
		dec.Close()
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: decoded sequence differs from reference", seed)
		}
	}
}

func TestVersionSwitchDoesNotReuseOldReferences(t *testing.T) {
	reg := NewRegistry()
	v1 := frozenDict(t, 1, "apple", "banana")
	v2 := frozenDict(t, 2, "banana", "cherry") // same indices, new meanings
	if err := reg.Publish(v1); err != nil {
		t.Fatal(err)
	}
	if err := reg.Publish(v2); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf, v1)
	_ = enc.Ref(0) // apple (v1)
	_ = enc.Ref(1) // banana (v1)
	if err := enc.SwitchTo(v2); err != nil {
		t.Fatal(err)
	}
	_ = enc.Ref(0) // banana (v2): same index as "apple" before the switch
	_ = enc.Ref(1) // cherry (v2)
	_ = enc.End()

	dec, err := NewDecoder(bytes.NewReader(buf.Bytes()), DecoderConfig{Registry: reg, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	got, err := decodeAll(t, dec)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"apple", "banana", "banana", "cherry"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnknownIndexIsNotEmptyString(t *testing.T) {
	reg := NewRegistry()
	d := frozenDict(t, 1, "") // index 0 is the (valid) empty string
	if err := reg.Publish(d); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf, d)
	_ = enc.Ref(0)
	// Append a hand-crafted reference to an index that does not exist.
	raw := append(buf.Bytes(), tagRef, 9)

	dec, err := NewDecoder(bytes.NewReader(raw), DecoderConfig{Registry: reg, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	s, err := dec.Next() // valid empty string
	if err != nil || s != "" {
		t.Fatalf("first Next = %q, %v; want \"\", nil", s, err)
	}
	if _, err := dec.Next(); !errors.Is(err, ErrUnknownIndex) {
		t.Fatalf("unknown index error = %v, want ErrUnknownIndex", err)
	}
}

func TestBudgetIncludesDictionaryAndStaging(t *testing.T) {
	reg := NewRegistry()
	d := frozenDict(t, 1, "hello") // 5 dictionary bytes
	if err := reg.Publish(d); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf, d)
	_ = enc.Ref(0)        // expands to 5 bytes
	_ = enc.Inline("hey") // 3 staging bytes
	_ = enc.End()
	// Total charge: 5 (dict) + 5 (ref expansion) + 3 (inline staging) = 13.

	for _, tc := range []struct {
		limit int64
		ok    bool
	}{
		{13, true},
		{12, false},
		{5, false}, // output-only budget ignores the dictionary: must fail
	} {
		dec, err := NewDecoder(bytes.NewReader(buf.Bytes()), DecoderConfig{
			Registry: reg, Version: 1, Budget: NewBudget(tc.limit),
		})
		if tc.limit < 5 {
			if !errors.Is(err, ErrBudgetExceeded) {
				t.Fatalf("limit %d: NewDecoder = %v, want ErrBudgetExceeded", tc.limit, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("limit %d: NewDecoder = %v", tc.limit, err)
		}
		_, err = decodeAll(t, dec)
		dec.Close()
		if tc.ok && err != nil {
			t.Fatalf("limit %d: %v", tc.limit, err)
		}
		if !tc.ok && !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("limit %d: %v, want ErrBudgetExceeded", tc.limit, err)
		}
	}
}

func TestHugeLengthRejectedBeforeAllocation(t *testing.T) {
	reg := NewRegistry()
	var buf bytes.Buffer
	buf.WriteByte(tagInline)
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], 1<<40) // absurd declared length
	buf.Write(tmp[:n])
	// No payload at all: the decoder must fail on the length check, not on
	// reading 1<<40 bytes.
	dec, err := NewDecoder(bytes.NewReader(buf.Bytes()), DecoderConfig{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Next(); !errors.Is(err, ErrLengthTooLarge) {
		t.Fatalf("Next = %v, want ErrLengthTooLarge", err)
	}

	// A legal-but-unaffordable length is rejected by the budget, still
	// before allocation.
	buf.Reset()
	buf.WriteByte(tagInline)
	n = binary.PutUvarint(tmp[:], 1<<20)
	buf.Write(tmp[:n])
	dec, err = NewDecoder(bytes.NewReader(buf.Bytes()), DecoderConfig{
		Registry: reg, Budget: NewBudget(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Next(); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("Next = %v, want ErrBudgetExceeded", err)
	}
}

func TestTruncatedAndMalformedStreams(t *testing.T) {
	reg := NewRegistry()
	// Inline frame declaring 5 bytes but carrying 2.
	truncated := []byte{tagInline, 5, 'a', 'b'}
	dec, err := NewDecoder(bytes.NewReader(truncated), DecoderConfig{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated Next = %v, want io.ErrUnexpectedEOF", err)
	}
	// Unknown tag.
	dec, err = NewDecoder(bytes.NewReader([]byte{0x7f}), DecoderConfig{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Next(); !errors.Is(err, ErrMalformed) {
		t.Fatalf("bad tag Next = %v, want ErrMalformed", err)
	}
	// Reference with no dictionary bound.
	dec, err = NewDecoder(bytes.NewReader([]byte{tagRef, 0}), DecoderConfig{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Next(); !errors.Is(err, ErrNoDictionary) {
		t.Fatalf("no-dict Next = %v, want ErrNoDictionary", err)
	}
}

func TestRecoveryFromCheckpoint(t *testing.T) {
	reg := NewRegistry()
	v1 := frozenDict(t, 1, "alpha", "beta")
	v2 := frozenDict(t, 2, "gamma")
	if err := reg.Publish(v1); err != nil {
		t.Fatal(err)
	}
	if err := reg.Publish(v2); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf, v1)
	want := []string{"alpha", "free-1", "beta"}
	for _, s := range want {
		_ = enc.String(s)
	}
	_ = enc.SwitchTo(v2)
	_ = enc.Ref(0) // gamma
	_ = enc.Inline("free-2")
	_ = enc.End()
	want = append(want, "gamma", "free-2")

	raw := buf.Bytes()
	// First session: decode two strings, then "crash".
	dec, err := NewDecoder(bytes.NewReader(raw), DecoderConfig{Registry: reg, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := 0; i < 2; i++ {
		s, err := dec.Next()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	cp := dec.Checkpoint()
	dec.Close()

	// Recovery: resume at the checkpoint with the version in force there.
	dec2, err := NewDecoder(bytes.NewReader(raw[cp.Offset:]), DecoderConfig{Registry: reg, Version: cp.Version})
	if err != nil {
		t.Fatal(err)
	}
	defer dec2.Close()
	rest, err := decodeAll(t, dec2)
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, rest...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCancelledBatchFailsDecode(t *testing.T) {
	reg := NewRegistry()
	d := frozenDict(t, 1, "a", "b")
	if err := reg.Publish(d); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := NewEncoder(&buf, d)
	_ = enc.Ref(0)
	_ = enc.Ref(1)
	_ = enc.End()

	b := reg.BeginBatch()
	dec, err := NewDecoder(bytes.NewReader(buf.Bytes()), DecoderConfig{Registry: reg, Batch: b, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Next(); err != nil {
		t.Fatal(err)
	}
	b.Cancel()
	if _, err := dec.Next(); !errors.Is(err, ErrLeaseReleased) {
		t.Fatalf("Next after Cancel = %v, want ErrLeaseReleased", err)
	}
	// Cancellation released the lease, so the generation is reclaimable.
	if err := reg.Reclaim(1); err != nil {
		t.Fatalf("Reclaim after Cancel = %v", err)
	}
}

func TestDecodeReclaimedVersionFails(t *testing.T) {
	reg := NewRegistry()
	d := frozenDict(t, 1, "a")
	if err := reg.Publish(d); err != nil {
		t.Fatal(err)
	}
	if err := reg.Reclaim(1); err != nil {
		t.Fatal(err)
	}
	_, err := NewDecoder(bytes.NewReader(nil), DecoderConfig{Registry: reg, Version: 1})
	if !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("NewDecoder = %v, want ErrUnknownVersion", err)
	}
}
