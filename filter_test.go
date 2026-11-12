package netsec

import (
	"bytes"
	"errors"
	"testing"
)

// tagBytes encodes a protobuf key (field number and wire type) as the
// varint that actually appears on the wire; a single byte only works for
// field numbers up to 15.
func tagBytes(fieldNum, wireType int) []byte {
	return varintBytes(uint64(fieldNum<<3 | wireType))
}

func varintBytes(value uint64) []byte {
	var buf []byte
	for value >= 0x80 {
		buf = append(buf, byte(value)|0x80)
		value >>= 7
	}
	return append(buf, byte(value))
}

func varintField(fieldNum int, value uint64) []byte {
	return append(tagBytes(fieldNum, wireVarint), varintBytes(value)...)
}

func bytesField(fieldNum int, payload []byte) []byte {
	out := tagBytes(fieldNum, wireBytes)
	out = append(out, varintBytes(uint64(len(payload)))...)
	return append(out, payload...)
}

func fixed32Field(fieldNum int) []byte {
	return append(tagBytes(fieldNum, wireFixed32), 1, 2, 3, 4)
}

func fixed64Field(fieldNum int) []byte {
	return append(tagBytes(fieldNum, wireFixed64), 1, 2, 3, 4, 5, 6, 7, 8)
}

func testKnown() map[int]struct{} {
	return map[int]struct{}{1: {}, 2: {}, 3: {}}
}

func concat(parts ...[]byte) []byte {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	out := make([]byte, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func TestNewFilterValidation(t *testing.T) {
	goodRanges := []Range{{Min: 100, Max: 200}}

	cases := []struct {
		name     string
		known    map[int]struct{}
		reserved []Range
		maxBytes int
		wantErr  error
	}{
		{name: "valid", known: testKnown(), reserved: goodRanges, maxBytes: 64},
		{name: "zero max means unlimited", known: nil, reserved: goodRanges, maxBytes: 0},
		{name: "negative max", maxBytes: -1, wantErr: ErrInvalidConfig},
		{name: "zero known field", known: map[int]struct{}{0: {}}, wantErr: ErrInvalidConfig},
		{name: "negative known field", known: map[int]struct{}{-7: {}}, wantErr: ErrInvalidConfig},
		{name: "oversized known field", known: map[int]struct{}{maxFieldNumber + 1: {}}, wantErr: ErrInvalidConfig},
		{name: "reserved zero min", reserved: []Range{{Min: 0, Max: 10}}, wantErr: ErrInvalidConfig},
		{name: "reserved oversized max", reserved: []Range{{Min: 1, Max: maxFieldNumber + 1}}, wantErr: ErrInvalidConfig},
		{name: "reserved inverted", reserved: []Range{{Min: 20, Max: 10}}, wantErr: ErrInvalidConfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := NewFilter(tc.known, tc.reserved, tc.maxBytes)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f == nil {
				t.Fatal("nil filter without error")
			}
		})
	}
}

func TestNewFilterCopiesInputs(t *testing.T) {
	known := map[int]struct{}{1: {}}
	ranges := []Range{{Min: 100, Max: 200}}

	f, err := NewFilter(known, ranges, 1)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	known[2] = struct{}{}
	ranges[0] = Range{Min: 1, Max: 1}

	// Field 2 must stay unknown after the caller mutates its map (so its
	// varint field exceeds the 1-byte limit), and the widened range must not
	// have been copied into the filter.
	msg := varintField(2, 42)
	if _, err := f.FilterBytes(msg); !errors.Is(err, ErrFieldTooLarge) {
		t.Fatalf("want ErrFieldTooLarge after caller mutation, got %v", err)
	}
}

func TestFilterBytes(t *testing.T) {
	nested := concat(
		varintField(1, 7),
		bytesField(2, []byte("nested-payload")),
	)

	cases := []struct {
		name     string
		msg      []byte
		want     []byte
		wantErr  error
		maxBytes int
	}{
		{
			name: "empty message",
			msg:  nil,
			want: []byte{},
		},
		{
			name: "known fields pass through untouched",
			msg: concat(
				varintField(1, 100),
				fixed64Field(2),
				bytesField(3, []byte("abc")),
			),
			want: concat(
				varintField(1, 100),
				fixed64Field(2),
				bytesField(3, []byte("abc")),
			),
		},
		{
			name: "benign new field retained in original order",
			msg:  concat(varintField(1, 9), varintField(50, 5), varintField(2, 3)),
			want: concat(varintField(1, 9), varintField(50, 5), varintField(2, 3)),
		},
		{
			name:    "unknown field at reserved range lower bound rejected",
			msg:     varintField(100, 1),
			wantErr: ErrReservedField,
		},
		{
			name:    "unknown field inside reserved range rejected",
			msg:     varintField(150, 1),
			wantErr: ErrReservedField,
		},
		{
			name:    "unknown field at reserved range upper bound rejected",
			msg:     varintField(200, 1),
			wantErr: ErrReservedField,
		},
		{
			name: "unknown just below reserved range retained",
			msg:  varintField(99, 1),
			want: varintField(99, 1),
		},
		{
			name: "unknown just above reserved range retained",
			msg:  varintField(201, 1),
			want: varintField(201, 1),
		},
		{
			name: "known field inside reserved range still passes",
			msg:  varintField(1, 1),
			want: varintField(1, 1),
		},
		{
			name:    "field number zero rejected",
			msg:     varintField(0, 1),
			wantErr: ErrFieldNumberZero,
		},
		{
			name:    "start group wire type rejected",
			msg:     tagBytes(50, wireStartGroup),
			wantErr: ErrGroupField,
		},
		{
			name:    "end group wire type rejected",
			msg:     tagBytes(50, wireEndGroup),
			wantErr: ErrGroupField,
		},
		{
			name:     "unknown field larger than byte limit rejected",
			msg:      bytesField(50, make([]byte, 10)),
			wantErr:  ErrFieldTooLarge,
			maxBytes: 4,
		},
		{
			name:     "unknown field exactly at byte limit retained",
			msg:      varintField(50, 1),
			want:     varintField(50, 1),
			maxBytes: 3,
		},
		{
			name: "oversized known field is exempt from limit",
			msg:  bytesField(1, make([]byte, 10)),
			want: bytesField(1, make([]byte, 10)),
		},
		{
			name: "nested length-delimited message framing retained",
			msg: concat(
				varintField(1, 1),
				bytesField(50, nested),
			),
			want: concat(
				varintField(1, 1),
				bytesField(50, nested),
			),
		},
		{
			name:    "nested length larger than remaining buffer rejected",
			msg:     append(tagBytes(50, wireBytes), varintBytes(64)...),
			wantErr: ErrMalformed,
		},
		{
			name:    "truncated varint rejected",
			msg:     append(tagBytes(50, wireVarint), 0x80),
			wantErr: ErrMalformed,
		},
		{
			name:    "truncated fixed64 rejected",
			msg:     append(tagBytes(50, wireFixed64), 1, 2),
			wantErr: ErrMalformed,
		},
		{
			name:    "truncated fixed32 rejected",
			msg:     append(tagBytes(50, wireFixed32), 1, 2),
			wantErr: ErrMalformed,
		},
		{
			name:    "truncated length varint rejected",
			msg:     append(tagBytes(50, wireBytes), 0x80),
			wantErr: ErrMalformed,
		},
		{
			name:    "non-canonical varint rejected",
			msg:     append(tagBytes(50, wireVarint), 0x80, 0x00),
			wantErr: ErrMalformed,
		},
		{
			name:    "unsupported wire type six rejected",
			msg:     tagBytes(50, 6),
			wantErr: ErrMalformed,
		},
		{
			name:    "field number beyond protobuf maximum rejected",
			msg:     append(varintBytes(uint64(maxFieldNumber+1)<<3), 1),
			wantErr: ErrMalformed,
		},
		{
			name: "varint tag overflowing 64 bits rejected",
			// 10-byte varint whose final byte carries more than one bit.
			msg:     append([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02}, 1),
			wantErr: ErrMalformed,
		},
		{
			name: "non-canonical ten-byte varint rejected",
			// Ten bytes with a zero final byte encode a value that fit in
			// fewer bytes.
			msg:     append([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, 1),
			wantErr: ErrMalformed,
		},
		{
			name: "length-delimited length overflowing int rejected",
			msg: append(
				tagBytes(50, wireBytes),
				0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01,
			),
			wantErr: ErrMalformed,
		},
		{
			name: "all valid wire types retained for benign unknown fields",
			msg: concat(
				varintField(50, 42),
				fixed64Field(51),
				bytesField(52, []byte("x")),
				fixed32Field(53),
			),
			want: concat(
				varintField(50, 42),
				fixed64Field(51),
				bytesField(52, []byte("x")),
				fixed32Field(53),
			),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Zero means no byte limit; size-boundary cases set their own.
			f, err := NewFilter(testKnown(), []Range{{Min: 100, Max: 200}}, tc.maxBytes)
			if err != nil {
				t.Fatalf("NewFilter: %v", err)
			}
			got, err := f.FilterBytes(tc.msg)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("want %x, got %x", tc.want, got)
			}
		})
	}
}

