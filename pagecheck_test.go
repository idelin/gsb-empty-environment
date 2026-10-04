package pagecheck

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func rec(key int64, seq uint32) Record {
	return Record{Key: key, Seq: seq, Value: fmt.Sprintf("v-%d-%d", key, seq)}
}

// buildPage creates a page whose summary is consistent with recs.
func buildPage(id, next PageID, recs ...Record) Page {
	p := Page{ID: id, Records: recs}
	p.Summary.NextID = next
	p.Summary.Count = uint32(len(recs))
	if len(recs) > 0 {
		lo, hi := recs[0].Key, recs[0].Key
		for _, r := range recs[1:] {
			if r.Key < lo {
				lo = r.Key
			}
			if r.Key > hi {
				hi = r.Key
			}
		}
		p.Summary.MinKey, p.Summary.MaxKey = lo, hi
	}
	return p
}

func issueKinds(rep *Report) []IssueKind {
	var kinds []IssueKind
	for _, i := range rep.Issues {
		kinds = append(kinds, i.Kind)
	}
	return kinds
}

func hasKind(rep *Report, k IssueKind) bool {
	for _, i := range rep.Issues {
		if i.Kind == k {
			return true
		}
	}
	return false
}

// Duplicate keys are legal as long as their explicit Seq order is
// non-decreasing, both within a page and across a page boundary.
func TestValidFileWithDuplicateKeys(t *testing.T) {
	f := &File{Pages: []Page{
		buildPage(1, 2, rec(1, 0), rec(2, 0), rec(2, 1), rec(3, 0)),
		buildPage(2, NilPage, rec(3, 1), rec(3, 2), rec(4, 0)),
	}}
	rep := Check(f)
	if !rep.OK() {
		t.Fatalf("expected clean report, got %v", rep.Issues)
	}
	if len(rep.BadPages) != 0 {
		t.Fatalf("expected no bad pages, got %v", rep.BadPages)
	}
	recs, err := Scan(f)
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if len(recs) != 7 {
		t.Fatalf("expected 7 records, got %d", len(recs))
	}
	for i := 1; i < len(recs); i++ {
		if lessRecord(recs[i], recs[i-1]) {
			t.Fatalf("scan order broken at %d: %v after %v", i, recs[i], recs[i-1])
		}
	}
	got, err := Get(f, 3)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if len(got) != 3 || got[0].Seq != 0 || got[1].Seq != 1 || got[2].Seq != 2 {
		t.Fatalf("Get(3) returned %v, want seqs 0,1,2 across the page boundary", got)
	}
}

// Duplicate keys whose explicit Seq order decreases are illegal.
func TestDuplicateKeysOutOfExplicitOrder(t *testing.T) {
	f := &File{Pages: []Page{
		buildPage(1, NilPage, rec(2, 1), rec(2, 0)),
	}}
	rep := Check(f)
	if !hasKind(rep, IssueLocalOrder) {
		t.Fatalf("expected local order issue, got %v", rep.Issues)
	}
	if !reflect.DeepEqual(rep.BadPages, []PageID{1}) {
		t.Fatalf("bad pages = %v, want [1]", rep.BadPages)
	}
}

func TestCountMismatch(t *testing.T) {
	p2 := buildPage(2, NilPage, rec(5, 0), rec(6, 0))
	p2.Summary.Count = 3 // summary claims one more record than stored
	f := &File{Pages: []Page{
		buildPage(1, 2, rec(1, 0), rec(2, 0)),
		p2,
	}}
	rep := Check(f)
	if !hasKind(rep, IssueCountMismatch) {
		t.Fatalf("expected count mismatch, got %v", rep.Issues)
	}
	if !reflect.DeepEqual(rep.BadPages, []PageID{2}) {
		t.Fatalf("bad pages = %v, want [2]", rep.BadPages)
	}
}

func TestMinMaxMismatch(t *testing.T) {
	p1 := buildPage(1, 2, rec(1, 0), rec(4, 0))
	p1.Summary.MinKey = 0 // actual min is 1
	p2 := buildPage(2, NilPage, rec(5, 0), rec(9, 0))
	p2.Summary.MaxKey = 10 // actual max is 9
	f := &File{Pages: []Page{p1, p2}}
	rep := Check(f)
	if !hasKind(rep, IssueMinMismatch) || !hasKind(rep, IssueMaxMismatch) {
		t.Fatalf("expected min and max mismatch, got %v", rep.Issues)
	}
	if !reflect.DeepEqual(rep.BadPages, []PageID{1, 2}) {
		t.Fatalf("bad pages = %v, want [1 2]", rep.BadPages)
	}
}

func TestLocalOrderViolation(t *testing.T) {
	f := &File{Pages: []Page{
		buildPage(1, NilPage, rec(1, 0), rec(3, 0), rec(2, 0)),
	}}
	rep := Check(f)
	if !hasKind(rep, IssueLocalOrder) {
		t.Fatalf("expected local order issue, got %v", rep.Issues)
	}
	// The summary still matches the contents, so only the ordering
	// issue is reported and only page 1 is bad.
	if len(rep.Issues) != 1 {
		t.Fatalf("expected exactly one issue, got %v", rep.Issues)
	}
	if !reflect.DeepEqual(rep.BadPages, []PageID{1}) {
		t.Fatalf("bad pages = %v, want [1]", rep.BadPages)
	}
}

