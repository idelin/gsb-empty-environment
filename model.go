package linker

import (
	"fmt"
	"strings"
)

// SectionID is an index into Image.Sections.  SectionID(-1) means "none".
type SectionID int

// NoSection is the invalid / undefined section identifier.
const NoSection SectionID = -1

// SectionKind is the coarse permission class of a section.
type SectionKind string

const (
	KindCode         SectionKind = "code"
	KindReadOnlyData SectionKind = "rodata"
	KindData         SectionKind = "data"
)

// Common spelling aliases.
const (
	SectionCode         = KindCode
	SectionReadOnlyData = KindReadOnlyData
	SectionData         = KindData
)

// RelocType distinguishes a direct call edge from an address-taking edge.
// Address-taking edges matter to --icf=safe because they make pointer identity
// observable to the program.
type RelocType string

const (
	RelocCall      RelocType = "CALL"
	RelocAddrTaken RelocType = "ADDR_TAKEN"
)

// Short aliases used by some callers.
const (
	CallReloc      = RelocCall
	AddrTakenReloc = RelocAddrTaken
)

// SectionFlags models the subset of ELF section flags relevant to this engine.
type SectionFlags uint32

const (
	// SHFKeep marks a section as a forced GC root (SHF_KEEP).
	SHFKeep SectionFlags = 1 << iota
	// SHFNoICF marks a section as ineligible for folding (NO_ICF).
	SHFNoICF
	// SHFWritable marks a section whose contents may be mutated at run time.
	SHFWritable
)

const (
	SHF_KEEP     = SHFKeep
	SHF_NO_ICF   = SHFNoICF
	SHF_NOICF    = SHFNoICF
	SHF_WRITE    = SHFWritable
	FlagKeep     = SHFKeep
	FlagNoICF    = SHFNoICF
	FlagWritable = SHFWritable
)

// Section is a unit of reachability and folding.  Data contains the raw
// instruction or data bytes.  Relocation slots are described separately in
// Image.Relocations.
type Section struct {
	Name  string
	Kind  SectionKind
	Flags SectionFlags
	Align uint64
	Data  []byte

	// Arity is optional metadata used by the built-in VM when automatic
	// validation probes are requested.  It is not part of an ICF equivalence
	// signature and may be left at zero.
	Arity uint8
}

// Symbol is a named address expression "section + value".
//
// A symbol with Section == NoSection is an external/undefined symbol.  This
// package does not resolve external symbols; they remain opaque and distinct
// in ICF refinement.
type Symbol struct {
	Name    string
	Section SectionID
	Value   uint64
	Size    uint64
}

// Relocation is a link-time edge.  A relocation is normally addressed by a
// symbol name.  If Symbol is empty, Target/TargetOffset identify the section
// and offset directly.  Addend is applied to the symbol value when the target
// address is formed.
//
// Size is the width in bytes of the relocation slot.  If it is zero, a width
// of 4 is used for CALL and 8 for ADDR_TAKEN.  The width controls which bytes
// are treated as relocation holes by the ICF signature.
type Relocation struct {
	From         SectionID
	Offset       uint64
	Type         RelocType
	Symbol       string
	Addend       int64
	Target       SectionID
	TargetOffset uint64
	Size         uint8
}

// Image is a flat, linked view of one or more object files.
//
// Section IDs are slice indexes.  The graph has an edge for every relocation
// in Relocations, from From to the section defining the relocation target.
type Image struct {
	Sections     []Section
	Symbols      []Symbol
	Relocations  []Relocation
	EntrySymbols []string
	RootSections []SectionID
}

// AddSection appends a section and returns its new ID.
func (img *Image) AddSection(sec Section) SectionID {
	id := SectionID(len(img.Sections))
	img.Sections = append(img.Sections, cloneSection(sec))
	return id
}

// AddSymbol appends a symbol.
func (img *Image) AddSymbol(sym Symbol) {
	img.Symbols = append(img.Symbols, sym)
}

