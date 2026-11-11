package structmut

import (
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
)

// schemaForTests builds:
//   - hdr: body carrying a u16 length of payload and a u32 absolute offset
//     of payload, aligned to 4;
//   - payload: free-form body, aligned to 4;
//   - back: 1-byte signed rel offset pointing at hdr, placed after payload.
func schemaForTests() *Schema {
	return &Schema{Sections: []*Section{
		{
			ID:    "hdr",
			Body:  []byte{0, 0, 0, 0, 0, 'p', 0},
			Align: 4,
			Fields: []Field{
				{ID: "len", Offset: 0, Kind: KindLength, Width: 2, Target: "payload"},
				{ID: "abs", Offset: 2, Kind: KindAbsOffset, Width: 4, Target: "payload"},
			},
		},
		{ID: "payload", Body: []byte("AB"), Align: 4},
		{
			ID:     "back",
			Body:   []byte{0},
			Align:  1,
			After:  []SectionID{"payload"},
			Fields: []Field{{ID: "rel", Offset: 0, Kind: KindRelOffset, Width: 1, Target: "hdr"}},
		},
	}}
}

func snapshot(sch *Schema) []*Section {
	out := make([]*Section, len(sch.Sections))
	for i, sec := range sch.Sections {
		out[i] = &Section{
			ID: sec.ID, Body: append([]byte(nil), sec.Body...), Align: sec.Align,
			After: append([]SectionID(nil), sec.After...), Fields: append([]Field(nil), sec.Fields...),
		}
	}
	return out
}

func equalSnapshot(a, b []*Section) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.Align != y.Align || string(x.Body) != string(y.Body) ||
			len(x.After) != len(y.After) || len(x.Fields) != len(y.Fields) {
			return false
		}
		for j := range x.After {
			if x.After[j] != y.After[j] {
				return false
			}
		}
		for j := range x.Fields {
			if x.Fields[j] != y.Fields[j] {
				return false
			}
		}
	}
	return true
}

func decodeAt(blob []byte, start, off int, w Width) uint64 {
	b := blob[start+off : start+off+int(w)]
	switch w {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(binary.LittleEndian.Uint16(b))
	case 4:
		return uint64(binary.LittleEndian.Uint32(b))
	default:
		return binary.LittleEndian.Uint64(b)
	}
}

func findInfo(r *Report, id SectionID) SectionInfo {
	for _, s := range r.Sections {
		if s.ID == id {
			return s
		}
	}
	panic("missing section " + id)
}

func TestMaterializeAndRoundTrip(t *testing.T) {
	sch := schemaForTests()
	res, err := sch.Materialize()
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	report, err := Read(res.Blob, res.Schema)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	hdr := findInfo(report, "hdr")
	payload := findInfo(report, "payload")
	if got := decodeAt(res.Blob, hdr.Start, 0, 2); got != uint64(payload.Length) {
		t.Fatalf("length field = %d, want %d", got, payload.Length)
	}
	if got := decodeAt(res.Blob, hdr.Start, 2, 4); got != uint64(payload.Start) {
		t.Fatalf("abs field = %d, want %d", got, payload.Start)
	}
	back := findInfo(report, "back")
	rel := int8(decodeAt(res.Blob, back.Start, 0, 1))
	if int(rel) != hdr.Start-back.Start {
		t.Fatalf("rel field = %d, want %d", rel, hdr.Start-back.Start)
	}
}

func TestInsertRelocatesFieldsAndReferences(t *testing.T) {
	sch := schemaForTests()
	before := snapshot(sch)

	res, err := sch.Apply(Insert("payload", 0, []byte("XYZ")))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	report, err := Read(res.Blob, res.Schema)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	payload := findInfo(report, "payload")
	if payload.Length != 5 {
		t.Fatalf("payload length = %d, want 5", payload.Length)
	}
	hdr := findInfo(report, "hdr")
	if got := decodeAt(res.Blob, hdr.Start, 0, 2); got != 5 {
		t.Fatalf("length field = %d, want 5", got)
	}
	if got := decodeAt(res.Blob, hdr.Start, 2, 4); got != uint64(payload.Start) {
		t.Fatalf("abs field = %d, want %d", got, payload.Start)
	}
	if !equalSnapshot(snapshot(sch), before) {
		t.Fatal("original schema mutated by successful Apply")
	}
}

func TestDeleteShrinksAndRelocates(t *testing.T) {
	sch := schemaForTests()
	res, err := sch.Apply(Delete("payload", 0, 2))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	report, err := Read(res.Blob, res.Schema)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := findInfo(report, "payload").Length; got != 0 {
		t.Fatalf("payload length = %d, want 0", got)
	}
}

func TestInsertInsideFieldRejected(t *testing.T) {
	sch := schemaForTests()
	if _, err := sch.Apply(Insert("hdr", 3, []byte{1})); !errors.Is(err, ErrFieldOverlap) {
		t.Fatalf("err = %v, want ErrFieldOverlap", err)
	}
}

func TestDeleteFieldRangeRejected(t *testing.T) {
	sch := schemaForTests()
	if _, err := sch.Apply(Delete("hdr", 0, 2)); !errors.Is(err, ErrFieldOverlap) {
		t.Fatalf("err = %v, want ErrFieldOverlap", err)
	}
}

func TestLengthOverflowRejectsCandidate(t *testing.T) {
	sch := schemaForTests()
	before := snapshot(sch)
	_, err := sch.Apply(Insert("payload", 2, make([]byte, 1<<16)))
	if !errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("err = %v, want ErrLengthOverflow", err)
	}
	if !equalSnapshot(snapshot(sch), before) {
		t.Fatal("rejected candidate mutated original schema")
	}
}