// An inversion between two otherwise clean pages is attributed to
// exactly one of them: the minimal bad page set has size one.
func TestCrossPageInversion(t *testing.T) {
	f := &File{Pages: []Page{
		buildPage(1, 2, rec(1, 0), rec(5, 0)),
		buildPage(2, NilPage, rec(4, 0), rec(9, 0)),
	}}
	rep := Check(f)
	if !hasKind(rep, IssueInversion) {
		t.Fatalf("expected inversion, got %v", rep.Issues)
	}
	if len(rep.BadPages) != 1 {
		t.Fatalf("minimal bad set = %v, want exactly one page", rep.BadPages)
	}
	if rep.BadPages[0] != 1 && rep.BadPages[0] != 2 {
		t.Fatalf("bad page %d is not part of the inversion", rep.BadPages[0])
	}
}

// When one endpoint of an inversion is already bad for its own
// reasons, the inversion adds nothing to the minimal bad page set.
func TestInversionCoveredByOtherFault(t *testing.T) {
	p1 := buildPage(1, 2, rec(1, 0), rec(5, 0))
	p1.Summary.Count = 7 // page 1 is bad on its own
	f := &File{Pages: []Page{
		p1,
		buildPage(2, NilPage, rec(4, 0), rec(9, 0)),
	}}
	rep := Check(f)
	if !hasKind(rep, IssueCountMismatch) || !hasKind(rep, IssueInversion) {
		t.Fatalf("expected count mismatch and inversion, got %v", rep.Issues)
	}
	if !reflect.DeepEqual(rep.BadPages, []PageID{1}) {
		t.Fatalf("bad pages = %v, want [1] only", rep.BadPages)
	}
}

func TestCyclicLink(t *testing.T) {
	f := &File{Pages: []Page{
		buildPage(1, 2, rec(1, 0)),
		buildPage(2, 1, rec(2, 0)), // points back to page 1
	}}
	rep := Check(f)
	if !hasKind(rep, IssueCycle) {
		t.Fatalf("expected cycle, got %v", rep.Issues)
	}
	// Walking 1 -> 2 -> 1, page 2's next pointer closes the loop.
	if !reflect.DeepEqual(rep.BadPages, []PageID{2}) {
		t.Fatalf("bad pages = %v, want [2]", rep.BadPages)
	}
}

func TestSelfLoop(t *testing.T) {
	f := &File{Pages: []Page{
		buildPage(1, 1, rec(1, 0)),
	}}
	rep := Check(f)
	if !hasKind(rep, IssueCycle) {
		t.Fatalf("expected cycle, got %v", rep.Issues)
	}
	if !reflect.DeepEqual(rep.BadPages, []PageID{1}) {
		t.Fatalf("bad pages = %v, want [1]", rep.BadPages)
	}
}

func TestDanglingNext(t *testing.T) {
	f := &File{Pages: []Page{
		buildPage(1, 99, rec(1, 0)),
	}}
	rep := Check(f)
	if !hasKind(rep, IssueDanglingNext) {
		t.Fatalf("expected dangling next, got %v", rep.Issues)
	}
	if !reflect.DeepEqual(rep.BadPages, []PageID{1}) {
		t.Fatalf("bad pages = %v, want [1]", rep.BadPages)
	}
}

// Queries must refuse to answer after validation failure instead of
// silently skipping the bad pages.
func TestQueryDoesNotSilentlySkipBadPages(t *testing.T) {
	p2 := buildPage(2, 3, rec(5, 0))
	p2.Summary.Count = 2
	f := &File{Pages: []Page{
		buildPage(1, 2, rec(1, 0)),
		p2,
		buildPage(3, NilPage, rec(9, 0)),
	}}

	recs, err := Scan(f)
	if err == nil {
		t.Fatalf("Scan returned %v records despite validation failure", recs)
	}
	if recs != nil {
		t.Fatalf("Scan returned partial data %v", recs)
	}
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error is %T, want *ValidationError", err)
	}
	if !reflect.DeepEqual(verr.Report.BadPages, []PageID{2}) {
		t.Fatalf("bad pages = %v, want [2]", verr.Report.BadPages)
	}

	if _, err := Get(f, 5); err == nil {
		t.Fatal("Get succeeded despite validation failure")
	}

	// The tolerant variant is explicit: it returns both the surviving
	// records and the report naming the skipped bad page.
	recs, rep := ScanTolerant(f)
	if rep.OK() {
		t.Fatal("ScanTolerant report should not be OK")
	}
	if len(recs) != 2 || recs[0].Key != 1 || recs[1].Key != 9 {
		t.Fatalf("ScanTolerant returned %v, want keys 1 and 9", recs)
	}
}

// The checker works on file bytes alone: a corrupted summary survives
// a round trip and is still detected after decoding.
func TestCodecRoundTripAndIndependentCheck(t *testing.T) {
	good := &File{Pages: []Page{
		buildPage(1, 2, rec(1, 0), rec(2, 0), rec(2, 1)),
		buildPage(2, NilPage, rec(3, 0)),
	}}
	var buf bytes.Buffer
	if err := WriteFile(&buf, good); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	back, err := ReadFile(&buf)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !reflect.DeepEqual(good, back) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", back, good)
	}
	if rep := Check(back); !rep.OK() {
		t.Fatalf("clean file failed check: %v", rep.Issues)
	}

	bad := &File{Pages: []Page{
		buildPage(1, 2, rec(1, 0), rec(2, 0)),
		buildPage(2, NilPage, rec(0, 0)), // inversion, and fix nothing
	}}
	bad.Pages[0].Summary.MaxKey = 8 // summary lies about contents
	buf.Reset()
	if err := WriteFile(&buf, bad); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	back, err = ReadFile(&buf)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	rep := Check(back)
	if !hasKind(rep, IssueMaxMismatch) || !hasKind(rep, IssueInversion) {
		t.Fatalf("expected max mismatch and inversion, got %v", rep.Issues)
	}
	if len(rep.BadPages) != 1 || rep.BadPages[0] != 1 {
		t.Fatalf("bad pages = %v, want [1]", rep.BadPages)
	}
}
