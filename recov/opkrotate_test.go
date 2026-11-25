package recov

import (
	"errors"
	"testing"
)

// 实例发布第二棵 OPK 树后：锚定旧树的会话仍可恢复，新会话使用新树。
func TestOPKTreeRotation(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	oldBlock := h.publishOPK(acct, instA, 2)
	cpOld := h.dirCPs[len(h.dirCPs)-1]
	oldEnv := h.envelopeAt(acct, instA, 0, cpOld, []byte("old-tree-rk!!1"))
	h.receiptFor(oldEnv)

	// 挂载新树，旧树被替换。
	newBlock := h.publishOPK(acct, instA, 2)
	cpNew := h.dirCPs[len(h.dirCPs)-1]
	newEnv := h.envelopeAt(acct, instA, 0, cpNew, []byte("new-tree-rk!!1"))
	h.receiptFor(newEnv)

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cpNew)
	h.feedAllReceipts(im)
	if err := im.ImportOPKBlock(oldBlock); err != nil {
		t.Fatal(err)
	}
	if err := im.ImportOPKBlock(newBlock); err != nil {
		t.Fatal(err)
	}

	// 旧信封锚定旧检查点，其授权根是旧树：必须仍可恢复。
	oldPriv := h.opkHistory[instA][0]
	if err := im.RecoverSession(oldEnv, oldPriv,
		func(_ []byte, _ AccountID, _ InstanceID, _ []byte) error { return nil }); err != nil {
		t.Fatalf("old-tree session must still verify: %v", err)
	}

	// 新信封需要新树私钥；用旧私钥解封应失败（承诺不同）。
	newPriv := h.opkHistory[instA][2] // 第二棵树的第 0 把私钥
	if err := im.RecoverSession(newEnv, oldPriv,
		func(_ []byte, _ AccountID, _ InstanceID, _ []byte) error { return nil }); err == nil {
		t.Fatal("stale private key must not recover the new-tree session")
	} else if !errors.Is(err, ErrUnwrap) {
		t.Fatalf("expected ErrUnwrap, got %v", err)
	}

	var got []byte
	if err := im.RecoverSession(newEnv, newPriv,
		func(_ []byte, _ AccountID, _ InstanceID, rk []byte) error {
			got = clone(rk)
			return nil
		}); err != nil {
		t.Fatalf("new-tree session recovery failed: %v", err)
	}
	if string(got) != "new-tree-rk!!1" {
		t.Fatal("new tree recovery key mismatch")
	}
}
