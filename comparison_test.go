package derstrict

import "testing"

// TestAcceptanceMatrix pins down, item by item, how each DER violation is
// treated by derstrict versus encoding/asn1. derstrict rejects every row;
// encoding/asn1 tolerates the BER-style rows and only rejects the few syntax
// shapes its own parser already forbids.
func TestAcceptanceMatrix(t *testing.T) {
	valid := buildValidSample()
	rows := []struct {
		violation   string
		data        []byte
		asn1Accepts bool
		note        string
	}{
		{
			violation:   "non-minimal length (leading zero octet)",
			data:        mutateLength(valid, 0x02, []byte{0x82, 0x00, 0x01}),
			asn1Accepts: true,
			note:        "asn1.Unmarshal succeeds, leaving shifted bytes in rest",
		},
		{
			violation:   "indefinite length",
			data:        indefiniteInteger(),
			asn1Accepts: false,
			note:        "indefinite length is a syntax error for both",
		},
		{
			violation:   "non-canonical BOOLEAN (0x01)",
			data:        mutateAt(valid, indexOf(valid, []byte{0x01, 0x01, 0xff})+2, []byte{0x01}),
			asn1Accepts: true,
			note:        "any non-zero boolean content is treated as true",
		},
		{
			violation:   "INTEGER redundant leading 0x00",
			data:        replaceVarLen(valid, indexOf(valid, []byte{0x02, 0x01, 0x01}), 3, derIntegerPaddingZero()),
			asn1Accepts: true,
			note:        "padding is not rejected when parsing into RawValue",
		},
		{
			violation:   "unsorted SET OF",
			data:        unsortedSet(),
			asn1Accepts: true,
			note:        "no SET OF ordering check exists in encoding/asn1",
		},
		{
			violation:   "trailing data",
			data:        append(append([]byte{}, valid...), 0x00),
			asn1Accepts: true,
			note:        "trailing byte is returned through rest, not an error",
		},
		{
			violation:   "non-minimal high-tag-number encoding",
			data:        nonMinimalHighTag(),
			asn1Accepts: false,
			note:        "both decoders reject the non-minimal base-128 tag",
		},
	}

	for _, row := range rows {
		t.Run(row.violation, func(t *testing.T) {
			if _, err := Decode(row.data); err == nil {
				t.Fatalf("derstrict accepted %s", row.violation)
			}
			if got := asn1Accepts(row.data); got != row.asn1Accepts {
				t.Fatalf("encoding/asn1 acceptance = %v, want %v (%s)", got, row.asn1Accepts, row.note)
			}
		})
	}
}
