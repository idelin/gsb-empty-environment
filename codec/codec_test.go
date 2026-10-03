package codec_test

import (
	"bytes"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"pbcodec/codec"
	"pbcodec/wire"
)

// ---- schema used across tests ----

type Geo struct {
	Lat codec.SFixed64 `pb:"1"`
	Lng codec.SFixed64 `pb:"2"`
}

type Address struct {
	City string `pb:"1"`
	Zip  string `pb:"2"`
	Geo  *Geo   `pb:"3"`
}

type Person struct {
	codec.UnknownFields
	Name    string         `pb:"1"`
	Age     int32          `pb:"2"`
	Scores  []int32        `pb:"3"` // packed by default
	Tags    []string       `pb:"4"`
	Addr    *Address       `pb:"5"`
	Balance codec.SInt64   `pb:"6"`
	ID      codec.Fixed64  `pb:"7"`
	Data    []byte         `pb:"8"`
	Vip     bool           `pb:"9"`
	Weights []codec.SInt32 `pb:"10"` // packed zigzag
	Raw     []uint64       `pb:"11,rep"`
	Prev    []*Address     `pb:"12"`
}

// PersonV1 is an older schema: a strict subset of Person's fields.
type PersonV1 struct {
	codec.UnknownFields
	Name string `pb:"1"`
	Age  int32  `pb:"2"`
}