func TestAbsOffsetOverflowRejectsCandidate(t *testing.T) {
	sch := &Schema{Sections: []*Section{
		{
			ID:     "h",
			Body:   make([]byte, 256),
			Align:  1,
			Fields: []Field{{ID: "off", Offset: 0, Kind: KindAbsOffset, Width: 1, Target: "tail"}},
		},
		{ID: "tail", Body: make([]byte, 300), Align: 1},
	}}
	if _, err := sch.Materialize(); !errors.Is(err, ErrOffsetOverflow) {
		t.Fatalf("err = %v, want ErrOffsetOverflow", err)
	}
}

func TestNegativeRelativeOffset(t *testing.T) {
	sch := schemaForTests()
	res, err := sch.Apply(Insert("payload", 0, []byte("ZZZZZZ")))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	report, err := Read(res.Blob, res.Schema)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var rel Reference
	for _, r := range report.References {
		if r.Kind == KindRelOffset {
			rel = r
		}
	}
	if rel.Value >= 0 {
		t.Fatalf("rel value = %d, want negative", rel.Value)
	}
	if int(rel.Value) != findInfo(report, "hdr").Start-rel.Position {
		t.Fatal("negative relative offset does not resolve to target")
	}
}

func TestZeroLengthSection(t *testing.T) {
	sch := &Schema{Sections: []*Section{
		{
			ID:    "h",
			Body:  []byte{0, 0, 0},
			Align: 1,
			Fields: []Field{
				{ID: "len", Offset: 0, Kind: KindLength, Width: 2, Target: "empty"},
				{ID: "abs", Offset: 2, Kind: KindAbsOffset, Width: 1, Target: "empty"},
			},
		},
		{ID: "empty", Body: nil, Align: 2},
	}}
	res, err := sch.Materialize()
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	report, err := Read(res.Blob, res.Schema)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(report.ZeroLength) != 1 || report.ZeroLength[0] != "empty" {
		t.Fatalf("zero length report = %v", report.ZeroLength)
	}
}

func TestDuplicateReferencesAllowed(t *testing.T) {
	sch := &Schema{Sections: []*Section{
		{
			ID:    "h",
			Body:  make([]byte, 4),
			Align: 1,
			Fields: []Field{
				{ID: "a", Offset: 0, Kind: KindLength, Width: 2, Target: "d"},
				{ID: "b", Offset: 2, Kind: KindLength, Width: 2, Target: "d"},
			},
		},
		{ID: "d", Body: []byte{1, 2, 3}, Align: 1},
	}}
	res, err := sch.Materialize()
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	report, err := Read(res.Blob, res.Schema)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if report.TargetCounts["d"] != 2 {
		t.Fatalf("target count = %d, want 2", report.TargetCounts["d"])
	}
}

func TestReferenceCycleAllowedButLayoutCycleRejected(t *testing.T) {
	// Reference cycle: h.len -> tail and tail.rel -> h. No layout dependency.
	sch := &Schema{Sections: []*Section{
		{
			ID:     "h",
			Body:   make([]byte, 2),
			Align:  1,
			Fields: []Field{{ID: "len", Offset: 0, Kind: KindLength, Width: 2, Target: "tail"}},
		},
		{
			ID:     "tail",
			Body:   make([]byte, 1),
			Align:  1,
			Fields: []Field{{ID: "rel", Offset: 0, Kind: KindRelOffset, Width: 1, Target: "h"}},
		},
	}}
	if _, err := sch.Materialize(); err != nil {
		t.Fatalf("reference cycle must be allowed, got %v", err)
	}

	cyclic := schemaForTests()
	cyclic.Sections[0].After = []SectionID{"back"}
	// Close the ring: hdr -> back -> payload -> hdr.
	cyclic.Sections[1].After = []SectionID{"hdr"}
	if _, err := cyclic.Materialize(); !errors.Is(err, ErrLayoutCycle) {
		t.Fatalf("err = %v, want ErrLayoutCycle", err)
	}

	selfCycle := schemaForTests()
	selfCycle.Sections[1].After = []SectionID{"payload"}
	if _, err := selfCycle.Materialize(); !errors.Is(err, ErrLayoutCycle) {
		t.Fatalf("self dependency err = %v, want ErrLayoutCycle", err)
	}
}

func TestReaderDetectsCorruptedBlob(t *testing.T) {
	sch := schemaForTests()
	res, err := sch.Materialize()
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	report, err := Read(res.Blob, res.Schema)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	hdr := findInfo(report, "hdr")
	corrupt := append([]byte(nil), res.Blob...)
	corrupt[hdr.Start] ^= 0xFF
	if _, err := Read(corrupt, res.Schema); !errors.Is(err, ErrVerification) {
		t.Fatalf("err = %v, want ErrVerification", err)
	}
}

func TestFailureLeavesOriginalUntouched(t *testing.T) {
	sch := schemaForTests()
	before := snapshot(sch)
	cases := [][]Op{
		{Insert("hdr", 3, []byte{9})},
		{Delete("hdr", 0, 1)},
		{Insert("payload", 2, make([]byte, 1<<16))},
		{Insert("missing", 0, []byte{1})},
		{nil},
	}
	for _, ops := range cases {
		if _, err := sch.Apply(ops...); err == nil {
			t.Fatalf("ops %v unexpectedly accepted", ops)
		}
		if !equalSnapshot(snapshot(sch), before) {
			t.Fatalf("original schema changed after rejected ops %v", ops)
		}
	}
}

func TestRandomAlwaysVerifies(t *testing.T) {
	sch := schemaForTests()
	rng := rand.New(rand.NewSource(1245))
	for i := 0; i < 200; i++ {
		res, err := sch.Random(rng, 6)
		if err != nil {
			continue
		}
		if _, err := Read(res.Blob, res.Schema); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}
