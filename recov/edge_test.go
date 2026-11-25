package recov

import (
	"errors"
	"testing"
)

func TestBadCheckpointSignature(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 2)
	cp := h.dirCPs[len(h.dirCPs)-1]
	env := h.envelopeAt(acct, instA, 0, cp, []byte("sig-rk!!16"))
	h.receiptFor(env)

	rogue, _ := GenerateDirectoryKey()
	im := NewImporter(rogue.Public, witPub(h.witKey))
	h.feedAllReceipts(im)
	_ = im.ImportOPKBlock(blk)
	// 用错误的目录信任锚导入检查点，必须在签名处被拒。
	if err := im.ImportDirectoryCheckpoint(cp); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
	if _, err := im.Evaluate(env); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("envelope checkpoint sig must fail, got %v", err)
	}
}

func TestReceiptLogPresentButNoMatchingReceipt(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 2)
	cp := h.dirCPs[len(h.dirCPs)-1]
	env := h.envelopeAt(acct, instA, 0, cp, []byte("no-rcpt!!16"))

	// 另一个无关实例的回执占据回执日志，本会话回执缺失。
	other := h.envelopeAt(acct, instA, 1, cp, []byte("other-rk!16"))
	h.receiptFor(other)

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cp)
	h.feedAllReceipts(im)
	_ = im.ImportOPKBlock(blk)
	if _, err := im.Evaluate(env); !errors.Is(err, ErrMissingInclusion) {
		t.Fatalf("expected ErrMissingInclusion without matching receipt, got %v", err)
	}
}

func TestTamperedOPKInclusionProof(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 4)
	cp := h.dirCPs[len(h.dirCPs)-1]
	env := h.envelopeAt(acct, instA, 2, cp, []byte("proof-rk!!16"))
	h.receiptFor(env)

	env.OPKInclusion.Hashes[0][0] ^= 0xFF
	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cp)
	h.feedAllReceipts(im)
	_ = im.ImportOPKBlock(blk)
	if _, err := im.Evaluate(env); !errors.Is(err, ErrMissingInclusion) {
		t.Fatalf("bad inclusion proof must be ErrMissingInclusion, got %v", err)
	}
}

func TestWrongPrivateKeyRejected(t *testing.T) {
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 2)
	cp := h.dirCPs[len(h.dirCPs)-1]
	env := h.envelopeAt(acct, instA, 0, cp, []byte("key-rk!!16"))
	h.receiptFor(env)

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cp)
	h.feedAllReceipts(im)
	_ = im.ImportOPKBlock(blk)
	// 用另一把 OPK 私钥解封，承诺不匹配，绝不能触发回调。
	called := false
	err := im.RecoverSession(env, h.opkPriv[instA][1],
		func(_ []byte, _ AccountID, _ InstanceID, _ []byte) error {
			called = true
			return nil
		})
	if !errors.Is(err, ErrUnwrap) {
		t.Fatalf("expected ErrUnwrap, got %v", err)
	}
	if called {
		t.Fatal("callback must not fire with mismatched private key")
	}
}
