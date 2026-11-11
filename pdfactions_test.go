package pdfactions

import (
	"bytes"
	"reflect"
	"strconv"
	"testing"
)

type recorder struct {
	targets []Target
	blocked []BlockedAction
}

func (r *recorder) callbacks() Callbacks {
	return Callbacks{
		OnTarget:  func(t Target) { r.targets = append(r.targets, t) },
		OnBlocked: func(b BlockedAction) { r.blocked = append(r.blocked, b) },
	}
}

func targetKeys(targets []Target) []string {
	keys := make([]string, 0, len(targets))
	for _, target := range targets {
		keys = append(keys, target.Source+"->"+target.View+"@"+strconv.Itoa(target.PageIndex))
	}
	return keys
}

func blockedKeys(blocked []BlockedAction) []string {
	keys := make([]string, 0, len(blocked))
	for _, item := range blocked {
		keys = append(keys, item.Source+"->"+string(item.Type))
	}
	return keys
}

// TestPolicyFilters builds a page with every relevant action category and
// verifies that only in-document navigation reaches the target callback.
func TestPolicyFilters(t *testing.T) {
	builder := newPDFBuilder()
	builder.Add(10, dictBody("/Type /Page", "/Parent 5 0 R",
		"/Annots [ 20 0 R 21 0 R 22 0 R 23 0 R 24 0 R 25 0 R ]"))
	builder.Add(5, dictBody("/Type /Pages", "/Kids [ 10 0 R ]", "/Count 1"))
	builder.Add(1, dictBody("/Type /Catalog", "/Pages 5 0 R"))

	builder.Add(20, dictBody("/Subtype /Link", "/A << /S /GoTo /D [ 10 0 R /Fit ] >>"))
	builder.Add(21, dictBody("/Subtype /Link", "/A << /S /JavaScript /JS (app.alert(1)) >>"))
	builder.Add(22, dictBody("/Subtype /Link", "/A << /S /Launch /F (/usr/bin/open) >>"))
	builder.Add(23, dictBody("/Subtype /Link", "/A << /S /URI /URI (https://example.com) >>"))
	builder.Add(24, dictBody("/Subtype /Link", "/A << /S /Named /N /NextPage >>"))
	builder.Add(25, dictBody("/Subtype /Link", "/A << /S /Named /N /Print >>"))

	data := builder.Commit(Ref{Number: 1})
	doc, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec := &recorder{}
	NewPreviewer(DefaultPolicy()).Preview(doc.Latest(), rec.callbacks())

	if got, want := targetKeys(rec.targets), []string{
		"page 0 annotation 20 0 R->Fit@0",
		"page 0 annotation 24 0 R->Named:NextPage@0",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if got, want := blockedKeys(rec.blocked), []string{
		"page 0 annotation 21 0 R->JavaScript",
		"page 0 annotation 22 0 R->Launch",
		"page 0 annotation 23 0 R->URI",
		"page 0 annotation 25 0 R->Named",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blocked = %v, want %v", got, want)
	}
	if doc.Latest().PageCount() != 1 {
		t.Fatalf("PageCount = %d, want 1", doc.Latest().PageCount())
	}
}

// TestPageTreeAndActionCycles makes the page tree and an action /Next chain
// cyclic. Walking must terminate and each reachable page must appear once.
func TestPageTreeAndActionCycles(t *testing.T) {
	builder := newPDFBuilder()
	builder.Add(1, dictBody("/Type /Catalog", "/Pages 2 0 R"))
	builder.Add(2, dictBody("/Type /Pages", "/Kids [ 3 0 R 4 0 R 2 0 R ]", "/Count 3"))
	builder.Add(3, dictBody("/Type /Page", "/Parent 2 0 R", "/Annots [ 30 0 R ]"))
	builder.Add(4, dictBody("/Type /Page", "/Parent 2 0 R", "/Kids [ 3 0 R ]"))

	// 30 -> 31 -> 32 -> 30 forms an action cycle; each action is otherwise a
	// valid GoTo so termination, not policy, decides the outcome.
	builder.Add(30, dictBody("/Subtype /Link", "/A << /S /GoTo /D [ 3 0 R /Fit ] /Next 31 0 R >>"))
	builder.Add(31, dictBody("/S /GoTo", "/D [ 4 0 R /FitH 100 ]", "/Next 32 0 R"))
	builder.Add(32, dictBody("/S /GoTo", "/D [ 3 0 R /Fit ]", "/Next 30 0 R"))

	data := builder.Commit(Ref{Number: 1})
	doc, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rev := doc.Latest()
	if got, want := rev.PageCount(), 2; got != want {
		t.Fatalf("PageCount = %d, want %d", got, want)
	}
	rec := &recorder{}
	NewPreviewer(DefaultPolicy()).Preview(rev, rec.callbacks())

	if got, want := targetKeys(rec.targets), []string{
		"page 0 annotation 30 0 R->Fit@0",
		"page 0 annotation 30 0 R /Next->FitH@1",
		"page 0 annotation 30 0 R /Next /Next->Fit@0",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if len(rec.blocked) != 0 {
		t.Fatalf("unexpected blocked actions: %+v", rec.blocked)
	}
}

// TestStaleGenerationReference verifies the xref generation check directly:
// a reference to a freed generation cannot resolve to the reused object.
func TestStaleGenerationReference(t *testing.T) {
	builder := newPDFBuilder()
	builder.Add(1, dictBody("/Type /Catalog", "/Pages 2 0 R"))
	builder.Add(2, dictBody("/Type /Pages", "/Kids [ 3 0 R ]", "/Count 1"))
	builder.Add(3, dictBody("/Type /Page", "/Parent 2 0 R", "/Annots [ 10 0 R ]"))
	builder.Add(10, dictBody("/Subtype /Link", "/A << /S /GoTo /D [ 3 0 R /Fit ] >>"))
	builder.Commit(Ref{Number: 1})

	builder.Reuse(10, dictBody("/Subtype /Link", "/A << /S /GoTo /D [ 3 0 R /Fit ] >>"))
	data := builder.Commit(Ref{Number: 1})
	doc, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if value, ok := doc.Latest().resolve(Ref{Number: 10, Generation: 0}); ok {
		t.Fatalf("stale generation resolved to %v", value)
	}
	if _, ok := doc.Latest().resolve(Ref{Number: 10, Generation: 1}); !ok {
		t.Fatal("current generation must resolve")
	}
}

func equalBytes(a, b []byte) bool {
	return bytes.Equal(a, b)
}
