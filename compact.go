package pagereloc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
)

var errSimulatedCrash = errors.New("pagereloc: simulated crash mid-write")

// RelocTable maps old page numbers to new page numbers. It is the durable
// record that lets external consumers follow a compaction.
type RelocTable struct {
	OldToNew map[uint32]uint32
}

// Options tunes Compact. The zero value is valid.
type Options struct {
	// RelocPath overrides where the relocation table is published.
	// Defaults to dst + ".reloc".
	RelocPath string

	// CrashAfterPages is a test hook: if > 0, the new file write aborts
	// after that many page slots, simulating an abrupt exit mid-write.
	CrashAfterPages int
}

// Compact reads srcPath, rewrites every reachable page densely numbered
// from 0, updates all internal references, preserves external object IDs
// and page sharing, and publishes the result plus a relocation table.
//
// Nothing at dstPath or the relocation path is modified unless every
// validation passes; the source file is never modified.
func Compact(srcPath, dstPath string, opts *Options) (*RelocTable, error) {
	if opts == nil {
		opts = &Options{}
	}
	relocPath := opts.RelocPath
	if relocPath == "" {
		relocPath = dstPath + ".reloc"
	}

	src, err := ReadFile(srcPath)
	if err != nil {
		return nil, err
	}

	table, err := planRelocation(src)
	if err != nil {
		return nil, err
	}
	dst := applyRelocation(src, table)

	// Write the candidate to a temporary sibling; publish only after
	// independent validation succeeds.
	tmp := dstPath + ".tmp"
	if err := writeFileSync(tmp, dst, opts.CrashAfterPages); err != nil {
		// Leave the partial file in place, exactly as an abrupt exit
		// would; a later run truncates it. dstPath stays untouched.
		return nil, err
	}

	if err := ValidateEquivalence(srcPath, tmp, table); err != nil {
		os.Remove(tmp)
		return nil, fmt.Errorf("pagereloc: validation failed, not publishing: %v", err)
	}

	if err := writeRelocTable(relocPath, table); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, dstPath); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	syncDir(dstPath)
	return table, nil
}

// planRelocation walks the page graph depth-first from the root and
// assigns dense new page numbers in finish order of first visit. Shared
// subpages are visited once, so sharing is preserved. Cycles and dangling
// references are rejected as corruption.
func planRelocation(f *File) (*RelocTable, error) {
	const (
		white = 0 // unvisited
		gray  = 1 // on the DFS stack
		black = 2 // fully explored
	)
	color := make(map[uint32]int, len(f.Pages))
	table := &RelocTable{OldToNew: make(map[uint32]uint32)}

	type frame struct {
		page uint32
		next int // index into Refs
	}
	if _, ok := f.Pages[f.Root]; !ok {
		return nil, fmt.Errorf("pagereloc: root page %d is not allocated", f.Root)
	}
	color[f.Root] = gray
	table.OldToNew[f.Root] = 0
	stack := []frame{{page: f.Root}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		p := f.Pages[top.page]
		if top.next < len(p.Refs) {
			ref := p.Refs[top.next]
			top.next++
			if _, ok := f.Pages[ref]; !ok {
				return nil, fmt.Errorf("pagereloc: page %d has dangling reference to unallocated page %d",
					top.page, ref)
			}
			switch color[ref] {
			case gray:
				return nil, fmt.Errorf("pagereloc: corrupt graph: cycle through page %d", ref)
			case white:
				color[ref] = gray
				table.OldToNew[ref] = uint32(len(table.OldToNew))
				stack = append(stack, frame{page: ref})
			}
			continue
		}
		color[top.page] = black
		stack = stack[:len(stack)-1]
	}
	return table, nil
}

// applyRelocation builds the compacted in-memory image: new numbers for
// every reachable page, all references rewritten, object IDs and payloads
// carried over verbatim.
func applyRelocation(src *File, table *RelocTable) *File {
	dst := &File{
		PageSize: src.PageSize,
		Root:     table.OldToNew[src.Root],
		Pages:    make(map[uint32]*Page, len(table.OldToNew)),
	}
	for old, newNum := range table.OldToNew {
		p := src.Pages[old]
		np := &Page{
			ObjectID: p.ObjectID,
			Payload:  append([]byte(nil), p.Payload...),
			Refs:     make([]uint32, len(p.Refs)),
		}
		for i, r := range p.Refs {
			np.Refs[i] = table.OldToNew[r]
		}
		dst.Pages[newNum] = np
	}
	return dst
}

