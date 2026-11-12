package pdfscan

import (
	"bytes"
	"testing"
)

func benignBuilder() *classicBuilder {
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R >>")
	return b
}

func TestCleanDocumentPasses(t *testing.T) {
	report, err := ScanBytes(benignBuilder().build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Blocked() {
		t.Fatalf("clean document blocked: %+v", report.Findings)
	}
}

func TestOpenActionIsRejected(t *testing.T) {
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(5, "<< /S /URI /URI (https://example.test) >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R /OpenAction 5 0 R >>")

	report, err := ScanBytes(b.build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFinding(t, report, 1, FindOpenAction)
}

func TestOpenActionDestinationIsAllowed(t *testing.T) {
	// A plain destination array only changes the initial view, it does not
	// execute an action and must not be flagged.
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R /OpenAction [3 0 R /Fit] >>")

	report, err := ScanBytes(b.build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Blocked() {
		t.Fatalf("destination-only OpenAction blocked: %+v", report.Findings)
	}
}

func TestEscapedNamesAreNormalizedAndRejected(t *testing.T) {
	// "/Open#41ction" -> OpenAction, "/#4cA#75nch" -> LAunch,
	// "/#4aS" -> JS. Escapes use mixed-case hex digits on purpose.
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	// Launch action whose /S hides behind name escapes.
	b.set(7, "<< /#53 /#4cA#75nch /#46 (calc.exe) >>")
	// JavaScript action whose /JS key is escaped.
	b.set(8, "<< /S /#4aava#53cript /#4aS (app.alert(1)) >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R /Open#41ction 7 0 R /Names << /JavaScript [ (x) 8 0 R ] >> >>")

	report, err := ScanBytes(b.build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFinding(t, report, 1, FindOpenAction)
	assertFinding(t, report, 7, FindLaunch)
	assertFinding(t, report, 8, FindJavaScript)
}

func TestPageAndAnnotationActionsAreReachable(t *testing.T) {
	b := newClassicBuilder(1)
	b.set(9, "<< /Type /Annot /Subtype /Link /A << /S /Launch /F (evil.sh) >> >>")
	b.set(3, "<< /Type /Page /Parent 2 0 R /Annots [9 0 R] /AA << /O << /S /SubmitForm /F << /FS (http://evil.test) >> >> >> >>")
	b.set(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.set(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [ << /T (f) /AA << /K << /S /JavaScript /JS (1) >> >> >> ] >> >>")

	report, err := ScanBytes(b.build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFinding(t, report, 9, FindLaunch)
	assertFinding(t, report, 3, FindAdditional)
	assertFinding(t, report, 3, FindSubmitForm)
	// Field AA reaches a JavaScript action (indirect owner gets the number).
	if !hasAny(report, FindJavaScript) {
		t.Fatalf("expected JavaScript finding, got %+v", report.Findings)
	}
}

func TestIncrementalUpdateOldActionIgnored(t *testing.T) {
	// Revision 1: catalog carries an OpenAction pointing at object 5.
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(5, "<< /S /JavaScript /JS (old) >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R /OpenAction 5 0 R >>")
	base := b.build()

	// Revision 2: replace both the catalog and the action with clean ones.
	updated := appendIncrement(base, 1, 4, map[int]string{
		1: "<< /Type /Catalog /Pages 3 0 R >>",
		5: "<< /S /URI /URI (https://safe.example) >>",
	})

	report, err := ScanBytes(updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Blocked() {
		t.Fatalf("superseded action still flagged: %+v", report.Findings)
	}
}

func TestIncrementalUpdateNewActionDetected(t *testing.T) {
	base := benignBuilder().build()
	updated := appendIncrement(base, 1, 4, map[int]string{
		6: "<< /S /Launch /F (malware.bin) >>",
		1: "<< /Type /Catalog /Pages 3 0 R /OpenAction 6 0 R >>",
	})

	report, err := ScanBytes(updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFinding(t, report, 1, FindOpenAction)
	assertFinding(t, report, 6, FindLaunch)
}

func TestIncrementalUpdateFreeingObjectRemovesIt(t *testing.T) {
	// Revision 1 defines a JavaScript action object 6 referenced by the
	// catalog. Revision 2 replaces the catalog with a clean one; the stale
	// object 6 must remain unreachable and therefore produce no finding.
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(6, "<< /S /JavaScript /JS (stale) >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R /OpenAction 6 0 R >>")
	base := b.build()

	updated := appendIncrement(base, 1, 7, map[int]string{
		1: "<< /Type /Catalog /Pages 3 0 R >>",
	})
	report, err := ScanBytes(updated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Blocked() {
		t.Fatalf("unreachable stale action flagged: %+v", report.Findings)
	}
}

func TestCompressedObjectsAreScanned(t *testing.T) {
	report, err := ScanBytes(buildCompressedPDF())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Catalog 20 (compressed) reports OpenAction; action 21 (compressed)
	// reports JavaScript with its real object number.
	assertFinding(t, report, 20, FindOpenAction)
	assertFinding(t, report, 21, FindJavaScript)
}

func TestNonPDFRejected(t *testing.T) {
	if _, err := ScanBytes([]byte("not a pdf at all")); err == nil {
		t.Fatal("expected error for non-PDF input")
	}
}

func TestScanReaderMatchesBytes(t *testing.T) {
	report, err := ScanReader(bytes.NewReader(benignBuilder().build()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Blocked() {
		t.Fatalf("clean document blocked: %+v", report.Findings)
	}
}

func assertFinding(t *testing.T, r *Report, objNum int, kind FindingKind) {
	t.Helper()
	for _, f := range r.Findings {
		if f.ObjectNum == objNum && f.Kind == kind {
			return
		}
	}
	t.Fatalf("missing finding obj=%d kind=%s; got %+v", objNum, kind, r.Findings)
}

func hasAny(r *Report, kind FindingKind) bool {
	for _, f := range r.Findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}
