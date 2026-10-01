package msg

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"protowire/wire"
)

func sample() *Outer {
	m := AcquireOuter()
	m.Big = -1 << 60
	m.Delta = -987654
	m.F64 = 0x0102030405060708
	m.F32 = 0xAABBCCDD
	m.Label = append(m.Label, "root-label"...)
	in := AcquireInner()
	in.ID = 42
	in.Name = append(in.Name, "nested"...)
	m.Inner = in
	m.Nums = append(m.Nums, 0, 1, 127, 128, 300, 1<<20, -1)
	for i := 0; i < 3; i++ {
		it := AcquireInner()
		it.ID = int32(i)
		it.Name = append(it.Name, byte('a'+i), byte('a'+i))
		m.Items = append(m.Items, it)
	}
	return m
}

func encode(t *testing.T, m *Outer) []byte {
	t.Helper()
	w := wire.AcquireWriter()
	EncodeOuter(w, m)
	data := append([]byte(nil), w.Bytes()...)
	w.Release()
	return data
}

func decode(t *testing.T, data []byte) *Outer {
	t.Helper()
	r := wire.AcquireReader(data)
	m := AcquireOuter()
	err := DecodeOuter(r, m)
	r.Release()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func assertEqual(t *testing.T, got, want *Outer) {
	t.Helper()
	if got.Big != want.Big || got.Delta != want.Delta || got.F64 != want.F64 || got.F32 != want.F32 {
		t.Fatalf("scalars: got %+v want %+v", got, want)
	}
	if !bytes.Equal(got.Label, want.Label) {
		t.Fatalf("label: %q want %q", got.Label, want.Label)
	}
	if got.Inner == nil || got.Inner.ID != want.Inner.ID || !bytes.Equal(got.Inner.Name, want.Inner.Name) {
		t.Fatalf("inner: got %+v want %+v", got.Inner, want.Inner)
	}
	if len(got.Nums) != len(want.Nums) {
		t.Fatalf("nums len: %d want %d", len(got.Nums), len(want.Nums))
	}
	for i := range got.Nums {
		if got.Nums[i] != want.Nums[i] {
			t.Fatalf("nums[%d]: %d want %d", i, got.Nums[i], want.Nums[i])
		}
	}
	if len(got.Items) != len(want.Items) {
		t.Fatalf("items len: %d want %d", len(got.Items), len(want.Items))
	}
	for i := range got.Items {
		if got.Items[i].ID != want.Items[i].ID || !bytes.Equal(got.Items[i].Name, want.Items[i].Name) {
			t.Fatalf("items[%d]: got %+v want %+v", i, got.Items[i], want.Items[i])
		}
	}
}

func TestRoundTrip(t *testing.T) {
	src := sample()
	data := encode(t, src)
	got := decode(t, data)
	assertEqual(t, got, src)
	got.Release()
	src.Release()
}

func TestTruncationSweep(t *testing.T) {
	src := sample()
	data := encode(t, src)
	src.Release()

	sentinel := func(err error) bool {
		return errors.Is(err, wire.ErrTruncated) ||
			errors.Is(err, wire.ErrOverflow) ||
			errors.Is(err, wire.ErrInvalidWireType) ||
			errors.Is(err, wire.ErrInvalidTag)
	}

	errorsSeen := 0
	for i := 0; i < len(data); i++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("prefix %d panicked: %v", i, rec)
				}
			}()
			r := wire.AcquireReader(data[:i])
			m := AcquireOuter()
			err := DecodeOuter(r, m)
			m.Release()
			r.Release()
			if err != nil {
				if !sentinel(err) {
					t.Fatalf("prefix %d: unexpected error type %v", i, err)
				}
				errorsSeen++
			}
			// A nil error is legal only when the prefix happens to end
			// exactly on a field boundary (a valid shorter message).
		}()
	}
	if errorsSeen == 0 {
		t.Fatal("expected at least some truncated prefixes to fail")
	}
	t.Logf("prefixes: %d, rejected: %d, valid-on-boundary: %d", len(data), errorsSeen, len(data)-errorsSeen)
}

func TestTruncationSpecific(t *testing.T) {
	src := sample()
	data := encode(t, src)
	src.Release()

	// Cut the stream at the final byte of the packed nums payload and
	// inside the nested message: both must error, not silently succeed.
	cuts := []int{len(data) - 1}
	// Find the nested message cut: locate field 6 tag byte.
	for i, b := range data {
		if b == byte(fieldOuterInner<<3|wire.WireBytes) {
			cuts = append(cuts, i+2) // tag + length, zero payload bytes of inner
			break
		}
	}
	for _, c := range cuts {
		r := wire.AcquireReader(data[:c])
		m := AcquireOuter()
		err := DecodeOuter(r, m)
		m.Release()
		r.Release()
		if err == nil {
			t.Fatalf("cut at %d: expected error, got clean decode", c)
		}
	}
}

