package pathverify

import (
	"testing"
	"time"
)

func TestInvalidInputs(t *testing.T) {
	if _, err := Verify(Options{}); err == nil || !IsReason(err, ReasonInvalidInput) {
		t.Fatalf("expected invalid input for empty options, got %v", err)
	}

	root := createCA("Input Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)
	leaf := issueLeaf(defaultLeafSpec(), root)

	// 提供叶子但无根池。
	if _, err := Verify(Options{Leaf: leaf, CurrentTime: AtTime(fixedNow)}); err == nil ||
		!IsReason(err, ReasonInvalidInput) {
		t.Fatalf("expected invalid input for missing roots, got %v", err)
	}

	// 根池存在但无可验证签名路径。
	roots := NewCertPool()
	roots.AddCert(root.cert)
	otherLeaf := issueLeaf(defaultLeafSpec(),
		createCA("Unrelated", mustGenerateKey(),
			fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true))
	_, err := Verify(baseOptions(otherLeaf, roots, NewCertPool()))
	if err == nil || !IsReason(err, ReasonNoValidPath) {
		t.Fatalf("expected no valid path, got %v", err)
	}
}

func TestErrorIsSentinel(t *testing.T) {
	e := fail(ReasonExpired, "boom")
	if !IsReason(e, ReasonExpired) {
		t.Fatal("IsReason should match")
	}
	if IsReason(e, ReasonRevoked) {
		t.Fatal("IsReason should not match a different reason")
	}
	if e.Error() == "" {
		t.Fatal("error string should not be empty")
	}
}
