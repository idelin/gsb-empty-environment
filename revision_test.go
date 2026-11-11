package pdfactions

import (
	"reflect"
	"testing"
)

// TestIncrementalReuseAndRevisionBinding is the core security test: an object
// number is freed and reused by a later update, and an action is rewritten.
// Old revisions keep resolving the original targets from their own xref
// generation instead of borrowing the new objects or cache entries.
func TestIncrementalReuseAndRevisionBinding(t *testing.T) {
	builder := newPDFBuilder()
	builder.Add(1, dictBody("/Type /Catalog", "/Pages 2 0 R"))
	builder.Add(2, dictBody("/Type /Pages", "/Kids [ 3 0 R 4 0 R ]", "/Count 2"))
	builder.Add(3, dictBody("/Type /Page", "/Parent 2 0 R", "/Annots [ 10 0 R ]"))
	builder.Add(4, dictBody("/Type /Page", "/Parent 2 0 R"))
	// Revision 0: object 10 is a safe link to page 4 (index 1).
	builder.Add(10, dictBody("/Subtype /Link", "/A << /S /GoTo /D [ 4 0 R /Fit ] >>"))
	rev0 := builder.Commit(Ref{Number: 1})

	// Revision 1: object 10 is deleted, then the number is reused at gen 1
	// for a JavaScript action. The old 10 0 R reference must dangle.
	builder.Reuse(10, dictBody("/Subtype /Link", "/A << /S /JavaScript /JS (evil()) >>"))
	builder.Update(3, dictBody("/Type /Page", "/Parent 2 0 R", "/Annots [ 10 0 R ]"))
	rev1 := builder.Commit(Ref{Number: 1})

	// Revision 2: the reused object becomes safe navigation to page 3.
	builder.Update(10, dictBody("/Subtype /Link", "/A << /S /GoTo /D [ 3 0 R /XYZ 0 800 null ] >>"))
	builder.Update(3, dictBody("/Type /Page", "/Parent 2 0 R", "/Annots [ 10 1 R ]"))
	rev2 := builder.Commit(Ref{Number: 1})

	if !equalBytes(rev0, rev1[:len(rev0)]) {
		t.Fatal("incremental update must preserve the original byte prefix")
	}

	doc0, err := Open(rev0)
	if err != nil {
		t.Fatalf("Open rev0: %v", err)
	}
	doc1, err := Open(rev1)
	if err != nil {
		t.Fatalf("Open rev1: %v", err)
	}
	doc2, err := Open(rev2)
	if err != nil {
		t.Fatalf("Open rev2: %v", err)
	}
	if len(doc0.Revisions) != 1 || len(doc1.Revisions) != 2 || len(doc2.Revisions) != 3 {
		t.Fatalf("revision counts = %d/%d/%d, want 1/2/3",
			len(doc0.Revisions), len(doc1.Revisions), len(doc2.Revisions))
	}

	// One previewer/cache is shared across revisions to prove cache keys are
	// revision-scoped.
	previewer := NewPreviewer(DefaultPolicy())

	rec0 := &recorder{}
	previewer.Preview(doc0.Latest(), rec0.callbacks())
	if got, want := targetKeys(rec0.targets), []string{"page 0 annotation 10 0 R->Fit@1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rev0 targets = %v, want %v", got, want)
	}

	rec1 := &recorder{}
	previewer.Preview(doc1.Latest(), rec1.callbacks())
	if len(rec1.targets) != 0 {
		t.Fatalf("rev1 must not deliver targets, got %+v", rec1.targets)
	}
	if got, want := blockedKeys(rec1.blocked), []string{"page 0 annotation 10 0 R->"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rev1 blocked = %v, want %v", got, want)
	}

	rec2 := &recorder{}
	previewer.Preview(doc2.Latest(), rec2.callbacks())
	if got, want := targetKeys(rec2.targets), []string{"page 0 annotation 10 1 R->XYZ@0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rev2 targets = %v, want %v", got, want)
	}
	if got := rec2.targets[0].Params; !reflect.DeepEqual(got, []float64{0, 800}) {
		t.Fatalf("rev2 params = %v, want [0 800]", got)
	}

	// Re-preview the oldest revision after newer entries were cached: the old
	// target must be returned unchanged.
	rec0Again := &recorder{}
	previewer.Preview(doc0.Latest(), rec0Again.callbacks())
	if !reflect.DeepEqual(targetKeys(rec0Again.targets), targetKeys(rec0.targets)) {
		t.Fatalf("old revision result changed after newer cache entries: %v",
			targetKeys(rec0Again.targets))
	}

	if _, ok := previewer.cache[cacheKey{revision: 0, id: ObjectID{Number: 10, Generation: 0}}]; !ok {
		t.Fatal("missing revision-0 cache entry for object 10")
	}
	if _, ok := previewer.cache[cacheKey{revision: 2, id: ObjectID{Number: 10, Generation: 1}}]; !ok {
		t.Fatal("missing revision-2 cache entry for reused object 10")
	}
}

// TestNamedDestinations covers the /Dests dictionary and the /Names name
// tree, including a cyclic name-tree node and an undefined name.
func TestNamedDestinations(t *testing.T) {
	builder := newPDFBuilder()
	builder.Add(1, dictBody("/Type /Catalog", "/Pages 2 0 R",
		"/Dests << /direct [ 3 0 R /Fit ] >>",
		"/Names << /Dests 40 0 R >>"))
	builder.Add(2, dictBody("/Type /Pages", "/Kids [ 3 0 R ]", "/Count 1"))
	builder.Add(3, dictBody("/Type /Page", "/Parent 2 0 R", "/Annots [ 30 0 R 31 0 R 32 0 R ]"))
	builder.Add(30, dictBody("/Subtype /Link", "/A << /S /GoTo /D /direct >>"))
	builder.Add(31, dictBody("/Subtype /Link", "/Dest (tree)"))
	builder.Add(32, dictBody("/Subtype /Link", "/A << /S /GoTo /D /missing >>"))
	// Node 40 points at itself through /Kids to prove cycle protection.
	builder.Add(40, dictBody("/Kids [ 41 0 R 40 0 R ]"))
	builder.Add(41, dictBody("/Names [ (tree) [ 3 0 R /FitH 200 ] ]"))

	data := builder.Commit(Ref{Number: 1})
	doc, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec := &recorder{}
	NewPreviewer(DefaultPolicy()).Preview(doc.Latest(), rec.callbacks())

	if got, want := targetKeys(rec.targets), []string{
		"page 0 annotation 30 0 R->Fit@0",
		"page 0 annotation 31 0 R->FitH@0",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if got, want := blockedKeys(rec.blocked), []string{
		"page 0 annotation 32 0 R->GoTo",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blocked = %v, want %v", got, want)
	}
}
