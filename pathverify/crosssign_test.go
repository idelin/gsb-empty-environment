package pathverify

import (
	"crypto/x509"
	"math/big"
	"testing"
	"time"
)

// TestCrossSignBacktrack 验收点 1：跨签名桥接 CA，过期主路径自动回溯到有效备用根。
func TestCrossSignBacktrack(t *testing.T) {
	oldRoot := createCA("Old Root CA", mustGenerateKey(),
		fixedNow.Add(-10*365*24*time.Hour), fixedNow.Add(-time.Hour), 2, true)
	newRoot := createCA("New Root CA", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(365*24*time.Hour), 2, true)

	bridgeKey := mustGenerateKey()
	// 同 Subject、不同 Issuer 的两张跨签名证书：旧根签发的随旧根失效，新根签发的有效。
	bridgeByOld := issueCA("Bridge CA", bridgeKey, oldRoot,
		fixedNow.Add(-2*time.Hour), fixedNow.Add(365*24*time.Hour), 1, true, false)
	bridgeByNew := issueCA("Bridge CA", bridgeKey, newRoot,
		fixedNow.Add(-2*time.Hour), fixedNow.Add(365*24*time.Hour), 1, true, false)

	inter := issueCA("Intermediate CA", mustGenerateKey(), bridgeByNew,
		fixedNow.Add(-time.Hour), fixedNow.Add(30*24*time.Hour), 0, true, false)
	leaf := issueLeaf(defaultLeafSpec(), inter)

	roots := NewCertPool()
	roots.AddCert(oldRoot.cert)
	roots.AddCert(newRoot.cert)
	inters := NewCertPool()
	inters.AddCert(bridgeByOld.cert)
	inters.AddCert(bridgeByNew.cert)
	inters.AddCert(inter.cert)

	chains := buildChains(leaf, roots, inters, defaultMaxPathLen)
	if len(chains) != 2 {
		t.Fatalf("expected 2 structural candidate chains, got %d", len(chains))
	}

	res, err := Verify(baseOptions(leaf, roots, inters))
	if err != nil {
		t.Fatalf("expected successful backtracking to valid anchor, got: %v", err)
	}
	if res.Chain[0].Subject.CommonName != "New Root CA" {
		t.Fatalf("expected chain anchored at New Root CA, got %q", res.Chain[0].Subject.CommonName)
	}
	if len(res.Chain) != 4 {
		t.Fatalf("expected NewRoot -> Bridge -> Inter -> Leaf (4 certs), got %d", len(res.Chain))
	}

	// 仅信任过期旧根：候选链全部失败，并保留逐链诊断。
	onlyOld := NewCertPool()
	onlyOld.AddCert(oldRoot.cert)
	_, err = Verify(baseOptions(leaf, onlyOld, inters))
	if err == nil || !IsReason(err, ReasonNoValidPath) {
		t.Fatalf("expected ReasonNoValidPath with expired-only anchor, got %v", err)
	}
	if len(err.(*Error).Attempts) == 0 {
		t.Fatal("expected failed-attempt diagnostics when all candidates fail")
	}

	// 关闭锚点有效期校验后，经旧根的链按纯 RFC 5280 行为可被接受。
	opts := baseOptions(leaf, onlyOld, inters)
	opts.IgnoreAnchorValidity = true
	if _, err = Verify(opts); err != nil {
		t.Fatalf("expected success when anchor validity ignored, got %v", err)
	}
}

// TestCrossSignRevocationFallback 一条跨签链因 CRL 吊销失败时，回溯到未吊销的另一条链。
func TestCrossSignRevocationFallback(t *testing.T) {
	rootA := createCA("Root A", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 2, true)
	rootB := createCA("Root B", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 2, true)

	bridgeKey := mustGenerateKey()
	bridgeA := issueCA("Shared Bridge", bridgeKey, rootA,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true, false)
	bridgeB := issueCA("Shared Bridge", bridgeKey, rootB,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true, false)

	inter := issueCA("Inter", mustGenerateKey(), bridgeB,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	leaf := issueLeaf(defaultLeafSpec(), inter)

	roots := NewCertPool()
	roots.AddCert(rootA.cert)
	roots.AddCert(rootB.cert)
	inters := NewCertPool()
	inters.AddCert(bridgeA.cert)
	inters.AddCert(bridgeB.cert)
	inters.AddCert(inter.cert)

	// Root A 签发的 CRL 吊销了 bridgeA（按 bridgeA 序列号）。
	crlA := makeCRL(rootA, []*big.Int{bridgeA.cert.SerialNumber},
		fixedNow.Add(-time.Minute), fixedNow.Add(time.Hour))

	opts := baseOptions(leaf, roots, inters)
	opts.CRLs = []*x509.RevocationList{crlA}
	res, err := Verify(opts)
	if err != nil {
		t.Fatalf("expected fallback to non-revoked Root B chain, got %v", err)
	}
	if res.Chain[0].Subject.CommonName != "Root B" {
		t.Fatalf("expected Root B anchor after revocation, got %q", res.Chain[0].Subject.CommonName)
	}
}