func TestFilterBytesIdempotent(t *testing.T) {
	f, err := NewFilter(testKnown(), []Range{{Min: 100, Max: 200}}, 0)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	msg := concat(
		varintField(1, 1),
		varintField(50, 2),
		bytesField(51, []byte("hello")),
	)
	first, err := f.FilterBytes(msg)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	second, err := f.FilterBytes(first)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("filtering is not idempotent: %x vs %x", first, second)
	}
	if !bytes.Equal(first, msg) {
		t.Fatalf("retained bytes do not match original: %x vs %x", first, msg)
	}
}

func TestFilterBytesDoesNotAliasInput(t *testing.T) {
	f, err := NewFilter(nil, nil, 0)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}

	msg := varintField(5, 1)
	got, err := f.FilterBytes(msg)
	if err != nil {
		t.Fatalf("FilterBytes: %v", err)
	}
	msg[0] = tagBytes(6, wireVarint)[0]
	if got[0] != tagBytes(5, wireVarint)[0] {
		t.Fatal("filtered output aliases the caller's input buffer")
	}
}

func TestFilterBytesNilFilter(t *testing.T) {
	var f *Filter
	if _, err := f.FilterBytes(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}

func TestMaxFieldBytesBoundary(t *testing.T) {
	// Field 50 needs a 2-byte tag, so the full field is 5 encoded bytes:
	// tag (2) + length varint (1) + 2 payload bytes.
	msg := bytesField(50, []byte{0xaa, 0xbb})

	f4 := mustFilter(t, 0, 4)
	if _, err := f4.FilterBytes(msg); !errors.Is(err, ErrFieldTooLarge) {
		t.Fatalf("limit 4: want ErrFieldTooLarge, got %v", err)
	}

	f5 := mustFilter(t, 0, 5)
	got, err := f5.FilterBytes(msg)
	if err != nil {
		t.Fatalf("limit 5: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("limit 5: want %x, got %x", msg, got)
	}
}

func mustFilter(t *testing.T, knownField, maxBytes int) *Filter {
	t.Helper()
	known := map[int]struct{}{}
	if knownField > 0 {
		known[knownField] = struct{}{}
	}
	f, err := NewFilter(known, []Range{{Min: 100, Max: 200}}, maxBytes)
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	return f
}
