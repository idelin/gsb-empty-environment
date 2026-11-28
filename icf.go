package linker

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// ICFMode selects the identical-code-folding policy.
type ICFMode string

const (
	ICFDisabled ICFMode = "none"
	ICFSafe     ICFMode = "safe"
	ICFAll      ICFMode = "all"
)

// ParseICFMode accepts the spellings commonly used on linker command lines.
func ParseICFMode(s string) (ICFMode, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "--")
	s = strings.TrimPrefix(s, "icf=")
	switch s {
	case "", "none", "off", "false", "disabled", "no":
		return ICFDisabled, nil
	case "safe", "safe-icf", "on":
		return ICFSafe, nil
	case "all", "aggressive":
		return ICFAll, nil
	default:
		return "", fmt.Errorf("linker: unknown ICF mode %q", s)
	}
}

// ICFGroup is one final Moore equivalence class.
type ICFGroup struct {
	Class          int
	Sections       []SectionID
	Representative SectionID
	Merged         []SectionID
	AddressTaken   []SectionID
}

// ICFResult describes one folding pass.
type ICFResult struct {
	Mode ICFMode
	// Groups contains every final candidate equivalence class in
	// deterministic class order.  Singleton classes are included.
	Groups []ICFGroup
	// Merges maps every folded-away original section to its representative.
	Merges map[SectionID]SectionID
	// Canonical maps every original section to its representative (identity
	// for sections that were not folded).
	Canonical map[SectionID]SectionID
	// OldToNew maps an original section to its section ID in the output image.
	// Folded sections map to the representative's new ID.
	OldToNew map[SectionID]SectionID
	// NewToOld is the inverse of OldToNew for retained sections.
	NewToOld map[SectionID]SectionID
	// AddressTaken lists sections whose address appears in a live ADDR_TAKEN
	// relocation before folding.
	AddressTaken  []SectionID
	SavedSections int
	SavedBytes    uint64
}

// Fold is a shorthand for ICF.
func Fold(img *Image, mode ICFMode) (*Image, *ICFResult, error) {
	return ICF(img, mode)
}

// ICF folds identical sections using Moore partition refinement.
//
// Candidate sections must be non-writable code or read-only data, and must not
// carry SHF_NO_ICF.  The initial partition uses only non-relocation bytes and
// the ordered relocation hole shape.  Each refinement round replaces every
// relocation target with the target section's current equivalence-class ID.
// The round is repeated until the partition stops splitting, which makes
// self-recursive and arbitrarily long mutually-recursive cycles comparable.
//
// In safe mode, two sections whose addresses are both taken by live ADDR_TAKEN
// relocations are never merged with each other.  An address-not-taken section
// may still be folded into an address-taken representative, because its address
// is not observable.
func ICF(img *Image, mode ICFMode) (*Image, *ICFResult, error) {
	if img == nil {
		return nil, nil, fmt.Errorf("linker: nil image")
	}
	if err := img.Validate(); err != nil {
		return nil, nil, err
	}
	parsedMode, err := ParseICFMode(string(mode))
	if err != nil {
		return nil, nil, err
	}
	mode = parsedMode

	res := &ICFResult{
		Mode:      mode,
		Merges:    make(map[SectionID]SectionID),
		Canonical: make(map[SectionID]SectionID),
		OldToNew:  make(map[SectionID]SectionID),
		NewToOld:  make(map[SectionID]SectionID),
	}
	for i := range img.Sections {
		id := SectionID(i)
		res.Canonical[id] = id
	}

	if mode == ICFDisabled {
		return cloneWithIdentityMap(img, res)
	}

	symbols := img.SymbolMap()
	relocsByFrom := groupRelocations(img.Relocations)
	candidates := make([]SectionID, 0, len(img.Sections))
	for i, sec := range img.Sections {
		if isICFCandidate(sec) {
			candidates = append(candidates, SectionID(i))
		}
	}

	addressed := addressTakenSections(img, symbols)
	for _, id := range orderedSectionIDs(addressed, len(img.Sections)) {
		res.AddressTaken = append(res.AddressTaken, id)
	}

	if len(candidates) > 1 {
		baseKeys := make(map[SectionID]string, len(candidates))
		for _, id := range candidates {
			key, err := staticSignature(img.Sections[id], relocsByFrom[id])
			if err != nil {
				return nil, nil, fmt.Errorf("linker: section %d: %w", id, err)
			}
			baseKeys[id] = key
		}

		classes, count, err := assignClasses(candidates, func(id SectionID) (string, error) {
			return baseKeys[id], nil
		})
		if err != nil {
			return nil, nil, err
		}
		for round := 0; ; round++ {
			next, nextCount, err := assignClasses(candidates, func(id SectionID) (string, error) {
				return refinedSignature(img, id, baseKeys[id], relocsByFrom[id], classes, symbols)
			})
			if err != nil {
				return nil, nil, err
			}
			if nextCount < count {
				return nil, nil, fmt.Errorf("linker: ICF partition refinement merged classes")
			}
			if nextCount == count {
				classes = next
				break
			}
			if round > len(candidates)+2 {
				return nil, nil, fmt.Errorf("linker: ICF partition refinement did not converge")
			}
			classes = next
			count = nextCount
		}

		groups := make(map[int][]SectionID)
		for _, id := range candidates {
			groups[classes[id]] = append(groups[classes[id]], id)
		}
		classIDs := make([]int, 0, len(groups))
		for cid := range groups {
			classIDs = append(classIDs, cid)
		}
		sort.Ints(classIDs)

		for _, cid := range classIDs {
			members := groups[cid]
			sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
			g := ICFGroup{Class: cid, Sections: append([]SectionID(nil), members...)}
			for _, id := range members {
				if addressed[id] {
					g.AddressTaken = append(g.AddressTaken, id)
				}
			}
			if len(members) > 1 {
				mergeGroup(mode, members, addressed, &g, res)
			} else if len(members) == 1 {
				g.Representative = members[0]
			}
			res.Groups = append(res.Groups, g)
		}
	}

	return rewriteAfterICF(img, res)
}

