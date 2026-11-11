package pdfactions

import (
	"fmt"
	"sort"
	"strings"
)

// builderObject is one indirect object as held by the test builder.
type builderObject struct {
	id      ObjectID
	body    string
	freed   bool
	offset  int
	emitted bool
}

// pdfBuilder constructs minimal, valid PDF files. It supports incremental
// updates including freed object numbers that are later reused with a higher
// generation, so tests can exercise revision binding directly.
type pdfBuilder struct {
	objects    map[int]*builderObject
	bytes      []byte
	xrefStarts []int
}

func newPDFBuilder() *pdfBuilder {
	return &pdfBuilder{objects: map[int]*builderObject{}}
}

// Add creates object number with generation 0.
func (b *pdfBuilder) Add(number int, body string) Ref {
	return b.put(number, 0, body)
}

// Update rewrites an existing object at the same generation.
func (b *pdfBuilder) Update(number int, body string) Ref {
	existing, ok := b.objects[number]
	if !ok {
		panic(fmt.Sprintf("pdfBuilder: object %d does not exist", number))
	}
	return b.put(number, existing.id.Generation, body)
}

// Reuse frees number and recreates it with generation+1, as an incremental
// update does when an object number is recycled.
func (b *pdfBuilder) Reuse(number int, body string) Ref {
	existing, ok := b.objects[number]
	if !ok {
		panic(fmt.Sprintf("pdfBuilder: object %d does not exist", number))
	}
	generation := existing.id.Generation
	b.objects[number] = &builderObject{
		id:      ObjectID{Number: number, Generation: generation},
		freed:   true,
		emitted: true,
	}
	return b.put(number, generation+1, body)
}

// Free marks number as deleted without reusing the number.
func (b *pdfBuilder) Free(number int) {
	existing, ok := b.objects[number]
	if !ok {
		panic(fmt.Sprintf("pdfBuilder: object %d does not exist", number))
	}
	b.objects[number] = &builderObject{
		id:      ObjectID{Number: number, Generation: existing.id.Generation},
		freed:   true,
		emitted: true,
	}
}

func (b *pdfBuilder) put(number, generation int, body string) Ref {
	b.objects[number] = &builderObject{
		id:   ObjectID{Number: number, Generation: generation},
		body: body,
	}
	return Ref{Number: number, Generation: generation}
}

// Commit appends a new revision. The first call writes the header and the
// full xref table; later calls emit only changed entries plus a new trailer.
func (b *pdfBuilder) Commit(root Ref) []byte {
	if len(b.bytes) == 0 {
		b.bytes = append(b.bytes, []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")...)
	}
	changed := b.pendingObjects()
	for _, obj := range changed {
		if obj.freed {
			continue
		}
		obj.offset = len(b.bytes)
		b.bytes = append(b.bytes, []byte(fmt.Sprintf("%d %d obj\n%s\nendobj\n", obj.id.Number, obj.id.Generation, obj.body))...)
		obj.emitted = true
	}

	entries := b.xrefEntries()
	xrefStart := len(b.bytes)
	b.bytes = append(b.bytes, []byte("xref\n")...)
	for _, subsection := range groupSubsections(entries) {
		b.bytes = append(b.bytes, []byte(fmt.Sprintf("%d %d\n", subsection.first, len(subsection.rows)))...)
		for _, row := range subsection.rows {
			b.bytes = append(b.bytes, []byte(formatXRefRow(row))...)
		}
	}
	size := b.maxNumber() + 1
	trailer := fmt.Sprintf("trailer\n<< /Size %d /Root %d %d R", size, root.Number, root.Generation)
	if len(b.xrefStarts) > 0 {
		trailer += fmt.Sprintf(" /Prev %d", b.xrefStarts[len(b.xrefStarts)-1])
	}
	trailer += " >>\nstartxref\n" + fmt.Sprintf("%d", xrefStart) + "\n%%EOF\n"
	b.bytes = append(b.bytes, []byte(trailer)...)
	b.xrefStarts = append(b.xrefStarts, xrefStart)
	return append([]byte(nil), b.bytes...)
}

func (b *pdfBuilder) pendingObjects() []*builderObject {
	var out []*builderObject
	for number := range b.objects {
		obj := b.objects[number]
		if obj.emitted {
			continue
		}
		out = append(out, obj)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id.Number < out[j].id.Number })
	return out
}

func (b *pdfBuilder) offsetKnown(offset int) bool {
	return offset >= 0 && offset < len(b.bytes)
}

type xrefRow struct {
	number int
	entry  xrefEntry
}

type xrefSubsection struct {
	first int
	rows  []xrefEntry
}

func (b *pdfBuilder) xrefEntries() []xrefRow {
	rows := map[int]xrefEntry{}
	rows[0] = xrefEntry{offset: 0, generation: 65535, free: true}
	for number, obj := range b.objects {
		if obj.freed {
			rows[number] = xrefEntry{offset: 0, generation: obj.id.Generation + 1, free: true}
			continue
		}
		rows[number] = xrefEntry{offset: obj.offset, generation: obj.id.Generation, free: false}
	}
	var out []xrefRow
	for number := range rows {
		out = append(out, xrefRow{number: number, entry: rows[number]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].number < out[j].number })
	return out
}

func groupSubsections(rows []xrefRow) []xrefSubsection {
	var sections []xrefSubsection
	for _, row := range rows {
		last := len(sections) - 1
		if last >= 0 && sections[last].first+len(sections[last].rows) == row.number {
			sections[last].rows = append(sections[last].rows, row.entry)
			continue
		}
		sections = append(sections, xrefSubsection{first: row.number, rows: []xrefEntry{row.entry}})
	}
	return sections
}

func formatXRefRow(entry xrefEntry) string {
	kind := "n"
	if entry.free {
		kind = "f"
	}
	return fmt.Sprintf("%010d %05d %s \n", entry.offset, entry.generation, kind)
}

func (b *pdfBuilder) maxNumber() int {
	max := 0
	for number := range b.objects {
		if number > max {
			max = number
		}
	}
	return max
}

// dictBody is a tiny helper for building dictionary literals.
func dictBody(pairs ...string) string {
	return "<< " + strings.Join(pairs, " ") + " >>"
}
