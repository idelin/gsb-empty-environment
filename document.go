package pdfactions

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// xrefEntry is one row of a classic cross-reference table.
type xrefEntry struct {
	offset     int
	generation int
	free       bool
}

// xrefTable is one classic cross-reference section reached from a startxref.
type xrefTable struct {
	start   int
	rows    map[int]xrefEntry
	prev    int
	trailer *Dict
}

// Document is a parsed PDF file together with all of its incremental
// revisions. Revisions are ordered oldest to newest.
type Document struct {
	raw       []byte
	Revisions []*Revision
}

// Revision is one immutable snapshot of the document. All object lookups
// performed through the revision use the cross-reference state visible at
// that snapshot, never data appended by a later update.
type Revision struct {
	Index     int
	doc       *Document
	xref      map[int]xrefEntry
	catalogID ObjectID

	objects map[ObjectID]Value

	pagesOnce bool
	pageIDs   []ObjectID
	pageByID  map[ObjectID]int
}

// Open parses a PDF document and materializes every incremental revision.
// Encrypted documents and cross-reference streams are rejected rather than
// silently interpreted.
func Open(data []byte) (*Document, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("pdfactions: empty document")
	}
	if idx := bytes.Index(data, []byte("%PDF-")); idx < 0 || idx > 1024 {
		return nil, fmt.Errorf("pdfactions: missing PDF header")
	}

	var tables []*xrefTable
	firstStart, err := lastStartXRef(data)
	if err != nil {
		return nil, err
	}
	seenStarts := map[int]bool{}
	start := firstStart
	for start > 0 && !seenStarts[start] {
		seenStarts[start] = true
		tbl, err := parseXRefTable(data, start)
		if err != nil {
			return nil, err
		}
		tables = append(tables, tbl)
		start = tbl.prev
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("pdfactions: document has no cross-reference table")
	}

	if _, encrypted := tables[0].trailer.Get(Name("Encrypt")); encrypted {
		return nil, fmt.Errorf("pdfactions: encrypted documents are not supported")
	}
	rootValue, ok := tables[0].trailer.Get(Name("Root"))
	if !ok {
		return nil, fmt.Errorf("pdfactions: trailer lacks Root entry")
	}
	rootRef, ok := rootValue.(Ref)
	if !ok {
		return nil, fmt.Errorf("pdfactions: trailer Root is not an indirect reference")
	}

	doc := &Document{raw: data}
	merged := map[int]xrefEntry{}
	for i := len(tables) - 1; i >= 0; i-- {
		for number, entry := range tables[i].rows {
			merged[number] = entry
		}
		snapshot := make(map[int]xrefEntry, len(merged))
		for number, entry := range merged {
			snapshot[number] = entry
		}
		rev := &Revision{
			Index:     len(doc.Revisions),
			doc:       doc,
			xref:      snapshot,
			catalogID: rootRef.ID(),
			objects:   make(map[ObjectID]Value),
			pageByID:  make(map[ObjectID]int),
		}
		doc.Revisions = append(doc.Revisions, rev)
	}
	return doc, nil
}

// Latest returns the most recent revision.
func (d *Document) Latest() *Revision {
	if len(d.Revisions) == 0 {
		return nil
	}
	return d.Revisions[len(d.Revisions)-1]
}

// Catalog resolves the document catalog within this revision.
func (r *Revision) Catalog() (*Dict, error) {
	dict, ok := asDict(r, Ref(r.catalogID), nil)
	if !ok {
		return nil, fmt.Errorf("pdfactions: catalog %s cannot be resolved in revision %d", r.catalogID, r.Index)
	}
	return dict, nil
}

// resolve looks up an indirect object through this revision's cross-reference
// table. The generation recorded by the xref must match the reference, which
// guarantees a dangling reference (for example to a reused object number)
// fails instead of silently reading a different object.
func (r *Revision) resolve(ref Ref) (Value, bool) {
	entry, ok := r.xref[ref.Number]
	if !ok || entry.free {
		return nil, false
	}
	if entry.generation != ref.Generation {
		return nil, false
	}
	id := ObjectID{Number: ref.Number, Generation: ref.Generation}
	if cached, ok := r.objects[id]; ok {
		return cached, true
	}
	if entry.offset < 0 || entry.offset >= len(r.doc.raw) {
		return nil, false
	}
	p := &parser{data: r.doc.raw, pos: entry.offset}
	header, ok := p.parseIndirectHeader()
	if !ok || header != id {
		return nil, false
	}
	value, err := p.parseValue()
	if err != nil {
		return nil, false
	}
	r.objects[id] = value
	return value, true
}

// dereference follows indirect references while staying inside this revision.
// The seen set breaks reference cycles regardless of how deep they nest.
func (r *Revision) dereference(value Value, seen map[ObjectID]bool) (Value, bool) {
	for {
		ref, ok := value.(Ref)
		if !ok {
			return value, true
		}
		id := ref.ID()
		if seen[id] {
			return nil, false
		}
		resolved, ok := r.resolve(ref)
		if !ok {
			return nil, false
		}
		if seen == nil {
			seen = map[ObjectID]bool{}
		}
		seen[id] = true
		value = resolved
	}
}

// PageCount walks the page tree using only this revision's xref and returns
// the number of reachable page objects.
func (r *Revision) PageCount() int {
	r.collectPages()
	return len(r.pageIDs)
}

// PageIndex returns the 0-based page index of an indirect page object and
// whether it belongs to this revision's page tree.
func (r *Revision) PageIndex(id ObjectID) (int, bool) {
	r.collectPages()
	index, ok := r.pageByID[id]
	return index, ok
}

// PageID returns the indirect object id of the page at index.
func (r *Revision) PageID(index int) (ObjectID, bool) {
	r.collectPages()
	if index < 0 || index >= len(r.pageIDs) {
		return ObjectID{}, false
	}
	return r.pageIDs[index], true
}