func isICFCandidate(sec Section) bool {
	if sec.Flags&(SHFWritable|SHFNoICF) != 0 {
		return false
	}
	switch kindOf(sec) {
	case KindCode, KindReadOnlyData:
		return true
	default:
		return false
	}
}

func addressTakenSections(img *Image, symbols map[string]Symbol) map[SectionID]bool {
	out := make(map[SectionID]bool)
	for _, rel := range img.Relocations {
		if relocTypeOf(rel) != RelocAddrTaken {
			continue
		}
		tgt, err := img.resolveRelocation(rel, symbols)
		if err != nil || tgt.IsExternal || tgt.Section == NoSection {
			continue
		}
		out[tgt.Section] = true
	}
	return out
}

func mergeGroup(mode ICFMode, members []SectionID, addressed map[SectionID]bool, g *ICFGroup, res *ICFResult) {
	if mode == ICFAll {
		rep := members[0]
		g.Representative = rep
		for _, id := range members[1:] {
			res.Merges[id] = rep
			res.Canonical[id] = rep
			g.Merged = append(g.Merged, id)
		}
	} else {
		var addressedMembers, unaddressed []SectionID
		for _, id := range members {
			if addressed[id] {
				addressedMembers = append(addressedMembers, id)
			} else {
				unaddressed = append(unaddressed, id)
			}
		}
		if len(addressedMembers) == 0 {
			rep := members[0]
			g.Representative = rep
			for _, id := range members[1:] {
				res.Merges[id] = rep
				res.Canonical[id] = rep
				g.Merged = append(g.Merged, id)
			}
			return
		}
		// Every address-taken member must retain a distinct address.  All
		// non-address-taken members are unobserved as pointers and may share
		// one addressed representative.
		rep := addressedMembers[0]
		g.Representative = rep
		for _, id := range unaddressed {
			res.Merges[id] = rep
			res.Canonical[id] = rep
			g.Merged = append(g.Merged, id)
		}
		sort.Slice(g.Merged, func(i, j int) bool { return g.Merged[i] < g.Merged[j] })
	}
}

func cloneWithIdentityMap(img *Image, res *ICFResult) (*Image, *ICFResult, error) {
	out := img.Clone()
	for i := range out.Sections {
		id := SectionID(i)
		res.OldToNew[id] = id
		res.NewToOld[id] = id
	}
	return out, res, nil
}

func groupRelocations(rels []Relocation) map[SectionID][]Relocation {
	out := make(map[SectionID][]Relocation)
	for _, rel := range rels {
		out[rel.From] = append(out[rel.From], rel)
	}
	for id := range out {
		list := out[id]
		sort.SliceStable(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if a.Offset != b.Offset {
				return a.Offset < b.Offset
			}
			if relocTypeOf(a) != relocTypeOf(b) {
				return relocTypeOf(a) < relocTypeOf(b)
			}
			if a.Size != b.Size {
				return a.Size < b.Size
			}
			if a.Addend != b.Addend {
				return a.Addend < b.Addend
			}
			if a.Symbol != b.Symbol {
				return a.Symbol < b.Symbol
			}
			return a.Target < b.Target
		})
		out[id] = list
	}
	return out
}

// staticSignature is the initial Moore partition key:
// (non-relocation raw bytes, relocation offsets/types/widths).
func staticSignature(sec Section, rels []Relocation) (string, error) {
	masked := append([]byte(nil), sec.Data...)
	for _, rel := range rels {
		if rel.Offset > uint64(len(masked)) {
			return "", fmt.Errorf("relocation offset %d outside section", rel.Offset)
		}
		start := rel.Offset
		end := start + relocationWidth(rel)
		if end > uint64(len(masked)) {
			end = uint64(len(masked))
		}
		for i := start; i < end; i++ {
			masked[i] = 0
		}
	}

	var b []byte
	b = appendString(b, string(kindOf(sec)))
	b = appendU64(b, uint64(len(masked)))
	b = append(b, masked...)
	b = appendU64(b, uint64(len(rels)))
	for _, rel := range rels {
		b = appendU64(b, rel.Offset)
		b = appendString(b, string(relocTypeOf(rel)))
		b = appendU64(b, relocationWidth(rel))
	}
	return string(b), nil
}

