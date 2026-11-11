package derstrict

import (
	"encoding/asn1"
	"math/big"
	"reflect"
	"testing"
)

// tlv builds a canonical DER TLV from an identifier and content.
func tlv(identifier byte, content []byte) []byte {
	return append(tlvHeader(identifier, len(content)), content...)
}

// tlvHeader builds canonical identifier plus minimum-length octets.
func tlvHeader(identifier byte, length int) []byte {
	header := []byte{identifier}
	switch {
	case length < 0x80:
		header = append(header, byte(length))
	default:
		var lengthOctets []byte
		for length > 0 {
			lengthOctets = append([]byte{byte(length & 0xff)}, lengthOctets...)
			length >>= 8
		}
		header = append(header, byte(0x80|len(lengthOctets)))
		header = append(header, lengthOctets...)
	}
	return header
}

func derInteger(value int64) []byte {
	return tlv(0x02, big.NewInt(value).Bytes())
}

func derBoolean(value bool) []byte {
	if value {
		return tlv(0x01, []byte{0xff})
	}
	return tlv(0x01, []byte{0x00})
}

// asn1Accepts reports whether encoding/asn1 parses data as one RawValue
// without error. Leftover bytes remain in rest and do not make the call fail.
func asn1Accepts(data []byte) bool {
	var raw asn1.RawValue
	_, err := asn1.Unmarshal(data, &raw)
	return err == nil
}

// asn1AcceptsWhole reports whether encoding/asn1 parses and fully consumes data.
func asn1AcceptsWhole(data []byte) bool {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(data, &raw)
	return err == nil && len(rest) == 0
}

// buildValidSample returns a nested, well-formed DER structure used as the
// baseline for every mutation:
//
//	SEQUENCE {
//	  INTEGER 1
//	  BOOLEAN TRUE
//	  SET OF { INTEGER 2, INTEGER 3 }
//	  NULL
//	  context[0] { OCTET STRING "hi" }
//	}
func buildValidSample() []byte {
	innerSet := tlv(0x31,
		append(derInteger(2), derInteger(3)...))
	explicitZero := tlv(0xa0, tlv(0x04, []byte("hi")))
	content := append(derInteger(1), derBoolean(true)...)
	content = append(content, innerSet...)
	content = append(content, tlv(0x05, nil)...)
	content = append(content, explicitZero...)
	return tlv(0x30, content)
}

func TestDecodeValidSample(t *testing.T) {
	data := buildValidSample()
	node, err := Decode(data)
	if err != nil {
		t.Fatalf("valid sample rejected: %v", err)
	}
	if node.Tag != TagSequence || len(node.Children) != 5 {
		t.Fatalf("unexpected top-level node: %+v", node)
	}
	booleanValue, err := node.Children[1].Bool()
	if err != nil || !booleanValue {
		t.Fatalf("BOOLEAN decode failed: value=%v err=%v", booleanValue, err)
	}
	setNode := node.Children[2]
	if setNode.Tag != TagSet || len(setNode.Children) != 2 {
		t.Fatalf("SET OF decode failed: %+v", setNode)
	}
	if integer := node.Children[0].Content; !reflect.DeepEqual(integer, []byte{0x01}) {
		t.Fatalf("INTEGER content mismatch: %x", integer)
	}
	if OffsetOf(err) != -1 {
		t.Fatalf("nil error should report offset -1")
	}
}

func TestIntegerValues(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    string
	}{
		{"zero", []byte{0x00}, "0"},
		{"positive", []byte{0x7f}, "127"},
		{"positive-two-octets", []byte{0x00, 0x80}, "128"},
		{"negative-one", []byte{0xff}, "-1"},
		{"negative-128", []byte{0x80}, "-128"},
		{"negative-129", []byte{0xff, 0x7f}, "-129"},
		{"big-negative", []byte{0x80, 0x00, 0x00}, "-8388608"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tlv(0x02, tc.content)
			node, err := Decode(data)
			if err != nil {
				t.Fatalf("canonical integer rejected: %v", err)
			}
			got, err := node.Integer()
			if err != nil {
				t.Fatalf("Integer() failed: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("integer = %s, want %s", got.String(), tc.want)
			}
		})
	}
}

func TestSetOrderingAccepted(t *testing.T) {
	ordered := tlv(0x31, append(derInteger(1), derInteger(2)...))
	if _, err := Decode(ordered); err != nil {
		t.Fatalf("ordered SET OF rejected: %v", err)
	}
}

func TestValidHighTagNumber(t *testing.T) {
	// context tag 31 in minimal high-tag-number form: 9f 1f.
	content := []byte("hi")
	data := append([]byte{0x9f, 0x1f, byte(len(content))}, content...)
	node, err := Decode(data)
	if err != nil {
		t.Fatalf("minimal high-tag-number encoding rejected: %v", err)
	}
	if node.Class != ClassContextSpecific || node.Tag != 31 {
		t.Fatalf("decoded class=%d tag=%d, want context/31", node.Class, node.Tag)
	}
	if !asn1Accepts(data) {
		t.Fatal("encoding/asn1 should accept minimal high-tag-number encoding")
	}
}

func TestBitStringRules(t *testing.T) {
	if _, err := Decode(tlv(0x03, []byte{0x00, 0xf0})); err != nil {
		t.Fatalf("canonical BIT STRING rejected: %v", err)
	}

	// Unused bits count above 7.
	if _, err := Decode(tlv(0x03, []byte{0x08, 0x00})); err == nil {
		t.Fatal("BIT STRING with unused-bits=8 accepted")
	}

	// Unused bits must be zero in the final octet: 4 unused bits, value 0x01.
	if _, err := Decode(tlv(0x03, []byte{0x04, 0xf1})); err == nil {
		t.Fatal("BIT STRING with nonzero unused bits accepted")
	}

	// Empty BIT STRING must declare zero unused bits.
	if _, err := Decode(tlv(0x03, []byte{0x01})); err == nil {
		t.Fatal("empty BIT STRING with unused-bits=1 accepted")
	}
}

func TestEmptyAndPrimitiveRules(t *testing.T) {
	// Empty INTEGER is invalid.
	if _, err := Decode(tlv(0x02, nil)); err == nil {
		t.Fatal("empty INTEGER accepted")
	}

	// Primitive universal type encoded as constructed.
	if _, err := Decode(tlv(0x22, []byte{0x01})); err == nil {
		t.Fatal("constructed INTEGER accepted")
	}

	// Primitive SEQUENCE is invalid.
	if _, err := Decode(tlv(0x10, nil)); err == nil {
		t.Fatal("primitive SEQUENCE accepted")
	}

	// Bare end-of-contents octet is invalid.
	if _, err := Decode([]byte{0x00, 0x00}); err == nil {
		t.Fatal("end-of-contents octet accepted")
	}
}

func TestTruncatedLengthAndContent(t *testing.T) {
	cases := map[string][]byte{
		"missing-length":         {0x02},
		"truncated-long-length":  {0x02, 0x82, 0x01},
		"declared-content-short": {0x02, 0x03, 0x01, 0x00},
		"truncated-high-tag":     {0x1f, 0x82},
	}
	for name, data := range cases {
		if _, err := Decode(data); err == nil {
			t.Fatalf("%s accepted", name)
		} else if OffsetOf(err) < 0 {
			t.Fatalf("%s error carries no offset: %v", name, err)
		}
	}
}
