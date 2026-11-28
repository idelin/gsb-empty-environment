package pathverify

import (
	"crypto/x509"
	"math/big"
	"testing"
	"time"
)

// TestCRLRevocation 验收点 4：CRL 验签 + 时间窗 + 序列号吊销终态判定。
func TestCRLRevocation(t *testing.T) {
	root := createCA("CRL Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true)
	inter := issueCA("CRL Inter", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	leaf := issueLeaf(defaultLeafSpec(), inter)

	roots := NewCertPool()
	roots.AddCert(root.cert)
	inters := NewCertPool()
	inters.AddCert(inter.cert)

	// 未吊销：干净 CRL 应放行。
	cleanCRL := makeCRL(inter, nil, fixedNow.Add(-time.Minute), fixedNow.Add(time.Hour))
	opts := baseOptions(leaf, roots, inters)
	opts.CRLs = []*x509.RevocationList{cleanCRL}
	if _, err := Verify(opts); err != nil {
		t.Fatalf("clean CRL should not block the chain, got %v", err)
	}

	// 吊销叶子序列号：拒绝。
	revokedCRL := makeCRL(inter, []*big.Int{leaf.SerialNumber},
		fixedNow.Add(-time.Minute), fixedNow.Add(time.Hour))
	opts.CRLs = []*x509.RevocationList{revokedCRL}
	if err := expectReasonOpts(opts, ReasonRevoked); err != nil {
		t.Fatal(err)
	}

	// 过期 CRL（NextUpdate 已过）：拒绝该路径。
	expiredCRL := makeCRL(inter, nil, fixedNow.Add(-2*time.Hour), fixedNow.Add(-time.Hour))
	opts.CRLs = []*x509.RevocationList{expiredCRL}
	if err := expectReasonOpts(opts, ReasonCRL); err != nil {
		t.Fatal(err)
	}

	// 尚未生效的 CRL（ThisUpdate 在未来）：拒绝。
	futureCRL := makeCRL(inter, nil, fixedNow.Add(time.Hour), fixedNow.Add(2*time.Hour))
	opts.CRLs = []*x509.RevocationList{futureCRL}
	if err := expectReasonOpts(opts, ReasonCRL); err != nil {
		t.Fatal(err)
	}
}

// TestCycleDetection 构建带环的中间证书图，构建器必须通过环检测安全终止。
func TestCycleDetection(t *testing.T) {
	root := createCA("Cycle Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 2, true)
	ca1 := issueCA("Cycle CA1", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true, false)
	ca2 := issueCA("Cycle CA2", mustGenerateKey(), ca1,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	// 由 ca2 回签一张主体为 ca1 的证书，构成 ca1 -> ca2 -> ca1 环。
	loopBack := issueCA("Cycle CA1", mustGenerateKey(), ca2,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, false, false)

	leaf := issueLeaf(defaultLeafSpec(), ca2)

	roots := NewCertPool()
	roots.AddCert(root.cert)
	inters := NewCertPool()
	inters.AddCert(ca1.cert)
	inters.AddCert(ca2.cert)
	inters.AddCert(loopBack.cert)

	chains := buildChains(leaf, roots, inters, defaultMaxPathLen)
	if len(chains) == 0 {
		t.Fatal("expected at least one valid chain despite the cyclic graph")
	}
	if _, err := Verify(baseOptions(leaf, roots, inters)); err != nil {
		t.Fatalf("valid chain should verify despite cyclic alternatives, got %v", err)
	}
}

// TestNoPathToAnchor 图无法到达任何信任锚时返回结构化错误。
func TestNoPathToAnchor(t *testing.T) {
	trusted := createCA("Trusted Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)
	other := createCA("Other Root", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)
	inter := issueCA("Orphan Inter", mustGenerateKey(), other,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	leaf := issueLeaf(defaultLeafSpec(), inter)

	roots := NewCertPool()
	roots.AddCert(trusted.cert)
	inters := NewCertPool()
	inters.AddCert(inter.cert)

	_, err := Verify(baseOptions(leaf, roots, inters))
	if err == nil || !IsReason(err, ReasonNoValidPath) {
		t.Fatalf("expected ReasonNoValidPath for unreachable anchor, got %v", err)
	}
}