// AddRelocation appends a relocation.
func (img *Image) AddRelocation(rel Relocation) {
	img.Relocations = append(img.Relocations, rel)
}

// Object is one input object file.  Section IDs in Symbols and Relocations are
// local to the object.  MergeObjects converts a set of objects into a flat
// Image by concatenating sections and rebasing section IDs.
type Object struct {
	Name         string
	Sections     []Section
	Symbols      []Symbol
	Relocations  []Relocation
	EntrySymbols []string
}

// Clone returns a deep copy of the image.
func (img *Image) Clone() *Image {
	if img == nil {
		return nil
	}
	out := &Image{
		Sections:     make([]Section, len(img.Sections)),
		Symbols:      append([]Symbol(nil), img.Symbols...),
		Relocations:  append([]Relocation(nil), img.Relocations...),
		EntrySymbols: append([]string(nil), img.EntrySymbols...),
		RootSections: append([]SectionID(nil), img.RootSections...),
	}
	for i, sec := range img.Sections {
		out.Sections[i] = sec
		out.Sections[i].Data = append([]byte(nil), sec.Data...)
	}
	return out
}

// SymbolMap returns a name-to-symbol index.  Duplicate names are rejected by
// Validate; if a malformed image has duplicates the last definition wins.
func (img *Image) SymbolMap() map[string]Symbol {
	if img == nil {
		return nil
	}
	m := make(map[string]Symbol, len(img.Symbols))
	for _, sym := range img.Symbols {
		m[sym.Name] = sym
	}
	return m
}

// LookupSymbol resolves a symbol by name.
func (img *Image) LookupSymbol(name string) (Symbol, bool) {
	if img == nil {
		return Symbol{}, false
	}
	for _, sym := range img.Symbols {
		if sym.Name == name {
			return sym, true
		}
	}
	return Symbol{}, false
}

// MergeObjects concatenates object-local section spaces and resolves symbols
// by name.  It is intentionally strict about duplicate definitions: two
// defined symbols with the same name are an error.
func MergeObjects(objects ...*Object) (*Image, error) {
	out := &Image{}
	bases := make([]SectionID, len(objects))
	for oi, obj := range objects {
		if obj == nil {
			return nil, fmt.Errorf("linker: nil object")
		}
		bases[oi] = SectionID(len(out.Sections))
		for _, sec := range obj.Sections {
			out.Sections = append(out.Sections, cloneSection(sec))
		}
		out.EntrySymbols = append(out.EntrySymbols, obj.EntrySymbols...)
	}

	defs := make(map[string]Symbol)
	var defined []Symbol
	for oi, obj := range objects {
		for _, sym := range obj.Symbols {
			ns := sym
			if ns.Section != NoSection {
				if int(ns.Section) < 0 || int(ns.Section) >= len(obj.Sections) {
					return nil, fmt.Errorf("%s: symbol %q has invalid local section %d", obj.Name, sym.Name, sym.Section)
				}
				ns.Section += bases[oi]
			}
			if ns.Name == "" {
				return nil, fmt.Errorf("%s: empty symbol name", obj.Name)
			}
			if ns.Section == NoSection {
				continue
			}
			if prev, ok := defs[ns.Name]; ok {
				return nil, fmt.Errorf("%s: duplicate definition of symbol %q (already at section %d)", obj.Name, ns.Name, prev.Section)
			}
			defs[ns.Name] = ns
			defined = append(defined, ns)
		}
	}
	out.Symbols = append(out.Symbols, defined...)

	seenExternal := make(map[string]bool)
	for _, obj := range objects {
		for _, sym := range obj.Symbols {
			if sym.Section != NoSection || sym.Name == "" {
				continue
			}
			if _, isDefined := defs[sym.Name]; isDefined || seenExternal[sym.Name] {
				continue
			}
			seenExternal[sym.Name] = true
			out.Symbols = append(out.Symbols, Symbol{Name: sym.Name, Section: NoSection, Value: sym.Value, Size: sym.Size})
		}
	}

	for oi, obj := range objects {
		for _, rel := range obj.Relocations {
			nr := rel
			if int(nr.From) < 0 || int(nr.From) >= len(obj.Sections) {
				return nil, fmt.Errorf("%s: relocation has invalid local source section %d", obj.Name, rel.From)
			}
			nr.From += bases[oi]
			if nr.Symbol == "" {
				if int(nr.Target) < 0 || int(nr.Target) >= len(obj.Sections) {
					return nil, fmt.Errorf("%s: relocation has invalid local target section %d", obj.Name, rel.Target)
				}
				nr.Target += bases[oi]
			} else {
				nr.Target = NoSection
			}
			out.Relocations = append(out.Relocations, nr)
		}
	}
	out.EntrySymbols = uniqueStrings(out.EntrySymbols)
	return out, out.Validate()
}