func TestCorruptInputs(t *testing.T) {
	// Length prefix claims a huge payload: must fail without allocating.
	data := []byte{byte(fieldOuterLabel<<3 | wire.WireBytes), 0xff, 0xff, 0xff, 0xff, 0x0f}
	r := wire.AcquireReader(data)
	m := AcquireOuter()
	err := DecodeOuter(r, m)
	m.Release()
	r.Release()
	if !errors.Is(err, wire.ErrTruncated) {
		t.Fatalf("huge length: got %v, want ErrTruncated", err)
	}

	// Wrong wire type for a known field.
	data = []byte{byte(fieldOuterBig<<3 | wire.WireFixed32), 0, 0, 0, 0}
	r = wire.AcquireReader(data)
	m = AcquireOuter()
	err = DecodeOuter(r, m)
	m.Release()
	r.Release()
	if !errors.Is(err, wire.ErrInvalidWireType) {
		t.Fatalf("wrong wire type: got %v, want ErrInvalidWireType", err)
	}

	// Packed nums payload with a truncated varint inside.
	data = []byte{byte(fieldOuterNums<<3 | wire.WireBytes), 0x02, 0x80, 0x80}
	r = wire.AcquireReader(data)
	m = AcquireOuter()
	err = DecodeOuter(r, m)
	m.Release()
	r.Release()
	if !errors.Is(err, wire.ErrTruncated) {
		t.Fatalf("packed truncated: got %v, want ErrTruncated", err)
	}
}

func TestUnknownFieldsSkipped(t *testing.T) {
	src := sample()
	data := encode(t, src)
	src.Release()

	// Append unknown fields of every wire type; decode must still succeed.
	var extra []byte
	extra = wire.AppendTag(extra, 100, wire.WireVarint)
	extra = wire.AppendVarint(extra, 1)
	extra = wire.AppendTag(extra, 101, wire.WireFixed64)
	extra = wire.AppendFixed64(extra, 2)
	extra = wire.AppendTag(extra, 102, wire.WireBytes)
	extra = wire.AppendVarint(extra, 3)
	extra = append(extra, "xyz"...)
	extra = wire.AppendTag(extra, 103, wire.WireFixed32)
	extra = wire.AppendFixed32(extra, 4)

	got := decode(t, append(data, extra...))
	if got.Big == 0 && got.Inner == nil {
		t.Fatal("decode with unknown fields lost data")
	}
	got.Release()
}

func TestUnpackedRepeatedAccepted(t *testing.T) {
	// nums as unpacked varints (legal per spec even for packed fields).
	var data []byte
	for _, n := range []int32{5, 6, 7} {
		data = wire.AppendTag(data, fieldOuterNums, wire.WireVarint)
		data = wire.AppendVarint(data, uint64(uint32(n)))
	}
	got := decode(t, data)
	defer got.Release()
	if len(got.Nums) != 3 || got.Nums[0] != 5 || got.Nums[1] != 6 || got.Nums[2] != 7 {
		t.Fatalf("unpacked nums: %v", got.Nums)
	}
}

func TestZeroAllocDecode(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates on sync.Pool paths; run without -race")
	}
	src := sample()
	data := encode(t, src)
	src.Release()

	// Warm the pools.
	for i := 0; i < 200; i++ {
		r := wire.AcquireReader(data)
		m := AcquireOuter()
		if err := DecodeOuter(r, m); err != nil {
			t.Fatal(err)
		}
		m.Release()
		r.Release()
	}

	allocs := testing.AllocsPerRun(500, func() {
		r := wire.AcquireReader(data)
		m := AcquireOuter()
		if err := DecodeOuter(r, m); err != nil {
			t.Fatal(err)
		}
		m.Release()
		r.Release()
	})
	if allocs != 0 {
		t.Fatalf("decode allocated %v times per run, want 0", allocs)
	}
}

func TestNoLeaks(t *testing.T) {
	before := Stats()
	const n = 1000
	for i := 0; i < n; i++ {
		m := sample()
		data := encode(t, m)
		m.Release()
		got := decode(t, data)
		got.Release()
	}
	after := Stats()
	if after.InnerAcquired-before.InnerAcquired != after.InnerReleased-before.InnerReleased {
		t.Fatalf("inner leak: acquired %d released %d",
			after.InnerAcquired-before.InnerAcquired, after.InnerReleased-before.InnerReleased)
	}
	if after.OuterAcquired-before.OuterAcquired != after.OuterReleased-before.OuterReleased {
		t.Fatalf("outer leak: acquired %d released %d",
			after.OuterAcquired-before.OuterAcquired, after.OuterReleased-before.OuterReleased)
	}
}

func TestConcurrentDecode(t *testing.T) {
	src := sample()
	data := encode(t, src)
	src.Release()

	before := Stats()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				r := wire.AcquireReader(data)
				m := AcquireOuter()
				if err := DecodeOuter(r, m); err != nil {
					t.Error(err)
					return
				}
				if len(m.Items) != 3 || m.Inner == nil {
					t.Error("corrupt decode under concurrency")
					return
				}
				m.Release()
				r.Release()
			}
		}()
	}
	wg.Wait()

	after := Stats()
	if after.InnerAcquired != after.InnerReleased || after.OuterAcquired != after.OuterReleased {
		t.Fatalf("leak under concurrency: before=%+v after=%+v", before, after)
	}
}
