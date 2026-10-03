package wire

import (
	"reflect"
	"sync"
	"testing"
)

type innerMsg struct {
	Name  string    `pb:"1,bytes"`
	Score int64     `pb:"2,zigzag"`
	Vals  []uint64  `pb:"3,varint"` // packed
	Next  *innerMsg `pb:"4,bytes"`
}

type outerMsg struct {
	ID     uint64    `pb:"1,varint"`
	Title  string    `pb:"2,bytes"`
	Deltas []int32   `pb:"3,zigzag"` // packed
	Inner  *innerMsg `pb:"4,bytes"`
	F32    uint32    `pb:"5,fixed32"`
	F64    uint64    `pb:"6,fixed64"`
	Active bool      `pb:"7,varint"`
	SFixed int32     `pb:"8,fixed32"`
	Ratio  float64   `pb:"9,fixed64"`
	Tags   []string  `pb:"10,bytes"`
}

func buildOuter() []byte {
	var inner []byte
	inner = AppendTag(inner, 1, BytesType)
	inner = AppendBytes(inner, []byte("leaf"))
	inner = AppendTag(inner, 2, VarintType)
	inner = AppendZigZag64(inner, -8192)
	var packedVals []byte
	for _, v := range []uint64{1, 300, 1 << 60} {
		packedVals = AppendVarint(packedVals, v)
	}
	inner = AppendTag(inner, 3, BytesType)
	inner = AppendBytes(inner, packedVals)
	// nested-next level
	var next []byte
	next = AppendTag(next, 1, BytesType)
	next = AppendBytes(next, []byte("deep"))
	inner = AppendTag(inner, 4, BytesType)
	inner = AppendBytes(inner, next)

	var out []byte
	out = AppendTag(out, 1, VarintType)
	out = AppendVarint(out, 9001)
	out = AppendTag(out, 2, BytesType)
	out = AppendBytes(out, []byte("batch"))
	var packedDeltas []byte
	for _, d := range []int32{-3, 0, 7, -128} {
		packedDeltas = AppendZigZag32(packedDeltas, d)
	}
	out = AppendTag(out, 3, BytesType)
	out = AppendBytes(out, packedDeltas)
	out = AppendTag(out, 4, BytesType)
	out = AppendBytes(out, inner)
	out = AppendTag(out, 5, Fixed32Type)
	out = AppendFixed32(out, 0xDEADBEEF)
	out = AppendTag(out, 6, Fixed64Type)
	out = AppendFixed64(out, 0x0123456789ABCDEF)
	out = AppendTag(out, 7, VarintType)
	out = AppendVarint(out, 1)
	out = AppendTag(out, 8, Fixed32Type)
	out = AppendFixed32(out, 0xFFFFFFF9) // sfixed32(-7)
	out = AppendTag(out, 9, Fixed64Type)
	out = AppendFixed64(out, 0x3FF8000000000000) // 1.5
	out = AppendTag(out, 10, BytesType)
	out = AppendBytes(out, []byte("alpha"))
	out = AppendTag(out, 10, BytesType)
	out = AppendBytes(out, []byte("beta"))
	// Unknown field 99 must be skipped.
	out = AppendTag(out, 99, VarintType)
	out = AppendVarint(out, 12345)
	return out
}

func TestUnmarshalNestedPacked(t *testing.T) {
	var got outerMsg
	if err := Unmarshal(buildOuter(), &got); err != nil {
		t.Fatal(err)
	}
	want := outerMsg{
		ID: 9001, Title: "batch",
		Deltas: []int32{-3, 0, 7, -128},
		Inner: &innerMsg{
			Name: "leaf", Score: -8192,
			Vals: []uint64{1, 300, 1 << 60},
			Next: &innerMsg{Name: "deep"},
		},
		F32: 0xDEADBEEF, F64: 0x0123456789ABCDEF,
		Active: true, SFixed: -7, Ratio: 1.5,
		Tags: []string{"alpha", "beta"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch:\n got %+v\n inner %+v/%+v\nwant %+v", got, got.Inner, got.Inner.Next, want)
	}
}

func TestUnmarshalTruncatedNested(t *testing.T) {
	full := buildOuter()
	guarded := 0
	for cut := 1; cut < len(full); cut++ {
		var victim outerMsg
		err := Unmarshal(full[:cut], &victim)
		if err != nil {
			guarded++ // cut landed inside a field: bounds guard fired
		}
	}
	if guarded == 0 {
		t.Fatal("bounds guard never triggered on truncated input")
	}
	// Cutting the final varint in half must always be caught.
	var victim outerMsg
	if err := Unmarshal(full[:len(full)-1], &victim); err == nil {
		t.Fatal("expected truncation error on final-byte cut")
	}
}

func TestUnmarshalErrors(t *testing.T) {
	var notStruct int
	if err := Unmarshal(nil, &notStruct); err != ErrInvalidTarget {
		t.Fatalf("got %v", err)
	}
	if err := Unmarshal(nil, nil); err != ErrInvalidTarget {
		t.Fatalf("got %v", err)
	}
	// Wire type mismatch: string field fed a varint.
	var buf []byte
	buf = AppendTag(buf, 2, VarintType)
	buf = AppendVarint(buf, 5)
	var victim outerMsg
	if err := Unmarshal(buf, &victim); err == nil {
		t.Fatal("expected type mismatch error")
	}
}

func TestUnmarshalConcurrent(t *testing.T) {
	blob := buildOuter()
	var want outerMsg
	if err := Unmarshal(blob, &want); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				var got outerMsg
				if err := Unmarshal(blob, &got); err != nil {
					errs <- err
					return
				}
				if !reflect.DeepEqual(got, want) {
					errs <- errMismatch
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

var errMismatch = errorString("concurrent decode mismatch")

type errorString string

func (e errorString) Error() string { return string(e) }