// collectPages walks /Kids transitively. Page-tree cycles are broken with a
// per-revision visited set, so a corrupt document yields each reachable page
// exactly once instead of looping forever.
func (r *Revision) collectPages() {
	if r.pagesOnce {
		return
	}
	r.pagesOnce = true
	catalog, ok := asDict(r, Ref(r.catalogID), nil)
	if !ok {
		return
	}
	rootValue, ok := catalog.Get(Name("Pages"))
	if !ok {
		return
	}
	visited := map[ObjectID]bool{}
	r.walkPageNode(rootValue, visited)
}

func (r *Revision) walkPageNode(value Value, visited map[ObjectID]bool) {
	ref, isRef := value.(Ref)
	id := ref.ID()
	if isRef {
		if visited[id] {
			return
		}
	}
	dict, ok := asDict(r, value, visited)
	if !ok {
		return
	}
	if isRef {
		visited[id] = true
	}
	typeValue, _ := asName(r, dictValue(dict, Name("Type")), nil)
	kidsValue, hasKids := dict.Get(Name("Kids"))
	if typeValue == Name("Page") || (!hasKids && typeValue != Name("Pages")) {
		if isRef {
			if _, exists := r.pageByID[id]; !exists {
				r.pageByID[id] = len(r.pageIDs)
				r.pageIDs = append(r.pageIDs, id)
			}
		}
		return
	}
	kids, ok := asArray(r, kidsValue, nil)
	if !ok {
		return
	}
	for _, kid := range kids {
		r.walkPageNode(kid, visited)
	}
}

func dictValue(dict *Dict, key Name) Value {
	value, _ := dict.Get(key)
	return value
}

// parseXRefTable reads a classic xref table plus its trailer starting at
// offset. Cross-reference streams (a stream object standing in for the table)
// are explicitly rejected.
func parseXRefTable(data []byte, start int) (*xrefTable, error) {
	p := &parser{data: data, pos: start}
	p.skipSpace()
	if !bytes.HasPrefix(data[p.pos:], []byte("xref")) {
		return nil, fmt.Errorf("pdfactions: xref table expected at offset %d", start)
	}
	p.pos += len("xref")
	rows := map[int]xrefEntry{}
	for {
		p.skipSpace()
		line, next := nextLine(data, p.pos)
		fields := strings.Fields(line)
		if len(fields) == 1 && fields[0] == "trailer" {
			break
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("pdfactions: malformed xref subsection header %q", line)
		}
		firstNum, errFirst := strconv.Atoi(fields[0])
		count, errCount := strconv.Atoi(fields[1])
		if errFirst != nil || errCount != nil || count < 0 {
			return nil, fmt.Errorf("pdfactions: malformed xref subsection header %q", line)
		}
		p.pos = next
		for i := 0; i < count; i++ {
			entryLine, entryNext := nextLine(data, p.pos)
			if len(entryLine) < 18 {
				return nil, fmt.Errorf("pdfactions: truncated xref entry %q", entryLine)
			}
			entryFields := strings.Fields(entryLine)
			if len(entryFields) != 3 {
				return nil, fmt.Errorf("pdfactions: invalid xref entry %q", entryLine)
			}
			offset, errOffset := strconv.Atoi(entryFields[0])
			generation, errGen := strconv.Atoi(entryFields[1])
			kind := entryFields[2]
			if errOffset != nil || errGen != nil || (kind != "n" && kind != "f") {
				return nil, fmt.Errorf("pdfactions: invalid xref entry %q", entryLine)
			}
			rows[firstNum+i] = xrefEntry{
				offset:     offset,
				generation: generation,
				free:       kind == "f",
			}
			p.pos = entryNext
		}
	}
	if !bytes.HasPrefix(data[p.pos:], []byte("trailer")) {
		return nil, fmt.Errorf("pdfactions: xref table without trailer")
	}
	p.pos += len("trailer")
	trailerValue, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	trailer, ok := trailerValue.(*Dict)
	if !ok {
		return nil, fmt.Errorf("pdfactions: trailer is not a dictionary")
	}
	if _, isStream := trailerValue.(*Stream); isStream {
		return nil, fmt.Errorf("pdfactions: cross-reference streams are not supported")
	}
	prev := 0
	if prevValue, ok := trailer.Get(Name("Prev")); ok {
		if prevInt, ok := prevValue.(Integer); ok {
			prev = int(prevInt)
		}
	}
	return &xrefTable{start: start, rows: rows, prev: prev, trailer: trailer}, nil
}

// nextLine returns the content of the line at pos (without line ending) and
// the position just after the line ending.
func nextLine(data []byte, pos int) (string, int) {
	start := pos
	for pos < len(data) && data[pos] != '\r' && data[pos] != '\n' {
		pos++
	}
	line := string(data[start:pos])
	if pos < len(data) && data[pos] == '\r' {
		pos++
	}
	if pos < len(data) && data[pos] == '\n' {
		pos++
	}
	return line, pos
}

// lastStartXRef returns the byte offset following the final startxref marker.
func lastStartXRef(data []byte) (int, error) {
	marker := []byte("startxref")
	last := bytes.LastIndex(data, marker)
	if last < 0 {
		return 0, fmt.Errorf("pdfactions: missing startxref")
	}
	p := &parser{data: data, pos: last + len(marker)}
	p.skipSpace()
	value, err := p.parseValue()
	if err != nil {
		return 0, fmt.Errorf("pdfactions: malformed startxref")
	}
	offset, ok := value.(Integer)
	if !ok || offset <= 0 {
		return 0, fmt.Errorf("pdfactions: malformed startxref offset")
	}
	return int(offset), nil
}
