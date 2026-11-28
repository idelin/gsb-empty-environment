package pathverify

import (
	"testing"
	"time"
)

// TestPathLenZero 验收点 2：pathLenConstraint=0 放行叶证，但拦截下级非自颁发中间 CA。
func TestPathLenZero(t *testing.T) {
	root := createCA("Root PL0", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true)

	// Root(pathLen=0) -> CA -> Leaf：CA 是根之下的非自颁发中间 CA，超出根的 pathLen=0。
	ca := issueCA("ZeroLen CA", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	leafUnderCA := issueLeaf(defaultLeafSpec(), ca)

	roots := NewCertPool()
	roots.AddCert(root.cert)
	inters := NewCertPool()
	inters.AddCert(ca.cert)
	if err := expectReason(t, leafUnderCA, roots, inters, ReasonPathLen); err != nil {
		t.Fatal(err)
	}

	// Root(pathLen=0) 直接签发叶证书：叶证书不消耗 pathLen，必须放行。
	leafDirect := issueLeaf(defaultLeafSpec(), root)
	if _, err := Verify(baseOptions(leafDirect, roots, NewCertPool())); err != nil {
		t.Fatalf("pathLen=0 CA must still issue a leaf directly, got %v", err)
	}

	// 中间 CA pathLen=0 再签下级中间 CA 必须拒绝；直接签叶证放行。
	root2 := createCA("Root PL2", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 2, true)
	mid := issueCA("Mid PL0", mustGenerateKey(), root2,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	below := issueCA("Below Mid", mustGenerateKey(), mid,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, false, false)
	leafBelow := issueLeaf(defaultLeafSpec(), below)

	roots2 := NewCertPool()
	roots2.AddCert(root2.cert)
	inters2 := NewCertPool()
	inters2.AddCert(mid.cert)
	inters2.AddCert(below.cert)
	if err := expectReason(t, leafBelow, roots2, inters2, ReasonPathLen); err != nil {
		t.Fatal(err)
	}

	leafFromMid := issueLeaf(defaultLeafSpec(), mid)
	inters2b := NewCertPool()
	inters2b.AddCert(mid.cert)
	if _, err := Verify(baseOptions(leafFromMid, roots2, inters2b)); err != nil {
		t.Fatalf("pathLen=0 intermediate must issue leaf directly, got %v", err)
	}
}

// TestSelfIssuedDoesNotConsumePathLen 验收点 2：自颁发过渡 CA（Subject==Issuer）不消耗 pathLen。
func TestSelfIssuedDoesNotConsumePathLen(t *testing.T) {
	// 根 pathLen=1：只允许一个非自颁发中间 CA。
	root := createCA("Root PL1", mustGenerateKey(),
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 1, true)

	// 自颁发过渡证书：与根同 Subject，由根密钥签发（密钥轮换过渡场景）。
	rotatedKey := mustGenerateKey()
	selfIssued := issueCA("Root PL1", rotatedKey, root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, false, true)
	if !isSelfIssued(selfIssued.cert) {
		t.Fatal("expected self-issued certificate (Subject == Issuer)")
	}

	// 自颁发证书之下只有一个普通中间 CA：根之下非自颁发中间 CA 数量仍为 1，满足 pathLen=1。
	mid := issueCA("Mid CA", mustGenerateKey(), selfIssued,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, true, false)
	leaf := issueLeaf(defaultLeafSpec(), mid)

	roots := NewCertPool()
	roots.AddCert(root.cert)
	inters := NewCertPool()
	inters.AddCert(selfIssued.cert)
	inters.AddCert(mid.cert)
	if _, err := Verify(baseOptions(leaf, roots, inters)); err != nil {
		t.Fatalf("self-issued CA must not consume pathLenConstraint, got %v", err)
	}

	// 对照组：同结构替换为非自颁发 CA，则根之下出现两个非自颁发中间 CA，pathLen=1 被突破。
	normal := issueCA("Normal Transition CA", mustGenerateKey(), root,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, false, false)
	mid2 := issueCA("Mid CA 2", mustGenerateKey(), normal,
		fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour), 0, false, false)
	leaf2 := issueLeaf(defaultLeafSpec(), mid2)
	intersCtrl := NewCertPool()
	intersCtrl.AddCert(normal.cert)
	intersCtrl.AddCert(mid2.cert)
	if err := expectReason(t, leaf2, roots, intersCtrl, ReasonPathLen); err != nil {
		t.Fatal(err)
	}
}
