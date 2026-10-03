package wirecodec

import (
	"bytes"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
)

func samplePerson() *Person {
	return &Person{
		Name:    "Ada Lovelace",
		ID:      1843,
		Balance: -9876543210,
		Scores:  []int32{100, -3, 0, 42, 1<<30 - 1, -1 << 30},
		Address: &Address{
			City: "Shanghai",
			Zip:  200000,
			Geo:  &Geo{Lat: 31.2304, Lng: 121.4737},
		},
		Tags:   []string{"engineer", "pioneer", "poet"},
		Avatar: []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a},
		Flags:  0xcafebabe,
		Ratio:  2.718281828459045,
	}
}

func TestNestedMessageRoundTrip(t *testing.T) {
	want := samplePerson()
	data := want.Marshal()

	var got Person
	if err := got.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, &got) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", &got, want)
	}
}

func TestEmptyMessageRoundTrip(t *testing.T) {
	data := (&Person{}).Marshal()
	if len(data) != 0 {
		t.Fatalf("zero message encoded to %d bytes, want 0", len(data))
	}
	var got Person
	if err := got.Unmarshal(data); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Person{}) {
		t.Fatalf("got %+v, want zero Person", got)
	}
}

func TestPackedRepeatedEncoding(t *testing.T) {
	p := &Person{Scores: []int32{1, -1, 300}}
	data := p.Marshal()

	// Field 4 must appear exactly once, as a single length-delimited block.
	r := NewReader(data)
	field, wt, err := r.Tag()
	if err != nil || field != 4 || wt != WireBytes {
		t.Fatalf("packed tag: field=%d wt=%d err=%v", field, wt, err)
	}
	raw, err := r.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// Packed payload: zigzag(1)=2, zigzag(-1)=1, zigzag(300)=600 -> 1+1+2 bytes.
	if len(raw) != 4 {
		t.Fatalf("packed payload len = %d, want 4", len(raw))
	}
	if !r.Done() {
		t.Fatal("packed field should be a single length-delimited block")
	}

	var got Person
	if err := got.Unmarshal(data); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Scores, p.Scores) {
		t.Fatalf("scores = %v, want %v", got.Scores, p.Scores)
	}
}

func TestUnpackedRepeatedAccepted(t *testing.T) {
	// Backward compatibility: a sender using unpacked encoding must decode
	// into the same slice.
	w := NewWriter()
	for _, s := range []int32{7, -8, 9} {
		w.Tag(4, WireVarint)
		w.ZigZag(int64(s))
	}
	var got Person
	if err := got.Unmarshal(w.Bytes()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Scores, []int32{7, -8, 9}) {
		t.Fatalf("scores = %v", got.Scores)
	}
}

func TestForwardCompatibilityUnknownFields(t *testing.T) {
	want := samplePerson()
	data := want.Marshal()

	// A "newer schema" appends fields of every wire type with unknown numbers.
	w := NewWriter()
	w.buf = append(w.buf, data...)
	w.Tag(50, WireVarint)
	w.Varint(999)
	w.Tag(51, WireFixed64)
	w.Fixed64(0x0102030405060708)
	w.Tag(52, WireBytes)
	w.WriteString("future field")
	w.Tag(53, WireFixed32)
	w.Fixed32(0xaabbccdd)
	// An unknown nested message containing its own unknown fields.
	inner := NewWriter()
	inner.Uint64Field(1, 1)
	inner.StringField(2, "nested future")
	w.MessageField(54, inner.Bytes())

	var got Person
	if err := got.Unmarshal(w.Bytes()); err != nil {
		t.Fatalf("decode with unknown fields: %v", err)
	}
	if !reflect.DeepEqual(want, &got) {
		t.Fatalf("forward compat mismatch:\n got %+v\nwant %+v", &got, want)
	}
}

func TestBackwardCompatibilityMissingFields(t *testing.T) {
	// An "old schema" stream carries only fields 1 and 2; decoding into the
	// full struct must leave the rest at zero values.
	w := NewWriter()
	w.StringField(1, "old client")
	w.Uint64Field(2, 7)

	var got Person
	if err := got.Unmarshal(w.Bytes()); err != nil {
		t.Fatal(err)
	}
	want := Person{Name: "old client", ID: 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTruncationSafetySweep(t *testing.T) {
	data := samplePerson().Marshal()
	for i := 0; i < len(data); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix len %d panicked: %v", i, r)
				}
			}()
			var p Person
			// A prefix may be a valid shorter message (cut exactly at a field
			// boundary) or an error (cut mid-field). Both are acceptable;
			// a panic or silent corruption is not.
			if err := p.Unmarshal(data[:i]); err == nil {
				var again Person
				if err2 := again.Unmarshal(p.Marshal()); err2 != nil {
					t.Fatalf("prefix len %d: re-marshal/unmarshal failed: %v", i, err2)
				}
			}
		}()
	}
}