// refinedSignature replaces every relocation target with the target section's
// current equivalence class ID.  Target offsets are retained: two calls into
// different entry points of an otherwise identical section are not identical.
func refinedSignature(img *Image, id SectionID, base string, rels []Relocation, classes map[SectionID]int, symbols map[string]Symbol) (string, error) {
	var b []byte
	b = appendString(b, base)
	b = appendU64(b, uint64(len(rels)))
	for _, rel := range rels {
		tgt, err := img.resolveRelocation(rel, symbols)
		if err != nil {
			return "", err
		}
		b = appendString(b, targetClassKey(tgt, classes))
	}
	return string(b), nil
}

func targetClassKey(tgt targetRef, classes map[SectionID]int) string {
	if tgt.IsExternal || tgt.Section == NoSection {
		return fmt.Sprintf("E:%s@%d", tgt.External, tgt.Offset)
	}
	cid, ok := classes[tgt.Section]
	if !ok {
		// Non-candidate sections never fold, so their identity is a stable
		// leaf in the refinement lattice.
		return fmt.Sprintf("S:%d@%d", tgt.Section, tgt.Offset)
	}
	return fmt.Sprintf("C:%d@%d", cid, tgt.Offset)
}

func assignClasses(ids []SectionID, key func(SectionID) (string, error)) (map[SectionID]int, int, error) {
	type pair struct {
		id  SectionID
		key string
	}
	pairs := make([]pair, 0, len(ids))
	for _, id := range ids {
		k, err := key(id)
		if err != nil {
			return nil, 0, err
		}
		pairs = append(pairs, pair{id: id, key: k})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].id < pairs[j].id
	})
	out := make(map[SectionID]int, len(pairs))
	class := -1
	prev := ""
	for i, p := range pairs {
		if i == 0 || p.key != prev {
			class++
			prev = p.key
		}
		out[p.id] = class
	}
	return out, class + 1, nil
}

func appendU64(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}

func appendI64(dst []byte, v int64) []byte {
	return appendU64(dst, uint64(v))
}

func appendString(dst []byte, s string) []byte {
	dst = appendU64(dst, uint64(len(s)))
	return append(dst, s...)
}

func rewriteAfterICF(img *Image, res *ICFResult) (*Image, *ICFResult, error) {
	out := &Image{
		EntrySymbols: append([]string(nil), img.EntrySymbols...),
	}
	keepRep := make(map[SectionID]bool)
	for i, sec := range img.Sections {
		if sec.Flags&SHFKeep != 0 {
			keepRep[res.Canonical[SectionID(i)]] = true
		}
	}
	newIDs := make(map[SectionID]SectionID, len(img.Sections))
	for i := range img.Sections {
		id := SectionID(i)
		rep := res.Canonical[id]
		if rep != id {
			continue
		}
		newID := SectionID(len(out.Sections))
		newIDs[id] = newID
		res.NewToOld[newID] = id
		sec := cloneSection(img.Sections[id])
		if keepRep[id] {
			sec.Flags |= SHFKeep
		}
		out.Sections = append(out.Sections, sec)
	}
	for i := range img.Sections {
		id := SectionID(i)
		rep := res.Canonical[id]
		newID, ok := newIDs[rep]
		if !ok {
			return nil, nil, fmt.Errorf("linker: internal ICF canonical section %d missing", rep)
		}
		res.OldToNew[id] = newID
		if rep != id {
			res.SavedSections++
			res.SavedBytes += uint64(len(img.Sections[id].Data))
		}
	}

	for _, sym := range img.Symbols {
		if sym.Section == NoSection {
			out.Symbols = append(out.Symbols, sym)
			continue
		}
		rep := res.Canonical[sym.Section]
		newID, ok := newIDs[rep]
		if !ok {
			return nil, nil, fmt.Errorf("linker: symbol %q references folded section without representative", sym.Name)
		}
		sym.Section = newID
		out.Symbols = append(out.Symbols, sym)
	}

	for _, rel := range img.Relocations {
		if res.Canonical[rel.From] != rel.From {
			// The whole source section was folded away.  Its representative
			// has the identical relocation list.
			continue
		}
		rel.From = res.OldToNew[rel.From]
		if rel.Symbol == "" && rel.Target != NoSection {
			rep := res.Canonical[rel.Target]
			newID, ok := newIDs[rep]
			if !ok {
				return nil, nil, fmt.Errorf("linker: relocation target section %d has no representative", rel.Target)
			}
			rel.Target = newID
		}
		out.Relocations = append(out.Relocations, rel)
	}

	seenRoots := make(map[SectionID]bool)
	for _, id := range img.RootSections {
		rep := res.Canonical[id]
		newID, ok := newIDs[rep]
		if !ok || seenRoots[newID] {
			continue
		}
		seenRoots[newID] = true
		out.RootSections = append(out.RootSections, newID)
	}

	if err := out.Validate(); err != nil {
		return nil, nil, fmt.Errorf("linker: internal ICF image: %w", err)
	}
	return out, res, nil
}
