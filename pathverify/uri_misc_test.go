package pathverify

import (
	"encoding/pem"
	"errors"
	"testing"
	"time"
)

// TestURINameConstraints URI 主机部分遵循与 DNS 相同的子域边界。
func TestURINameConstraints(t *testing.T) {
	root := createCA("URI Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true)
	ca := issueCA("URI CA", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false,
		ncSpec{permittedURI: []string{".example.com"}})

	roots := NewCertPool()
	roots.AddCert(root.cert)
	inters := NewCertPool()
	inters.AddCert(ca.cert)

	leafOK := ncLeaf(ca, nil, nil, nil, []string{"https://api.example.com/v1"})
	if _, err := Verify(noHostname(leafOK, roots, inters)); err != nil {
		t.Fatalf("URI host within permitted subtree should pass, got %v", err)
	}
	leafBad := ncLeaf(ca, nil, nil, nil, []string{"https://evil.other.org/x"})
	if err := expectReasonOpts(noHostname(leafBad, roots, inters), ReasonNameConstraint); err != nil {
		t.Fatal(err)
	}
}

// TestPEMAndDERPool PEM/DER 导入与去重、错误判定辅助。
func TestPEMAndDERPool(t *testing.T) {
	root := createCA("Pool Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)

	pool := NewCertPool()
	if err := pool.AddDER(root.cert.Raw); err != nil {
		t.Fatalf("AddDER failed: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.cert.Raw})
	added, err := pool.AddPEM(pemBytes)
	if err != nil {
		t.Fatalf("AddPEM failed: %v", err)
	}
	if added != 1 {
		t.Fatalf("expected 1 added (dedup), got %d", added)
	}
	if pool.Len() != 1 {
		t.Fatalf("expected pool size 1 after dedup, got %d", pool.Len())
	}
	if err := pool.AddDER([]byte("not-a-cert")); err == nil {
		t.Fatal("expected error on invalid DER")
	}
	if _, err := pool.AddPEM([]byte("no pem here")); err == nil {
		t.Fatal("expected error on empty PEM input")
	}

	// 错误 Reason 判定与 errors.Is 语义。
	_, err = Verify(Options{Roots: pool})
	if !IsReason(err, ReasonInvalidInput) {
		t.Fatalf("expected ReasonInvalidInput, got %v", err)
	}
	if !errors.Is(err, &Error{Reason: ReasonInvalidInput}) {
		t.Fatal("errors.Is should match by Reason")
	}
}

// TestSentinelErrorText 保证错误信息包含候选链诊断，便于运维排障。
func TestSentinelErrorText(t *testing.T) {
	root := createCA("Text Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)
	ca := issueCA("Text CA", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	leaf := issueLeaf(defaultLeafSpec(), ca)

	roots := NewCertPool()
	roots.AddCert(root.cert)
	inters := NewCertPool()
	inters.AddCert(ca.cert)

	// 触发 pathLen 失败并检查顶层 + Attempt 文本。
	_, err := Verify(baseOptions(leaf, roots, inters))
	if err == nil {
		t.Fatal("expected failure")
	}
	e := err.(*Error)
	if len(e.Attempts) == 0 || e.Attempts[0].Err == nil {
		t.Fatal("expected nested attempt error detail")
	}
}