// ValidateEquivalence independently re-reads both files from disk and
// confirms that newPath is a faithful compaction of srcPath under table:
// identical reachable content, identical sharing structure, no dangling
// references. It shares no state with the compaction itself.
func ValidateEquivalence(srcPath, newPath string, table *RelocTable) error {
	src, err := ReadFile(srcPath)
	if err != nil {
		return err
	}
	dst, err := ReadFile(newPath)
	if err != nil {
		return err
	}

	srcReach, err := reachableSet(src)
	if err != nil {
		return fmt.Errorf("source: %v", err)
	}
	dstReach, err := reachableSet(dst)
	if err != nil {
		return fmt.Errorf("compacted: %v", err)
	}

	// The table must cover exactly the reachable source pages.
	if len(table.OldToNew) != len(srcReach) {
		return fmt.Errorf("relocation table covers %d pages, source has %d reachable",
			len(table.OldToNew), len(srcReach))
	}
	for old := range srcReach {
		if _, ok := table.OldToNew[old]; !ok {
			return fmt.Errorf("reachable source page %d missing from relocation table", old)
		}
	}

	// The mapping must be injective: distinct old pages stay distinct
	// (no merging) and shared pages stay shared (no splitting), and its
	// image must be exactly the pages reachable in the new file.
	image := make(map[uint32]uint32, len(table.OldToNew)) // new -> old
	for old, newNum := range table.OldToNew {
		if prev, dup := image[newNum]; dup {
			return fmt.Errorf("old pages %d and %d both map to new page %d", prev, old, newNum)
		}
		image[newNum] = old
		if _, ok := dstReach[newNum]; !ok {
			return fmt.Errorf("new page %d (from old %d) is not reachable in compacted file", newNum, old)
		}
	}
	if len(image) != len(dstReach) {
		return fmt.Errorf("compacted file has %d reachable pages, relocation table maps %d",
			len(dstReach), len(image))
	}

	// Root must follow the mapping.
	if dst.Root != table.OldToNew[src.Root] {
		return fmt.Errorf("root mismatch: old %d -> table %d, compacted root is %d",
			src.Root, table.OldToNew[src.Root], dst.Root)
	}

	// Page-by-page content equivalence: object IDs, payloads, and
	// reference structure (order-sensitive).
	for old, newNum := range table.OldToNew {
		op := src.Pages[old]
		np := dst.Pages[newNum]
		if op.ObjectID != np.ObjectID {
			return fmt.Errorf("page %d -> %d: object ID changed %d -> %d",
				old, newNum, op.ObjectID, np.ObjectID)
		}
		if !bytes.Equal(op.Payload, np.Payload) {
			return fmt.Errorf("page %d -> %d: payload differs", old, newNum)
		}
		if len(op.Refs) != len(np.Refs) {
			return fmt.Errorf("page %d -> %d: ref count %d -> %d",
				old, newNum, len(op.Refs), len(np.Refs))
		}
		for i, r := range op.Refs {
			if table.OldToNew[r] != np.Refs[i] {
				return fmt.Errorf("page %d -> %d: ref %d is old %d -> table %d, compacted has %d",
					old, newNum, i, r, table.OldToNew[r], np.Refs[i])
			}
		}
	}
	return nil
}

// reachableSet traverses the graph from the root, rejecting cycles and
// dangling references, and returns the set of reachable page numbers.
func reachableSet(f *File) (map[uint32]bool, error) {
	reach := make(map[uint32]bool)
	onStack := make(map[uint32]bool)
	var visit func(n uint32) error
	visit = func(n uint32) error {
		p, ok := f.Pages[n]
		if !ok {
			return fmt.Errorf("dangling reference to unallocated page %d", n)
		}
		if onStack[n] {
			return fmt.Errorf("cycle through page %d", n)
		}
		if reach[n] {
			return nil
		}
		onStack[n] = true
		for _, r := range p.Refs {
			if err := visit(r); err != nil {
				return err
			}
		}
		onStack[n] = false
		reach[n] = true
		return nil
	}
	if err := visit(f.Root); err != nil {
		return nil, err
	}
	return reach, nil
}

// writeRelocTable publishes the relocation table atomically (temp file,
// fsync, rename). Format: one "old new" pair per line, old ascending.
func writeRelocTable(path string, table *RelocTable) error {
	olds := make([]uint32, 0, len(table.OldToNew))
	for old := range table.OldToNew {
		olds = append(olds, old)
	}
	sort.Slice(olds, func(i, j int) bool { return olds[i] < olds[j] })

	var buf bytes.Buffer
	buf.WriteString("# pagereloc v1: old_page new_page\n")
	for _, old := range olds {
		fmt.Fprintf(&buf, "%d %d\n", old, table.OldToNew[old])
	}

	tmp := path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := out.Write(buf.Bytes()); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(path)
	return nil
}

// syncDir best-effort fsyncs the directory containing path so a rename
// survives a crash.
func syncDir(path string) {
	dir := "."
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				dir = "/"
			} else {
				dir = path[:i]
			}
			break
		}
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