func TestTruncationMustError(t *testing.T) {
	data := samplePerson().Marshal()
	// Cutting anywhere inside the final fixed64 field (tag + 8 bytes) must error.
	for i := len(data) - 8; i < len(data); i++ {
		var p Person
		if err := p.Unmarshal(data[:i]); err == nil {
			t.Fatalf("prefix len %d: expected truncation error, got nil", i)
		}
	}
}

func TestTruncatedNestedMessage(t *testing.T) {
	// Build a Person whose nested Address payload is cut short.
	addr := samplePerson().Address.Marshal()
	w := NewWriter()
	w.Tag(5, WireBytes)
	w.Varint(uint64(len(addr)))      // claim the full length
	w.WriteBytes(addr[:len(addr)-2]) // ...but deliver fewer bytes
	var p Person
	if err := p.Unmarshal(w.Bytes()); !errors.Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}

	// Nested message whose own tail is truncated.
	w2 := NewWriter()
	w2.MessageField(5, addr[:len(addr)-1])
	var p2 Person
	if err := p2.Unmarshal(w2.Bytes()); err == nil {
		t.Fatal("expected error for truncated nested message")
	}
}

func TestZeroCopyRestoration(t *testing.T) {
	orig := samplePerson()
	data := orig.Marshal()

	var got Person
	if err := got.Unmarshal(data); err != nil {
		t.Fatal(err)
	}
	// Avatar must alias the encoded buffer, not a copy.
	if len(got.Avatar) == 0 {
		t.Fatal("avatar missing")
	}
	// Mutating through the decoded slice must be visible in the source
	// buffer: proof that no copy was made.
	got.Avatar[0] ^= 0xff
	if data[avatarOffset(t, data)] != orig.Avatar[0]^0xff {
		t.Fatal("Avatar is a copy; want zero-copy alias of the input buffer")
	}
	if cap(got.Avatar) != len(got.Avatar) {
		t.Fatalf("Avatar cap = %d, want %d", cap(got.Avatar), len(got.Avatar))
	}
}

// avatarOffset locates the avatar payload inside the encoding by scanning.
func avatarOffset(t *testing.T, data []byte) int {
	t.Helper()
	r := NewReader(data)
	for !r.Done() {
		f, wt, err := r.Tag()
		if err != nil {
			t.Fatal(err)
		}
		if f == 7 {
			if _, err := r.Varint(); err != nil { // length prefix
				t.Fatal(err)
			}
			return r.Offset()
		}
		if err := r.Skip(wt); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("avatar field not found")
	return -1
}

func TestConcurrentDecode(t *testing.T) {
	want := samplePerson()
	data := want.Marshal()

	const goroutines = 32
	const iterations = 500
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				var p Person
				if err := p.Unmarshal(data); err != nil {
					errs <- err
					return
				}
				if !reflect.DeepEqual(&p, want) {
					errs <- errors.New("concurrent decode mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestConcurrentEncodeDecode(t *testing.T) {
	want := samplePerson()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				data := want.Marshal()
				var p Person
				if err := p.Unmarshal(data); err != nil {
					t.Error(err)
					return
				}
				if !reflect.DeepEqual(&p, want) {
					t.Error("round trip mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestNoLeak(t *testing.T) {
	data := samplePerson().Marshal()

	decode := func() {
		var p Person
		if err := p.Unmarshal(data); err != nil {
			t.Fatal(err)
		}
		// Keep the zero-copy field alive only within this scope.
		if len(p.Avatar) == 0 {
			t.Fatal("empty avatar")
		}
	}

	// Warm up so allocator caches reach steady state.
	for i := 0; i < 10000; i++ {
		decode()
	}
	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < 200000; i++ {
		decode()
	}
	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	const maxGrowth = 4 << 20 // 4 MiB slack for allocator arenas
	if growth > maxGrowth {
		t.Fatalf("heap grew by %d bytes after 200k decodes; possible leak", growth)
	}
	// The input buffer must remain the only large live object: decoded
	// Persons hold no copies of the payload.
	if after.HeapObjects > before.HeapObjects+1000 {
		t.Fatalf("live objects grew by %d; possible leak",
			after.HeapObjects-before.HeapObjects)
	}
}

func TestDeterministicEncoding(t *testing.T) {
	p := samplePerson()
	a := p.Marshal()
	b := p.Marshal()
	if !bytes.Equal(a, b) {
		t.Fatal("encoding is not deterministic")
	}
}
