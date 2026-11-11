package derstrict

import (
	"reflect"
	"testing"
)

type mutation struct {
	name       string
	data       []byte
	wantOp     string
	wantOffset int
	// asn1WantsAccepted records the observed encoding/asn1 acceptance scope,
	// so the two decoders can be compared item by item.
	asn1WantsAccepted bool
}

func TestMutationsRejectedAndCompared(t *testing.T) {
	valid := buildValidSample()

	mutations := []mutation{
		{
			name: "non-minimal-length-leading-zero",
			// INTEGER 1 re-encoded with length 82 00 01.
			data:              mutateLength(valid, 0x02, []byte{0x82, 0x00, 0x01}),
			wantOp:            "length",
			wantOffset:        indexOf(valid, []byte{0x02, 0x01, 0x01}) + 2,
			asn1WantsAccepted: true,
		},
		{
			name: "non-minimal-length-long-form-short-value",
			// NULL with length 81 00 instead of 00.
			data:              mutateLength(valid, 0x05, []byte{0x81, 0x00}),
			wantOp:            "length",
			wantOffset:        indexOf(valid, []byte{0x05, 0x00}) + 2,
			asn1WantsAccepted: true,
		},
		{
			name:              "indefinite-length",
			data:              indefiniteInteger(),
			wantOp:            "length",
			wantOffset:        1,
			asn1WantsAccepted: false,
		},
		{
			name: "non-canonical-boolean",
			// TRUE encoded as 01 01 01.
			data:              mutateAt(valid, indexOf(valid, []byte{0x01, 0x01, 0xff})+2, []byte{0x01}),
			wantOp:            "boolean",
			wantOffset:        indexOf(valid, []byte{0x01, 0x01, 0xff}) + 2,
			asn1WantsAccepted: true,
		},
		{
			name: "integer-redundant-leading-zero",
			// INTEGER 1 re-encoded as 02 02 00 01.
			data:              replaceVarLen(valid, indexOf(valid, []byte{0x02, 0x01, 0x01}), 3, derIntegerPaddingZero()),
			wantOp:            "integer",
			wantOffset:        indexOf(valid, []byte{0x02, 0x01, 0x01}) + 2,
			asn1WantsAccepted: true,
		},
		{
			name:              "integer-redundant-leading-ff",
			data:              tlv(0x02, []byte{0xff, 0xff}),
			wantOp:            "integer",
			wantOffset:        2,
			asn1WantsAccepted: true,
		},
		{
			name:              "unsorted-set-of",
			data:              unsortedSet(),
			wantOp:            "set",
			wantOffset:        unsortedSetViolationOffset(),
			asn1WantsAccepted: true,
		},
		{
			name:              "trailing-data",
			data:              append(append([]byte{}, valid...), 0x00),
			wantOp:            "decode",
			wantOffset:        len(valid),
			asn1WantsAccepted: true,
		},
		{
			name:              "truncated-data",
			data:              valid[:len(valid)-2],
			wantOp:            "length",
			wantOffset:        -1,
			asn1WantsAccepted: false,
		},
		{
			name:              "non-empty-null",
			data:              replaceVarLen(valid, indexOf(valid, []byte{0x05, 0x00}), 2, tlv(0x05, []byte{0x00})),
			wantOp:            "null",
			wantOffset:        indexOf(valid, []byte{0x05, 0x00}) + 2,
			asn1WantsAccepted: true,
		},
		{
			name: "non-minimal-high-tag-number",
			// context tag 0 encoded in high-tag-number form 1f 80 00.
			data:              nonMinimalHighTag(),
			wantOp:            "tag",
			wantOffset:        1,
			asn1WantsAccepted: false, // encoding/asn1 also rejects this syntax form.
		},
	}

	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			node, err := Decode(mutation.data)
			if err == nil {
				t.Fatalf("derstrict accepted malformed input, node=%+v", node)
			}
			strictErr, ok := err.(*Error)
			if !ok {
				t.Fatalf("error is not *Error: %T %v", err, err)
			}
			if strictErr.Op != mutation.wantOp {
				t.Fatalf("op = %q, want %q (err=%v)", strictErr.Op, mutation.wantOp, err)
			}
			if mutation.wantOffset >= 0 && strictErr.Offset != mutation.wantOffset {
				t.Fatalf("offset = %d, want %d (err=%v)", strictErr.Offset, mutation.wantOffset, err)
			}
			if got := OffsetOf(err); got != strictErr.Offset {
				t.Fatalf("OffsetOf = %d, want %d", got, strictErr.Offset)
			}

			// Item-by-item comparison against encoding/asn1's acceptance.
			if got := asn1Accepts(mutation.data); got != mutation.asn1WantsAccepted {
				t.Fatalf("encoding/asn1 acceptance = %v, want %v; update expectation",
					got, mutation.asn1WantsAccepted)
			}
			if mutation.name == "trailing-data" && asn1AcceptsWhole(mutation.data) {
				t.Fatalf("encoding/asn1 should leave trailing byte unconsumed")
			}
		})
	}
}