func cloneSection(sec Section) Section {
	out := sec
	out.Data = append([]byte(nil), sec.Data...)
	return out
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Validate checks structural invariants of the flat image.
func (img *Image) Validate() error {
	if img == nil {
		return fmt.Errorf("linker: nil image")
	}
	for i, sec := range img.Sections {
		_, ok := normalizeKind(sec.Kind)
		if !ok {
			return fmt.Errorf("linker: section %d (%q) has unknown kind %q", i, sec.Name, sec.Kind)
		}
	}
	names := make(map[string]bool, len(img.Symbols))
	for i, sym := range img.Symbols {
		if sym.Name == "" {
			return fmt.Errorf("linker: symbol %d has empty name", i)
		}
		if names[sym.Name] {
			return fmt.Errorf("linker: duplicate symbol %q", sym.Name)
		}
		names[sym.Name] = true
		if sym.Section != NoSection {
			if int(sym.Section) < 0 || int(sym.Section) >= len(img.Sections) {
				return fmt.Errorf("linker: symbol %q has invalid section %d", sym.Name, sym.Section)
			}
			if sym.Value > uint64(len(img.Sections[sym.Section].Data)) {
				return fmt.Errorf("linker: symbol %q value %d is outside section %d", sym.Name, sym.Value, sym.Section)
			}
		}
	}
	for i, rel := range img.Relocations {
		if int(rel.From) < 0 || int(rel.From) >= len(img.Sections) {
			return fmt.Errorf("linker: relocation %d has invalid source section %d", i, rel.From)
		}
		if _, ok := normalizeRelocType(rel.Type); !ok {
			return fmt.Errorf("linker: relocation %d has unknown type %q", i, rel.Type)
		}
		if rel.Offset > uint64(len(img.Sections[rel.From].Data)) {
			return fmt.Errorf("linker: relocation %d offset %d is outside section %d", i, rel.Offset, rel.From)
		}
		if rel.Symbol == "" {
			if int(rel.Target) < 0 || int(rel.Target) >= len(img.Sections) {
				return fmt.Errorf("linker: relocation %d has no symbol and invalid target section %d", i, rel.Target)
			}
		}
		// A symbolic target that is absent is a valid undefined external.
	}
	for _, id := range img.RootSections {
		if int(id) < 0 || int(id) >= len(img.Sections) {
			return fmt.Errorf("linker: invalid root section %d", id)
		}
	}
	return nil
}

func normalizeKind(k SectionKind) (SectionKind, bool) {
	s := strings.ToLower(strings.TrimSpace(string(k)))
	switch s {
	case "", "code", "text", ".text", "exec", "executable", "instruction", "instructions":
		return KindCode, true
	case "rodata", ".rodata", "readonly", "read-only", "read_only", "readonlydata", "const", ".rodata.cst":
		return KindReadOnlyData, true
	case "data", ".data", "bss", ".bss", "writable", "writable_data", "rw":
		return KindData, true
	default:
		return "", false
	}
}

func normalizeRelocType(t RelocType) (RelocType, bool) {
	s := strings.ToUpper(strings.TrimSpace(string(t)))
	switch s {
	case "CALL", "DIRECT_CALL", "R_X86_64_PLT32", "R_X86_64_CALL", "REL32", "PCREL":
		return RelocCall, true
	case "ADDR_TAKEN", "ADDR", "ADDRESS", "ADDRESS_TAKEN", "POINTER", "R_X86_64_64", "ABS64", "R_X86_64_PC32":
		return RelocAddrTaken, true
	default:
		return "", false
	}
}

func kindOf(sec Section) SectionKind {
	k, ok := normalizeKind(sec.Kind)
	if !ok {
		return KindCode
	}
	return k
}

func relocTypeOf(rel Relocation) RelocType {
	t, ok := normalizeRelocType(rel.Type)
	if !ok {
		return RelocCall
	}
	return t
}

func relocationWidth(rel Relocation) uint64 {
	if rel.Size != 0 {
		return uint64(rel.Size)
	}
	if relocTypeOf(rel) == RelocCall {
		return 4
	}
	return 8
}

type targetRef struct {
	Section    SectionID
	Offset     int64
	External   string
	IsExternal bool
}

// ResolvedTarget is the result of resolving a relocation.
type ResolvedTarget struct {
	// Section is NoSection when the target is an undefined external symbol.
	Section SectionID
	// Offset is the section-relative target value, including Addend.
	Offset int64
	// External is the external symbol name when IsExternal is true.
	External   string
	IsExternal bool
}

// ResolveRelocation resolves a relocation against the image's symbol table.
func (img *Image) ResolveRelocation(rel Relocation) (ResolvedTarget, error) {
	if img == nil {
		return ResolvedTarget{}, fmt.Errorf("linker: nil image")
	}
	t, err := img.resolveRelocation(rel, img.SymbolMap())
	if err != nil {
		return ResolvedTarget{}, err
	}
	return ResolvedTarget{
		Section:    t.Section,
		Offset:     t.Offset,
		External:   t.External,
		IsExternal: t.IsExternal,
	}, nil
}

// AddressOfSymbol returns the abstract address of a defined symbol plus an
// addend.
func (img *Image) AddressOfSymbol(name string, addend int64) (Address, bool) {
	if img == nil {
		return Address{}, false
	}
	sym, ok := img.LookupSymbol(name)
	if !ok || sym.Section == NoSection {
		return Address{}, false
	}
	off := int64(sym.Value) + addend
	if off < 0 {
		return Address{}, false
	}
	return Address{Section: sym.Section, Offset: uint64(off)}, true
}

func (img *Image) resolveRelocation(rel Relocation, symbols map[string]Symbol) (targetRef, error) {
	if rel.Symbol != "" {
		if sym, ok := symbols[rel.Symbol]; ok {
			if sym.Section == NoSection {
				return targetRef{External: sym.Name, Offset: rel.Addend, IsExternal: true}, nil
			}
			if int(sym.Section) < 0 || int(sym.Section) >= len(img.Sections) {
				return targetRef{}, fmt.Errorf("symbol %q has invalid section %d", sym.Name, sym.Section)
			}
			return targetRef{Section: sym.Section, Offset: int64(sym.Value) + rel.Addend}, nil
		}
		return targetRef{External: rel.Symbol, Offset: rel.Addend, IsExternal: true}, nil
	}
	if int(rel.Target) < 0 || int(rel.Target) >= len(img.Sections) {
		return targetRef{}, fmt.Errorf("relocation has no symbol and invalid target section %d", rel.Target)
	}
	return targetRef{Section: rel.Target, Offset: int64(rel.TargetOffset) + rel.Addend}, nil
}

func (t targetRef) addressKey() string {
	if t.IsExternal {
		return "ext:" + t.External
	}
	return fmt.Sprintf("sec:%d:%d", t.Section, t.Offset)
}
