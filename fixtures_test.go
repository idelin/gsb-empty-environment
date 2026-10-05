package wire

// Standalone byte fixtures, written out by hand (not produced by the
// encoder under test) so that encoding and decoding are checked
// against independent data.

// Field 1, varint, maximum uint64 value (10-byte varint).
var fixtureMaxUint64 = []byte{
	0x08,                                                       // key: field 1, wire 0
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01, // 2^64-1
}

// Field 2, empty length-delimited payload.
var fixtureEmptyLength = []byte{
	0x12, // key: field 2, wire 2
	0x00, // length 0
}

// Field 1 repeated three times: values 1, 2, 3.
var fixtureRepeated = []byte{
	0x08, 0x01,
	0x08, 0x02,
	0x08, 0x03,
}

// Field 2^29-1 (maximum legal), varint value 0.
var fixtureMaxFieldNumber = []byte{
	0xf8, 0xff, 0xff, 0xff, 0x0f, // key: field 536870911, wire 0
	0x00,
}

// Composite message exercising every supported construct.
// Schema: field 5 = nested message, 6 = packed varint,
// 7 = packed fixed32, 8 = packed fixed64. Fields 9 and 10 are unknown.
var fixtureComposite = []byte{
	0x08, 0x96, 0x01, // field 1  varint 150
	0x12, 0x02, 0x61, 0x62, // field 2  raw "ab"
	0x19, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // field 3  fixed64 1
	0x25, 0x07, 0x00, 0x00, 0x00, // field 4  fixed32 7
	0x2a, 0x07, 0x08, 0x01, 0x25, 0xef, 0xbe, 0xad, 0xde, // field 5  message{1:1, 4:0xdeadbeef}
	0x32, 0x04, 0x01, 0x02, 0xac, 0x02, // field 6  packed varint [1 2 300]
	0x3a, 0x08, 0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, // field 7 packed fixed32 [1 2]
	0x42, 0x08, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // field 8 packed fixed64 [5]
	0x48, 0x09, // field 9  (unknown) varint 9
	0x52, 0x03, 0x78, 0x79, 0x7a, // field 10 (unknown) raw "xyz"
}

var fixtureSchema = Schema{
	5: {Kind: KindMessage},
	6: {Kind: KindPacked, ElemWire: WireVarint},
	7: {Kind: KindPacked, ElemWire: WireFixed32},
	8: {Kind: KindPacked, ElemWire: WireFixed64},
}

// Nested three levels deep: field 5 { field 5 { field 5 { field 1 = 1 } } }.
var fixtureNested = []byte{
	0x2a, 0x06, 0x2a, 0x04, 0x2a, 0x02, 0x08, 0x01,
}

var fixtureNestedSchema = Schema{5: {Kind: KindMessage}}