func TestMutationComparisonSummary(t *testing.T) {
	// Every BER-tolerant sample must be rejected by derstrict while
	// encoding/asn1 accepts it, demonstrating the strictness gap.
	valid := buildValidSample()
	samples := map[string][]byte{
		"non-minimal-length": mutateLength(valid, 0x02, []byte{0x82, 0x00, 0x01}),
		"non-canonical-bool": mutateAt(valid, indexOf(valid, []byte{0x01, 0x01, 0xff})+2, []byte{0x01}),
		"padding-integer":    replaceVarLen(valid, indexOf(valid, []byte{0x02, 0x01, 0x01}), 3, derIntegerPaddingZero()),
		"unsorted-set":       unsortedSet(),
		"trailing-data":      append(append([]byte{}, valid...), 0x00),
	}
	for name, data := range samples {
		if _, err := Decode(data); err == nil {
			t.Errorf("%s: derstrict unexpectedly accepted", name)
		}
		if !asn1Accepts(data) {
			t.Errorf("%s: expected encoding/asn1 to accept BER-tolerant form", name)
		}
	}
}

func indexOf(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if reflect.DeepEqual(haystack[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func mutateAt(data []byte, offset int, replacement []byte) []byte {
	out := append([]byte{}, data...)
	return append(out[:offset], append(append([]byte{}, replacement...), out[offset+1:]...)...)
}

func replaceAt(data []byte, offset int, replacement []byte) []byte {
	out := append([]byte{}, data...)
	return append(out[:offset], append(append([]byte{}, replacement...), out[offset+1:]...)...)
}

// replaceVarLen swaps oldSize bytes at offset for replacement of any length.
func replaceVarLen(data []byte, offset, oldSize int, replacement []byte) []byte {
	out := append([]byte{}, data...)
	return append(out[:offset], append(append([]byte{}, replacement...), out[offset+oldSize:]...)...)
}

// mutateLength replaces the length octets of the first TLV whose identifier
// byte equals targetIdentifier.
func mutateLength(data []byte, targetIdentifier byte, newLength []byte) []byte {
	offset := indexOf(data, []byte{targetIdentifier})
	lengthStart := offset + 1
	originalLengthOctets := 1
	if data[lengthStart]&0x80 != 0 {
		originalLengthOctets = 1 + int(data[lengthStart]&0x7f)
	}
	out := append([]byte{}, data...)
	return append(out[:lengthStart], append(append([]byte{}, newLength...),
		out[lengthStart+originalLengthOctets:]...)...)
}

// indefiniteInteger builds 02 80 01 00 00 (indefinite INTEGER + EOC), which is
// BER but not DER.
func indefiniteInteger() []byte {
	return []byte{0x02, 0x80, 0x01, 0x00, 0x00}
}

func derIntegerPaddingZero() []byte {
	return tlv(0x02, []byte{0x00, 0x01})
}

// unsortedSet builds SET OF { INTEGER 3, INTEGER 2 }, whose member encodings
// are in descending order.
func unsortedSet() []byte {
	content := append(derInteger(3), derInteger(2)...)
	return tlv(0x31, content)
}

func unsortedSetViolationOffset() int {
	// SET header is 31 06, then INTEGER 3 occupies three bytes.
	return 2 + len(derInteger(3))
}

// nonMinimalHighTag wraps an OCTET STRING in context tag 0 using the
// non-minimal high-tag-number identifier 1f 80 00.
func nonMinimalHighTag() []byte {
	content := tlv(0x04, []byte("hi"))
	header := []byte{0x9f, 0x80, 0x00}
	header = append(header, tlvHeader(0, len(content))[1:]...)
	return append(header, content...)
}
