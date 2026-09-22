package bitlayout

import (
	"encoding/json"
	"fmt"
	"os"
)

// Schema is the JSON description of a record layout.
//
// BitOrder ("msb" or "lsb", default "msb") and ByteOrder ("big" or
// "little", default "big") are independent properties.
type Schema struct {
	Name      string      `json:"name"`
	BitOrder  string      `json:"bitOrder"`
	ByteOrder string      `json:"byteOrder"`
	Fields    []FieldSpec `json:"fields"`
}

// StructSpec is a group of fields, used for array elements and union cases.
type StructSpec struct {
	Fields []FieldSpec `json:"fields"`
}

// FieldSpec describes one field. Type is one of:
//
//	"uint"      fixed-width unsigned integer, requires bits (1..64);
//	            optional enum restricts the legal values.
//	"int"       fixed-width two's-complement signed integer, bits (1..64).
//	"array"     element count read from a previous uint field (lengthFrom);
//	            optional maxLength bounds the allocation at compile time.
//	"union"     case selected by a previous uint field (tagFrom); cases maps
//	            decimal (or 0x-prefixed) tag values to field groups.
//	"align"     zero-valued padding up to a multiple of "multiple" bits;
//	            non-zero padding is rejected as illegal reserved bits.
//	"checksum"  covers the encoded range from the start of field "from"
//	            through the end of field "to"; algo "sum" (the default).
type FieldSpec struct {
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Bits       int                    `json:"bits"`
	Enum       []uint64               `json:"enum"`
	LengthFrom string                 `json:"lengthFrom"`
	MaxLength  uint64                 `json:"maxLength"`
	Element    *StructSpec            `json:"element"`
	TagFrom    string                 `json:"tagFrom"`
	Cases      map[string]*StructSpec `json:"cases"`
	Multiple   int                    `json:"multiple"`
	Algo       string                 `json:"algo"`
	From       string                 `json:"from"`
	To         string                 `json:"to"`
}

// LoadSchema reads a JSON schema file and compiles it into a Plan.
func LoadSchema(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("bitlayout: parsing %s: %w", path, err)
	}
	return Compile(&s)
}