func samplePerson() *Person {
	return &Person{
		Name:    "Ada Lovelace",
		Age:     36,
		Scores:  []int32{1, -2, 300, 1 << 20},
		Tags:    []string{"math", "engines", "poetry"},
		Addr:    &Address{City: "London", Zip: "NW1", Geo: &Geo{Lat: 515283750, Lng: -1417956}},
		Balance: -9876543210,
		ID:      0xdeadbeefcafebabe,
		Data:    []byte{0x00, 0xff, 0x7f, 0x80},
		Vip:     true,
		Weights: []codec.SInt32{-1, 0, 1, 2147483647, -2147483648},
		Raw:     []uint64{0, 1, 1 << 60},
		Prev: []*Address{
			{City: "Paris", Zip: "75001"},
			{City: "Rome", Zip: "00100", Geo: &Geo{Lat: 419026110, Lng: 124765899}},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	want := samplePerson()
	enc, err := codec.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Person
	if err := codec.Unmarshal(enc, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, &got) {
		t.Fatalf("round trip mismatch:\nwant %+v\n got %+v", want, &got)
	}
	// Deterministic encoding: marshal again, byte-identical.
	enc2, _ := codec.Marshal(want)
	if !bytes.Equal(enc, enc2) {
		t.Fatal("marshal is not deterministic")
	}
}

func TestPackedRepeatedEncoding(t *testing.T) {
	enc, err := codec.Marshal(&Person{Scores: []int32{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	// Field 3 must appear exactly once, as length-delimited (packed).
	r := wire.NewReader(enc)
	n, wt, err := r.Tag()
	if err != nil || n != 3 || wt != wire.WireBytes {
		t.Fatalf("packed field: got %d/%d err %v", n, wt, err)
	}
	payload, _ := r.Bytes()
	if !r.Done() {
		t.Fatal("packed field should be the only record")
	}
	if want := []byte{1, 2, 3}; !bytes.Equal(payload, want) {
		t.Fatalf("packed payload = %x, want %x", payload, want)
	}
}

func TestPackedAndUnpackedBothAccepted(t *testing.T) {
	// Hand-build the unpacked (one tag per element) form of field 3.
	w := wire.NewWriter()
	for _, v := range []uint64{1, 2, 3} {
		w.Tag(3, wire.WireVarint)
		w.Varint(v)
	}
	// Append a packed record for the same field; decoders must merge both.
	w.Tag(3, wire.WireBytes)
	w.Bytes([]byte{4, 5})
	var p Person
	if err := codec.Unmarshal(w.Out(), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := []int32{1, 2, 3, 4, 5}; !reflect.DeepEqual(p.Scores, want) {
		t.Fatalf("scores = %v, want %v", p.Scores, want)
	}
	// The ",rep" option forces unpacked output.
	enc, err := codec.Marshal(&Person{Raw: []uint64{9, 9}})
	if err != nil {
		t.Fatal(err)
	}
	r := wire.NewReader(enc)
	count := 0
	for !r.Done() {
		n, wt, err := r.Tag()
		if err != nil {
			t.Fatal(err)
		}
		if n != 11 || wt != wire.WireVarint {
			t.Fatalf("rep field: got %d/%d", n, wt)
		}
		count++
		if err := r.Skip(wt); err != nil {
			t.Fatal(err)
		}
	}
	if count != 2 {
		t.Fatalf("rep field emitted %d records, want 2", count)
	}
}

func TestTruncationSweep(t *testing.T) {
	enc, err := codec.Marshal(samplePerson())
	if err != nil {
		t.Fatal(err)
	}
	errs := 0
	for i := 0; i < len(enc); i++ {
		var p Person
		// Must never panic; a strict prefix either decodes to a shorter
		// valid message (cut exactly on a field boundary) or errors.
		if err := codec.Unmarshal(enc[:i], &p); err != nil {
			errs++
		}
	}
	if errs == 0 {
		t.Fatal("no truncation was detected")
	}
	// Cutting the final byte of the last (length-delimited) field must error.
	var p Person
	if err := codec.Unmarshal(enc[:len(enc)-1], &p); !errors.Is(err, wire.ErrTruncated) {
		t.Fatalf("last-byte cut: want ErrTruncated, got %v", err)
	}
	// Full input still decodes.
	if err := codec.Unmarshal(enc, &p); err != nil {
		t.Fatalf("full input: %v", err)
	}
}

func TestTruncatedInsidePackedPayload(t *testing.T) {
	w := wire.NewWriter()
	w.Tag(3, wire.WireBytes)
	w.Bytes([]byte{0x80}) // packed payload holding a truncated varint
	var p Person
	if err := codec.Unmarshal(w.Out(), &p); !errors.Is(err, wire.ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestForwardCompatibility(t *testing.T) {
	want := samplePerson()
	enc, err := codec.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	// Old schema decodes new data: unknown fields are captured, not lost.
	var old PersonV1
	if err := codec.Unmarshal(enc, &old); err != nil {
		t.Fatalf("unmarshal into old schema: %v", err)
	}
	if old.Name != want.Name || old.Age != want.Age {
		t.Fatalf("known fields lost: %+v", old)
	}
	if len(old.UnknownFields.Raw) == 0 {
		t.Fatal("unknown fields were not captured")
	}
	// Re-marshaling the old struct preserves everything (lossless relay).
	reenc, err := codec.Marshal(&old)
	if err != nil {
		t.Fatal(err)
	}
	var back Person
	if err := codec.Unmarshal(reenc, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, &back) {
		t.Fatalf("forward-compat round trip mismatch:\nwant %+v\n got %+v", want, &back)
	}
}

func TestBackwardCompatibility(t *testing.T) {
	old := &PersonV1{Name: "Grace", Age: 85}
	enc, err := codec.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	// New schema decodes old data: absent fields stay zero-valued.
	var p Person
	if err := codec.Unmarshal(enc, &p); err != nil {
		t.Fatalf("unmarshal into new schema: %v", err)
	}
	if p.Name != "Grace" || p.Age != 85 {
		t.Fatalf("known fields lost: %+v", p)
	}
	if p.Addr != nil || p.Scores != nil || p.Balance != 0 || p.Vip {
		t.Fatalf("absent fields not zero: %+v", p)
	}
}

func TestZeroCopy(t *testing.T) {
	enc, err := codec.Marshal(samplePerson())
	if err != nil {
		t.Fatal(err)
	}
	var p Person
	if err := codec.Unmarshal(enc, &p); err != nil {
		t.Fatal(err)
	}
	lo := uintptr(unsafe.Pointer(&enc[0]))
	hi := lo + uintptr(len(enc))
	inBuf := func(ptr uintptr) bool { return ptr >= lo && ptr < hi }
	// []byte field aliases the input buffer.
	if !inBuf(uintptr(unsafe.Pointer(&p.Data[0]))) {
		t.Fatal("[]byte field was copied")
	}
	// string field aliases the input buffer.
	sh := (*reflect.StringHeader)(unsafe.Pointer(&p.Name))
	if !inBuf(sh.Data) {
		t.Fatal("string field was copied")
	}
	// Nested message strings are zero-copy too.
	sh2 := (*reflect.StringHeader)(unsafe.Pointer(&p.Addr.City))
	if !inBuf(sh2.Data) {
		t.Fatal("nested string field was copied")
	}
}

func TestNestingDepthLimit(t *testing.T) {
	type Node struct {
		Next *Node `pb:"1"`
	}
	root := &Node{}
	cur := root
	for i := 0; i < 300; i++ {
		cur.Next = &Node{}
		cur = cur.Next
	}
	enc, err := codec.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	var out Node
	if err := codec.Unmarshal(enc, &out); !errors.Is(err, codec.ErrMaxDepth) {
		t.Fatalf("want ErrMaxDepth, got %v", err)
	}
}

func TestHostileInputs(t *testing.T) {
	cases := map[string][]byte{
		"empty tag":          {},
		"truncated tag":      {0x80},
		"overflow tag":       {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		"field zero":         {0x00, 0x01},
		"reserved wire 3":    {0x0b, 0x01},
		"reserved wire 7":    {0x0f, 0x01},
		"length bomb":        {0x42, 0xff, 0xff, 0xff, 0xff, 0x0f}, // field 8, ~4GiB len
		"truncated fixed64":  {0x39, 0x01, 0x02},                   // field 7, 2 of 8 bytes
		"wire type mismatch": {0x08, 0x01},                         // varint for string field 1
	}
	for name, in := range cases {
		var p Person
		if err := codec.Unmarshal(in, &p); err == nil && name != "empty tag" {
			t.Fatalf("%s: expected error, got none (%+v)", name, p)
		}
	}
}

func TestConcurrentDecodeEncode(t *testing.T) {
	want := samplePerson()
	enc, err := codec.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	const iters = 200
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				var p Person
				if err := codec.Unmarshal(enc, &p); err != nil {
					errCh <- err
					return
				}
				if p.Name != want.Name || p.Addr.Geo.Lng != want.Addr.Geo.Lng {
					errCh <- errors.New("decoded value mismatch")
					return
				}
				if _, err := codec.Marshal(&p); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	enc, _ := codec.Marshal(samplePerson())
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var p Person
			_ = codec.Unmarshal(enc, &p)
		}()
	}
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestEmptyAndNilMessages(t *testing.T) {
	var p Person
	if err := codec.Unmarshal(nil, &p); err != nil {
		t.Fatalf("empty input: %v", err)
	}
	enc, err := codec.Marshal(&Person{})
	if err != nil {
		t.Fatal(err)
	}
	// Zero values are omitted (proto3-style), so the encoding is empty.
	if len(enc) != 0 {
		t.Fatalf("empty message encoded to %x", enc)
	}
	var q Person
	if err := codec.Unmarshal(enc, &q); err != nil {
		t.Fatal(err)
	}
	if err := codec.Unmarshal(enc, Person{}); !errors.Is(err, codec.ErrNotMessage) {
		t.Fatalf("non-pointer target: want ErrNotMessage, got %v", err)
	}
}

func FuzzUnmarshal(f *testing.F) {
	enc, _ := codec.Marshal(samplePerson())
	f.Add(enc)
	f.Add([]byte{0x08, 0x96, 0x01})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		var p Person
		_ = codec.Unmarshal(data, &p) // must never panic
	})
}

func BenchmarkUnmarshal(b *testing.B) {
	enc, _ := codec.Marshal(samplePerson())
	b.ReportAllocs()
	b.SetBytes(int64(len(enc)))
	for i := 0; i < b.N; i++ {
		var p Person
		if err := codec.Unmarshal(enc, &p); err != nil {
			b.Fatal(err)
		}
	}
}
