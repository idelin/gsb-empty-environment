package bridge

import (
	"crypto/sha256"
	"fmt"
	"sort"
)

type Type int

const (
	TVarint Type = iota
	TBytes
	TString
)

func (t Type) kind() Kind {
	if t == TVarint {
		return KindVarint
	}
	return KindBytes
}

func (t Type) String() string {
	switch t {
	case TVarint:
		return "varint"
	case TBytes:
		return "bytes"
	case TString:
		return "string"
	}
	return "?"
}

// Rule carries migration hints for a field.
type Rule struct {
	HasDefault  bool   // target-side: inject Default* when source cannot provide the field
	DefaultNum  uint64 // default for TVarint
	DefaultByte []byte // default for TBytes/TString
}

type FieldDef struct {
	ID        uint32
	Name      string
	Type      Type
	Required  bool
	Extension bool // carrier field for preserved unknown/unrepresentable data
	Rule      Rule
}

type Schema struct {
	Version int
	Fields  []FieldDef // sorted by ID
	byID    map[uint32]FieldDef
}

func NewSchema(version int, defs []FieldDef) (*Schema, error) {
	if version != 1 && version != 2 {
		return nil, fmt.Errorf("bridge: unsupported schema version %d", version)
	}
	s := &Schema{Version: version, byID: map[uint32]FieldDef{}}
	extSeen := false
	for _, f := range defs {
		if f.ID == 0 {
			return nil, fmt.Errorf("bridge: field id 0 is reserved")
		}
		if _, dup := s.byID[f.ID]; dup {
			return nil, fmt.Errorf("bridge: duplicate field id %d", f.ID)
		}
		if f.Extension {
			if f.Type != TBytes || f.Required {
				return nil, fmt.Errorf("bridge: extension field %d must be optional bytes", f.ID)
			}
			if extSeen {
				return nil, fmt.Errorf("bridge: more than one extension field")
			}
			extSeen = true
		}
		s.byID[f.ID] = f
		s.Fields = append(s.Fields, f)
	}
	sort.Slice(s.Fields, func(i, j int) bool { return s.Fields[i].ID < s.Fields[j].ID })
	return s, nil
}

func (s *Schema) field(id uint32) (FieldDef, bool) {
	f, ok := s.byID[id]
	return f, ok
}

func (s *Schema) extension() (FieldDef, bool) {
	for _, f := range s.Fields {
		if f.Extension {
			return f, true
		}
	}
	return FieldDef{}, false
}

// digest is a canonical fingerprint of the schema.
func (s *Schema) digest() [32]byte {
	h := sha256.New()
	fmt.Fprintf(h, "schema v%d\n", s.Version)
	for _, f := range s.Fields {
		fmt.Fprintf(h, "%d|%s|%s|req=%v|ext=%v|def=%v:%x:%x\n",
			f.ID, f.Name, f.Type, f.Required, f.Extension,
			f.Rule.HasDefault, f.Rule.DefaultNum, f.Rule.DefaultByte)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
