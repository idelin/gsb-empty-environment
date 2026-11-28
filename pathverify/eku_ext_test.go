package pathverify

import (
	"crypto/x509"
	"testing"
	"time"
)

// TestIntermediateEKUTightening 验收点 4：中间 CA 携带非空 EKU 时，
// 目标用途必须同时被链上每个带 EKU 的证书允许；AnyEKU 豁免。
func TestIntermediateEKUTightening(t *testing.T) {
	root := createCA("EKU Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true)
	roots := NewCertPool()
	roots.AddCert(root.cert)

	// 中间 CA 仅允许 ClientAuth，叶子仅 ServerAuth：交集为空，拒绝。
	clientOnlyCA := issueCAWithEKU("ClientAuth-only CA", root,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	serverLeaf := issueLeaf(defaultLeafSpec(), clientOnlyCA)
	inters := NewCertPool()
	inters.AddCert(clientOnlyCA.cert)
	if err := expectReason(t, serverLeaf, roots, inters, ReasonEKU); err != nil {
		t.Fatal(err)
	}

	// 中间 CA 允许 ServerAuth：放行。
	serverCA := issueCAWithEKU("ServerAuth CA", root,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	okLeaf := issueLeaf(defaultLeafSpec(), serverCA)
	inters2 := NewCertPool()
	inters2.AddCert(serverCA.cert)
	if _, err := Verify(baseOptions(okLeaf, roots, inters2)); err != nil {
		t.Fatalf("matching EKU across chain should pass, got %v", err)
	}

	// 中间 CA 携带 AnyExtendedKeyUsage：豁免叶子的 ServerAuth。
	anyCA := issueCAWithEKU("AnyEKU CA", root,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageAny})
	anyLeaf := issueLeaf(defaultLeafSpec(), anyCA)
	inters3 := NewCertPool()
	inters3.AddCert(anyCA.cert)
	if _, err := Verify(baseOptions(anyLeaf, roots, inters3)); err != nil {
		t.Fatalf("AnyExtendedKeyUsage should permit leaf usage, got %v", err)
	}

	// 两层中间 CA 的 EKU 取交集：上层 ServerAuth、下层 ClientAuth 时必须拒绝。
	upper := issueCAWithEKU("Upper ServerAuth", root,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	lower := issueCAWithEKU("Lower ClientAuth", upper,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	leaf := issueLeaf(defaultLeafSpec(), lower)
	inters4 := NewCertPool()
	inters4.AddCert(upper.cert)
	inters4.AddCert(lower.cert)
	if err := expectReason(t, leaf, roots, inters4, ReasonEKU); err != nil {
		t.Fatal(err)
	}
}

// TestClientAuthEKU mTLS 场景：目标用途为 ClientAuth 时同样逐链收紧。
func TestClientAuthEKU(t *testing.T) {
	root := createCA("mTLS Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true)
	roots := NewCertPool()
	roots.AddCert(root.cert)

	ca := issueCAWithEKU("mTLS CA", root,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth})
	spec := defaultLeafSpec()
	spec.eku = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	leaf := issueLeaf(spec, ca)

	opts := baseOptions(leaf, roots, NewCertPool())
	opts.Intermediates.AddCert(ca.cert)
	opts.EKU = []ExtKeyUsage{ExtKeyUsageClientAuth}
	if _, err := Verify(opts); err != nil {
		t.Fatalf("ClientAuth permitted by intermediate should pass, got %v", err)
	}
}

// TestCAKeyCertSignRequired 验收点 4：中间 CA 缺 keyCertSign 必须拒绝。
func TestCAKeyCertSignRequired(t *testing.T) {
	root := createCA("KU Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true)
	roots := NewCertPool()
	roots.AddCert(root.cert)

	badCA := issueCAWithoutKeyCertSign("No CertSign CA", root)
	leaf := issueLeaf(defaultLeafSpec(), badCA)
	inters := NewCertPool()
	inters.AddCert(badCA.cert)
	if err := expectReason(t, leaf, roots, inters, ReasonKeyUsage); err != nil {
		t.Fatal(err)
	}

	// 叶子 KeyUsage 不含 digitalSignature 时拒绝（可通过开关关闭该检查）。
	goodCA := issueCA("Good CA", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	spec := defaultLeafSpec()
	spec.keyUsage = x509.KeyUsageKeyEncipherment
	leafKU := issueLeaf(spec, goodCA)
	inters2 := NewCertPool()
	inters2.AddCert(goodCA.cert)
	if err := expectReason(t, leafKU, roots, inters2, ReasonKeyUsage); err != nil {
		t.Fatal(err)
	}
	opts := baseOptions(leafKU, roots, inters2)
	opts.DisableLeafKeyUsageCheck = true
	if _, err := Verify(opts); err != nil {
		t.Fatalf("disabling leaf key usage check should pass, got %v", err)
	}
}

// TestUnknownCriticalExtension 验收点 4：无法识别且 Critical=true 的扩展立即拒绝。
func TestUnknownCriticalExtension(t *testing.T) {
	root := createCA("Crit Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)
	roots := NewCertPool()
	roots.AddCert(root.cert)

	leaf := issueLeafWithCriticalExtension(root)
	if len(leaf.UnhandledCriticalExtensions) == 0 {
		t.Fatal("expected the private extension to surface as unhandled critical extension")
	}
	if err := expectReason(t, leaf, roots, NewCertPool(), ReasonCriticalExtension); err != nil {
		t.Fatal(err)
	}
}
