package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func TestDecodeLegalNestedStructure(t *testing.T) {
	stream := []byte{opProto, 4}
	stream = append(stream, opEmptyDict)
	stream = append(stream, shortUnicode("items")...)
	stream = append(stream, opEmptyList, opMark, opBinInt1, 1, opBinInt2, 2, 0)
	stream = append(stream, opAppends, opSetItem)
	stream = append(stream, shortUnicode("meta")...)
	stream = append(stream, opEmptyDict)
	stream = append(stream, shortUnicode("ok")...)
	stream = append(stream, opNone, opSetItem, opSetItem, opStop)

	got, err := Decode(stream)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	want := map[any]any{
		"items": []any{int64(1), int64(2)},
		"meta":  map[any]any{"ok": nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestDecodeMemoAndTuple(t *testing.T) {
	stream := []byte{opProto, 4}
	stream = append(stream, opEmptyList)
	stream = append(stream, opBinPut, 0)
	stream = append(stream, opBinGet, 0)
	stream = append(stream, opBinInt1, 7)
	stream = append(stream, opAppend)
	stream = append(stream, opStop)

	got, err := Decode(stream)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !reflect.DeepEqual(got, []any{int64(7)}) {
		t.Fatalf("Decode() = %#v, want []any{int64(7)}", got)
	}

	stream = []byte{opProto, 5}
	stream = append(stream, opMark)
	stream = append(stream, opNone, opBinInt, 0xff, 0xff, 0xff, 0xff)
	stream = append(stream, opTuple)
	stream = append(stream, opMemoize)
	stream = append(stream, opBinGet, 0)
	stream = append(stream, opStop)

	got, err = Decode(stream)
	if err != nil {
		t.Fatalf("Decode() tuple error = %v", err)
	}
	if !reflect.DeepEqual(got, Tuple{nil, int64(-1)}) {
		t.Fatalf("Decode() = %#v, want Tuple{nil, int64(-1)}", got)
	}
}

func TestDecodeUnicodeAndNoneKey(t *testing.T) {
	stream := []byte{opProto, 2, opEmptyDict, opNone, opEmptyList}
	stream = append(stream, opBinUnicode, 4, 0, 0, 0)
	stream = append(stream, 't', 'e', 'x', 't')
	stream = append(stream, opAppend, opSetItem, opStop)

	got, err := Decode(stream)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	want := map[any]any{nil: []any{"text"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestDecodeSetItems(t *testing.T) {
	stream := []byte{opProto, 4, opEmptyDict, opMark}
	stream = append(stream, shortUnicode("a")...)
	stream = append(stream, opBinInt1, 1)
	stream = append(stream, shortUnicode("b")...)
	stream = append(stream, opBinInt2, 2, 0)
	stream = append(stream, opSetItems, opStop)

	got, err := Decode(stream)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	want := map[any]any{"a": int64(1), "b": int64(2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestDecodeFramedStream(t *testing.T) {
	body := []byte{opEmptyList, opBinInt1, 9, opAppend, opStop}
	stream := []byte{opProto, 4, opFrame}
	stream = append(stream, 0, 0, 0, 0, 0, 0, 0, 0)
	for i, b := range []byte{byte(len(body)), 0, 0, 0, 0, 0, 0, 0} {
		stream[3+i] = b
	}
	stream = append(stream, body...)

	got, err := Decode(stream)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !reflect.DeepEqual(got, []any{int64(9)}) {
		t.Fatalf("Decode() = %#v, want []any{int64(9)}", got)
	}
}

func TestRejectsDangerousOpcodes(t *testing.T) {
	tests := []struct {
		name   string
		stream []byte
	}{
		{
			name:   "REDUCE",
			stream: append([]byte{opProto, 4, opNone, opEmptyTuple}, 0x52, opStop),
		},
		{
			name:   "GLOBAL",
			stream: []byte{opProto, 2, 0x63, 'o', 's', '\n', 's', 'y', 's', 't', 'e', 'm', '\n', opStop},
		},
		{
			name:   "STACK_GLOBAL",
			stream: []byte{opProto, 4, 0x93, opStop},
		},
		{
			name:   "BUILD",
			stream: []byte{opProto, 4, 0x62, opStop},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.stream)
			if !errors.Is(err, ErrOpcode) {
				t.Fatalf("Decode() error = %v, want ErrOpcode", err)
			}
		})
	}
}

func TestRejectsUndefinedMemo(t *testing.T) {
	_, err := Decode([]byte{opProto, 2, opBinGet, 0, opStop})
	if !errors.Is(err, ErrMemoUndefined) {
		t.Fatalf("Decode() error = %v, want ErrMemoUndefined", err)
	}
}

func TestRejectsExcessiveDepth(t *testing.T) {
	if _, err := Decode(nestedListStream(31)); err != nil {
		t.Fatalf("Decode() depth 32 error = %v", err)
	}

	_, err := Decode(nestedListStream(32))
	if !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("Decode() error = %v, want ErrDepthLimit", err)
	}
}

func TestRejectsTrailingBytesAfterStop(t *testing.T) {
	_, err := Decode([]byte{opProto, 2, opNone, opStop, opNone})
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("Decode() error = %v, want ErrTrailingData", err)
	}
}

func TestRejectsStackAndMemoLimits(t *testing.T) {
	stream := []byte{opProto, 4}
	for i := 0; i < MaxStack+1; i++ {
		stream = append(stream, opNone)
	}
	stream = append(stream, opStop)
	if _, err := Decode(stream); !errors.Is(err, ErrStackLimit) {
		t.Fatalf("Decode() stack error = %v, want ErrStackLimit", err)
	}

	stream = []byte{opProto, 4, opNone}
	for i := 0; i < MaxMemo; i++ {
		stream = append(stream, opMemoize)
	}
	stream = append(stream, opMemoize, opStop)
	if _, err := Decode(stream); !errors.Is(err, ErrMemoLimit) {
		t.Fatalf("Decode() memo error = %v, want ErrMemoLimit", err)
	}
}

func TestRejectsInvalidProtocolAndFrames(t *testing.T) {
	for _, stream := range [][]byte{
		{opProto, 1, opNone, opStop},
		{opProto, 6, opNone, opStop},
		{opNone, opStop},
	} {
		if _, err := Decode(stream); !errors.Is(err, ErrProtocol) && !errors.Is(err, ErrProtocolOrder) {
			t.Fatalf("Decode(%x) error = %v, want protocol error", stream, err)
		}
	}

	stream := []byte{opProto, 4, opFrame, 1, 0, 0, 0, 0, 0, 0, 0, opBinInt1, 7, opStop}
	if _, err := Decode(stream); !errors.Is(err, ErrFrame) {
		t.Fatalf("Decode() frame error = %v, want ErrFrame", err)
	}
}

func nestedListStream(outer int) []byte {
	stream := []byte{opProto, 4, opEmptyList, opBinPut, 0}
	for i := 1; i <= outer; i++ {
		stream = append(stream, opEmptyList, opBinPut, byte(i), opBinGet, byte(i-1), opAppend)
	}
	stream = append(stream, opStop)
	return stream
}

func shortUnicode(s string) []byte {
	out := []byte{opShortBinUnicode, byte(len(s))}
	return append(out, []byte(s)...)
}
