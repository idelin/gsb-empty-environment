package bitlayout

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// BitOrder selects how stream bits map into bytes. It is independent of
// ByteOrder: BitOrder governs the order bits of a group are emitted into
// the stream, ByteOrder governs the order the byte-sized groups of a
// multi-byte field are emitted.
type BitOrder string

const (
	// MSBFirst: stream bit 0 is bit 7 of byte 0 (network order).
	MSBFirst BitOrder = "msb"
	// LSBFirst: stream bit 0 is bit 0 of byte 0.
	LSBFirst BitOrder = "lsb"
)

// ByteOrder selects the emission order of the 8-bit groups of a field.
type ByteOrder string

const (
	BigEndian    ByteOrder = "big"
	LittleEndian ByteOrder = "little"
)

// Schema is the top-level JSON document accepted by Compile.
type Schema struct {
	Name      string     `json:"name"`
	BitOrder  BitOrder   `json:"bitOrder"`  // default "msb"
	ByteOrder ByteOrder  `json:"byteOrder"` // default "big"
	Fields    []FieldDef `json:"fields"`
}

// FieldDef is the JSON form of one field. Kind selects which attributes
// are meaningful:
//
//	int:      name, bits (1..64), signed
//	struct:   name, fields
//	array:    name, lengthFrom (earlier sibling int field), element
//	union:    name, tagFrom (earlier sibling int field), cases
//	pad:      align (pad with zero bits to a multiple of align)
//	checksum: name, bits, algo ("sum"|"xor"|"crc8"), from, to
//	          (names of earlier top-level fields; coverage is inclusive)
type FieldDef struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`

	// int / checksum
	Bits   int  `json:"bits,omitempty"`
	Signed bool `json:"signed,omitempty"`

	// struct / union cases
	Fields []FieldDef `json:"fields,omitempty"`

	// array
	LengthFrom string    `json:"lengthFrom,omitempty"`
	Element    *FieldDef `json:"element,omitempty"`

	// union
	TagFrom string             `json:"tagFrom,omitempty"`
	Cases   map[string]CaseDef `json:"cases,omitempty"`

	// pad
	Align int `json:"align,omitempty"`

	// checksum
	Algo string `json:"algo,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// CaseDef is one union branch.
type CaseDef struct {
	Fields []FieldDef `json:"fields"`
}

// ParseSchema decodes and validates the JSON surface syntax. The result
// still has to be passed to Compile, which performs semantic checks.
func ParseSchema(data []byte) (*Schema, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Schema
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("schema JSON: %w", err)
	}
	if s.BitOrder == "" {
		s.BitOrder = MSBFirst
	}
	if s.ByteOrder == "" {
		s.ByteOrder = BigEndian
	}
	if s.BitOrder != MSBFirst && s.BitOrder != LSBFirst {
		return nil, fmt.Errorf("schema: bitOrder must be %q or %q", MSBFirst, LSBFirst)
	}
	if s.ByteOrder != BigEndian && s.ByteOrder != LittleEndian {
		return nil, fmt.Errorf("schema: byteOrder must be %q or %q", BigEndian, LittleEndian)
	}
	if len(s.Fields) == 0 {
		return nil, fmt.Errorf("schema: fields must not be empty")
	}
	return &s, nil
}
