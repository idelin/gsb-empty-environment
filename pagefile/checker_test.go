package pagefile

import (
	"bytes"
	"reflect"
	"testing"
)

// rec builds a record with a throwaway value.
func rec(key int64) Record { return Record{Key: key, Value: "v"} }

// goodHeader derives the correct summary for records; tests then mutate the
// fields they want to corrupt.
func goodHeader(id, next uint32, records []Record) Header {
	h := Header{PageID: id, NextPage: next, Count: uint32(len(records))}
	if len(records) > 0 {
		h.MinKey, h.MaxKey = records[0].Key, records[0].Key
		for _, r := range records {
			if r.Key < h.MinKey {
				h.MinKey = r.Key
			}
			if r.Key > h.MaxKey {
				h.MaxKey = r.Key
			}
		}
	}
	return h
}

func build(t *testing.T, head uint32, pages []Page) *File {
	t.Helper()
	var buf bytes.Buffer
	if err := EncodeFile(&buf, head, pages); err != nil {
		t.Fatalf("encode: %v", err)
	}
	f, err := DecodeFile(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return f
}

func kinds(rep *Report) []ViolationKind {
	var out []ViolationKind
	for _, v := range rep.Violations {
		out = append(out, v.Kind)
	}
	return out
}

// A healthy three-page file, including duplicate keys within a page, across
// a page boundary, and an empty-range boundary, must pass cleanly.
func TestValidFileWithDuplicates(t *testing.T) {
	r1 := []Record{rec(1), rec(2), rec(2), rec(3)}
	r2 := []Record{rec(3), rec(3), rec(4)}
	r3 := []Record{rec(4), rec(9)}
	p1 := Page{Header: goodHeader(1, 2, r1), Records: r1}
	p2 := Page{Header: goodHeader(2, 3, r2), Records: r2}
	p3 := Page{Header: goodHeader(3, NilPage, r3), Records: r3}
	f := build(t, 1, []Page{p1, p2, p3})

	if rep := Check(f); !rep.OK() {
		t.Fatalf("expected OK, got %v", rep.Violations)
	}
	got, err := Get(f, 3)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 3 { // 2+2+... duplicates across pages 1 and 2: keys 3,3 on p1? no: p1 has one 3, p2 has two
		t.Fatalf("expected 3 records for key 3, got %d", len(got))
	}
	all, err := Scan(f, 2, 4)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	wantKeys := []int64{2, 2, 3, 3, 3, 4, 4}
	var gotKeys []int64
	for _, r := range all {
		gotKeys = append(gotKeys, r.Key)
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("scan keys = %v, want %v (explicit duplicate order must be preserved)", gotKeys, wantKeys)
	}
}

func TestLocalOrderViolation(t *testing.T) {
	r := []Record{rec(1), rec(5), rec(3)}
	p := Page{Header: goodHeader(7, NilPage, r), Records: r}
	rep := Check(build(t, 7, []Page{p}))
	if rep.OK() || kinds(rep)[0] != LocalOrder {
		t.Fatalf("expected local-order violation, got %v", rep.Violations)
	}
	if !reflect.DeepEqual(rep.BadPages, []uint32{7}) {
		t.Fatalf("bad pages = %v, want [7]", rep.BadPages)
	}
}

func TestMinMaxMismatch(t *testing.T) {
	r := []Record{rec(10), rec(20)}
	h := goodHeader(1, NilPage, r)
	h.MinKey = 5 // lie
	p := Page{Header: h, Records: r}
	rep := Check(build(t, 1, []Page{p}))
	if len(rep.Violations) != 1 || rep.Violations[0].Kind != MinMismatch {
		t.Fatalf("expected min-mismatch, got %v", rep.Violations)
	}

	h2 := goodHeader(1, NilPage, r)
	h2.MaxKey = 99 // lie
	p2 := Page{Header: h2, Records: r}
	rep2 := Check(build(t, 1, []Page{p2}))
	if len(rep2.Violations) != 1 || rep2.Violations[0].Kind != MaxMismatch {
		t.Fatalf("expected max-mismatch, got %v", rep2.Violations)
	}
}

func TestCountMismatch(t *testing.T) {
	r := []Record{rec(1), rec(2), rec(3)}
	h := goodHeader(4, NilPage, r)
	h.Count = 5 // lie: framing still works because RecordBytes drives parsing
	p := Page{Header: h, Records: r}
	f := build(t, 4, []Page{p})
	if got := len(f.Pages[0].Records); got != 3 {
		t.Fatalf("decoded %d records, want 3 (framing must not depend on Count)", got)
	}
	rep := Check(f)
	if len(rep.Violations) != 1 || rep.Violations[0].Kind != CountMismatch {
		t.Fatalf("expected count-mismatch, got %v", rep.Violations)
	}
	if !reflect.DeepEqual(rep.BadPages, []uint32{4}) {
		t.Fatalf("bad pages = %v, want [4]", rep.BadPages)
	}
}

func TestCrossPageInversion(t *testing.T) {
	r1 := []Record{rec(1), rec(10)}
	r2 := []Record{rec(5), rec(8)}
	r3 := []Record{rec(9), rec(12)}
	p1 := Page{Header: goodHeader(1, 2, r1), Records: r1}
	p2 := Page{Header: goodHeader(2, 3, r2), Records: r2}
	p3 := Page{Header: goodHeader(3, NilPage, r3), Records: r3}
	rep := Check(build(t, 1, []Page{p1, p2, p3}))
	if len(rep.Violations) != 1 || rep.Violations[0].Kind != CrossPageInversion {
		t.Fatalf("expected cross-page-inversion, got %v", rep.Violations)
	}
	// Both endpoints are implicated (either side could be wrong), but the
	// innocent neighbor page 3 must stay out of the bad set.
	if !reflect.DeepEqual(rep.BadPages, []uint32{1, 2}) {
		t.Fatalf("bad pages = %v, want [1 2]", rep.BadPages)
	}
}

func TestCycle(t *testing.T) {
	r1 := []Record{rec(1)}
	r2 := []Record{rec(2)}
	r3 := []Record{rec(3)}
	p1 := Page{Header: goodHeader(1, 2, r1), Records: r1}
	p2 := Page{Header: goodHeader(2, 3, r2), Records: r2}
	p3 := Page{Header: goodHeader(3, 2, r3), Records: r3} // loops back to 2
	rep := Check(build(t, 1, []Page{p1, p2, p3}))
	if len(rep.Violations) != 1 || rep.Violations[0].Kind != Cycle {
		t.Fatalf("expected cycle, got %v", rep.Violations)
	}
	if !reflect.DeepEqual(rep.BadPages, []uint32{2, 3}) {
		t.Fatalf("bad pages = %v, want cycle members [2 3]", rep.BadPages)
	}
}

func TestBrokenLink(t *testing.T) {
	r := []Record{rec(1)}
	p := Page{Header: goodHeader(1, 42, r), Records: r} // page 42 does not exist
	rep := Check(build(t, 1, []Page{p}))
	if len(rep.Violations) != 1 || rep.Violations[0].Kind != BrokenLink {
		t.Fatalf("expected broken-link, got %v", rep.Violations)
	}
}

// The bad page set must be minimal: one corrupt page among many must not
// condemn its neighbors.
func TestMinimalBadPageSet(t *testing.T) {
	var pages []Page
	for i := uint32(1); i <= 5; i++ {
		r := []Record{rec(int64(i * 10))}
		next := i + 1
		if i == 5 {
			next = NilPage
		}
		pages = append(pages, Page{Header: goodHeader(i, next, r), Records: r})
	}
	pages[2].Header.Count = 99 // corrupt only page 3
	rep := Check(build(t, 1, pages))
	if !reflect.DeepEqual(rep.BadPages, []uint32{3}) {
		t.Fatalf("bad pages = %v, want exactly [3]", rep.BadPages)
	}
}

// Queries must refuse to run on a failed file rather than silently skipping
// the bad pages.
func TestQueryRefusesAfterValidationFailure(t *testing.T) {
	r1 := []Record{rec(1), rec(2)}
	r2 := []Record{rec(3), rec(4)}
	p1 := Page{Header: goodHeader(1, 2, r1), Records: r1}
	h2 := goodHeader(2, NilPage, r2)
	h2.Count = 0 // corrupt
	p2 := Page{Header: h2, Records: r2}
	f := build(t, 1, []Page{p1, p2})

	if _, err := Get(f, 1); err == nil {
		t.Fatal("Get must fail on invalid file, not silently skip bad pages")
	} else if ve, ok := err.(*ValidationError); !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	} else if !reflect.DeepEqual(ve.Report.BadPages, []uint32{2}) {
		t.Fatalf("report bad pages = %v, want [2]", ve.Report.BadPages)
	}
	if _, err := Scan(f, 1, 4); err == nil {
		t.Fatal("Scan must fail on invalid file")
	}
}
