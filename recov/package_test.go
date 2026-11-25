package recov

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func buildHappyPackage(t *testing.T) (*testHarness, *SessionEnvelope, *AcceptancePackage) {
	t.Helper()
	h := newHarness(t)
	h.register(acct, phone, instA)
	blk := h.publishOPK(acct, instA, 3)
	cp := h.dirCPs[len(h.dirCPs)-1]
	env := h.envelopeAt(acct, instA, 1, cp, []byte("package-rk!!16"))
	h.receiptFor(env)

	im := NewImporter(dirPub(h.dirKey), witPub(h.witKey))
	h.feedDirectory(im, cp)
	h.feedAllReceipts(im)
	if err := im.ImportOPKBlock(blk); err != nil {
		t.Fatal(err)
	}
	pkg, err := im.BuildAcceptancePackage(env, h.witKey)
	if err != nil {
		t.Fatalf("build package: %v", err)
	}
	return h, env, pkg
}

func TestAcceptancePackageRoundTrip(t *testing.T) {
	h, env, pkg := buildHappyPackage(t)

	// 独立验证：全新状态、仅信任两个签名公钥。
	if err := VerifyAcceptancePackage(pkg); err != nil {
		t.Fatalf("independent verification failed: %v", err)
	}

	// 序列化为 JSON 后再导入，模拟跨宿主传输。
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	var transported AcceptancePackage
	if err := json.Unmarshal(raw, &transported); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAcceptancePackage(&transported); err != nil {
		t.Fatalf("verification after JSON transport failed: %v", err)
	}

	// 无状态恢复：解封恢复密钥并交给宿主回调。
	var got []byte
	err = RecoverFromPackage(&transported, h.opkPriv[instA][env.OPKIndex],
		func(_ []byte, _ AccountID, _ InstanceID, rk []byte) error {
			got = clone(rk)
			return nil
		})
	if err != nil {
		t.Fatalf("RecoverFromPackage: %v", err)
	}
	if string(got) != "package-rk!!16" {
		t.Fatal("recovered key mismatch")
	}
}

func TestPackageMinimal(t *testing.T) {
	_, _, pkg := buildHappyPackage(t)

	if len(pkg.Records) < 4 {
		t.Fatalf("expected envelope/opk/checkpoints/receipts, got %d", len(pkg.Records))
	}
	// 删除任意一条记录（并同步清单哈希）都必须导致独立验证失败。
	for i := range pkg.Records {
		reduced := clonePackage(pkg)
		removedHash := recordHashOf(pkg.Records[i])
		reduced.Records = append(reduced.Records[:i:i], reduced.Records[i+1:]...)
		reduced.Manifest.RecordHashes = dropHash(reduced.Manifest.RecordHashes, removedHash)
		if err := VerifyAcceptancePackage(reduced); err == nil {
			t.Fatalf("removing record %d (%s) unexpectedly still verifies",
				i, pkg.Records[i].Kind)
		}
	}
}

func TestPackageTamperDetection(t *testing.T) {
	_, _, pkg := buildHappyPackage(t)

	// 篡改清单记录集合但保留签名 -> 签名校验失败。
	tampered := clonePackage(pkg)
	tampered.Manifest.RecordHashes[0] = bytes.Repeat([]byte{0xAB}, 32)
	if err := VerifyAcceptancePackage(tampered); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}

	// 篡改见证签名。
	tampered2 := clonePackage(pkg)
	tampered2.WitnessSig[0] ^= 0xFF
	if err := VerifyAcceptancePackage(tampered2); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature on sig tamper, got %v", err)
	}

	// 替换信封的承诺但不更新清单 -> 记录哈希不匹配，缺少包含证明。
	tampered3 := clonePackage(pkg)
	for i, rec := range tampered3.Records {
		if rec.Kind == KindSessionEnvelope {
			var env SessionEnvelope
			if err := json.Unmarshal(rec.Raw, &env); err != nil {
				t.Fatal(err)
			}
			env.OPKCommitment = bytes.Repeat([]byte{0xCD}, 32)
			raw, _ := json.Marshal(&env)
			tampered3.Records[i].Raw = raw
			break
		}
	}
	if err := VerifyAcceptancePackage(tampered3); err == nil {
		t.Fatal("tampered envelope must fail verification")
	}
}

func TestPackageRejectsUntrustedDirectoryKey(t *testing.T) {
	_, _, pkg := buildHappyPackage(t)

	rogueDir, _ := GenerateDirectoryKey()
	pkg.Manifest.DirectoryVerifier = rogueDir.Public
	// 清单信任锚改变后，清单签名立即失效。
	if err := VerifyAcceptancePackage(pkg); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature with rogue trust anchor, got %v", err)
	}
}

func TestPackageMissingRecordRejected(t *testing.T) {
	_, _, pkg := buildHappyPackage(t)

	// 只删记录不改清单：应报“清单声明的记录缺失”。
	reduced := clonePackage(pkg)
	reduced.Records = reduced.Records[1:]
	if err := VerifyAcceptancePackage(reduced); !errors.Is(err, ErrMissingInclusion) {
		t.Fatalf("expected ErrMissingInclusion, got %v", err)
	}
}
