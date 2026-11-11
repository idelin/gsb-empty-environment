package pdfactions

import (
	"strings"
	"testing"
)

// TestOpenActionAndAdditionalActions verifies document-level and page-level
// action hooks are scanned, including JavaScript interception.
func TestOpenActionAndAdditionalActions(t *testing.T) {
	builder := newPDFBuilder()
	builder.Add(1, dictBody("/Type /Catalog", "/Pages 2 0 R",
		"/OpenAction << /S /JavaScript /JS (boot()) >>"))
	builder.Add(2, dictBody("/Type /Pages", "/Kids [ 3 0 R ]", "/Count 1"))
	builder.Add(3, dictBody("/Type /Page", "/Parent 2 0 R",
		"/AA << /Open << /S /Launch /F (calc.exe) >> >>"))
	data := builder.Commit(Ref{Number: 1})
	doc, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec := &recorder{}
	NewPreviewer(DefaultPolicy()).Preview(doc.Latest(), rec.callbacks())
	if len(rec.targets) != 0 {
		t.Fatalf("expected no targets, got %+v", rec.targets)
	}
	joined := strings.Join(blockedKeys(rec.blocked), ",")
	for _, want := range []string{
		"catalog:OpenAction->JavaScript",
		"page 0 /AA/Open->Launch",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("blocked = %q, want it to contain %q", joined, want)
		}
	}
}

// TestReferenceCycleTerminates points two objects at each other and confirms
// dereference cycle protection returns failure instead of looping.
func TestReferenceCycleTerminates(t *testing.T) {
	builder := newPDFBuilder()
	builder.Add(1, dictBody("/Type /Catalog", "/Pages 2 0 R"))
	builder.Add(2, dictBody("/Type /Pages", "/Kids [ 3 0 R ]", "/Count 1"))
	builder.Add(3, dictBody("/Type /Page", "/Parent 2 0 R", "/Annots [ 10 0 R ]"))
	// 10 references 11 and vice versa through /A, forming an indirection loop.
	builder.Add(10, dictBody("/Subtype /Link", "/A 11 0 R"))
	builder.Add(11, dictBody("/S /GoTo", "/D 10 0 R"))
	data := builder.Commit(Ref{Number: 1})
	doc, err := Open(data)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	rec := &recorder{}
	NewPreviewer(DefaultPolicy()).Preview(doc.Latest(), rec.callbacks())
	if len(rec.targets) != 0 {
		t.Fatalf("cyclic references must not produce targets, got %+v", rec.targets)
	}
	if len(rec.blocked) == 0 {
		t.Fatal("expected a blocked event for the cyclic action reference")
	}
}

// TestEncryptedDocumentRejected ensures encrypted trailers fail closed.
func TestEncryptedDocumentRejected(t *testing.T) {
	builder := newPDFBuilder()
	builder.Add(1, dictBody("/Type /Catalog", "/Pages 2 0 R"))
	builder.Add(2, dictBody("/Type /Pages", "/Kids []", "/Count 0"))
	data := builder.Commit(Ref{Number: 1})
	// Inject an Encrypt entry into the only trailer.
	data = []byte(strings.Replace(string(data),
		"trailer\n<<", "trailer\n<< /Encrypt 99 0 R", 1))
	if _, err := Open(data); err == nil {
		t.Fatal("encrypted document must be rejected")
	}
}
