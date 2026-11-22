package recovery

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"errors"
	"testing"
)

func testBinder(t *testing.T) Binder {
	t.Helper()
	bind, err := NewGCMBinder(map[uint64][]byte{
		1: bytes.Repeat([]byte{0x11}, 32),
		2: bytes.Repeat([]byte{0x22}, 32),
	})
	if err != nil {
		t.Fatalf("NewGCMBinder: %v", err)
	}
	return bind
}

func testRequest(digestByte byte) SealRequest {
	return SealRequest{
		KeyGen: 1,
		Plan:   "plan:v1",
		Slot:   7,
		Digest: sha256.Sum256([]byte{digestByte}),
	}
}

func TestSealIdempotentRetry(t *testing.T) {
	store := NewFrameStore()
	bind := testBinder(t)
	calls := 0
	counting := func(keyGen uint64, nonce, plaintext, aad []byte) ([]byte, error) {
		calls++
		return bind(keyGen, nonce, plaintext, aad)
	}
	req := testRequest(0xAA)
	first, err := store.Seal(req, []byte("frame-payload"), counting)
	if err != nil {
		t.Fatalf("首次密封: %v", err)
	}
	// 结果不明时重试:必须取回同一已密封帧,不得再次密封。
	second, err := store.Seal(req, []byte("frame-payload"), counting)
	if err != nil {
		t.Fatalf("重试取回: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Binder 调用 %d 次, 期望 1 次", calls)
	}
	if first.Nonce != second.Nonce || !bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("重试未取回同一已密封帧")
	}
	got, ok := store.Retrieve(req.KeyGen, req.Plan, req.Slot)
	if !ok || !bytes.Equal(got.Ciphertext, first.Ciphertext) {
		t.Fatal("Retrieve 未取回已密封帧")
	}
}

func TestSealRejectsNonceReuse(t *testing.T) {
	store := NewFrameStore()
	bind := testBinder(t)
	if _, err := store.Seal(testRequest(0xAA), []byte("a"), bind); err != nil {
		t.Fatalf("首次密封: %v", err)
	}
	// 同一 (密钥代次, 公开计划, 槽号) 配不同事务摘要:拒绝。
	if _, err := store.Seal(testRequest(0xBB), []byte("b"), bind); !errors.Is(err, ErrNonceReuse) {
		t.Fatalf("err = %v, 期望 ErrNonceReuse", err)
	}
	// 提升密钥代次后允许密封新内容。
	req := testRequest(0xBB)
	req.KeyGen = 2
	if _, err := store.Seal(req, []byte("b"), bind); err != nil {
		t.Fatalf("换代密封: %v", err)
	}
}

func TestSealBindsAssociatedData(t *testing.T) {
	store := NewFrameStore()
	bind := testBinder(t)
	req := testRequest(0xCC)
	frame, err := store.Seal(req, []byte("payload"), bind)
	if err != nil {
		t.Fatalf("密封: %v", err)
	}
	block, err := aes.NewCipher(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("NewGCM: %v", err)
	}
	pt, err := aead.Open(nil, frame.Nonce[:], frame.Ciphertext, frame.AAD)
	if err != nil {
		t.Fatalf("正确关联数据解密失败: %v", err)
	}
	if string(pt) != "payload" {
		t.Fatalf("明文 = %q", pt)
	}
	// 篡改任一绑定字段(此处为槽号)必须使认证失败。
	tampered := req
	tampered.Slot = 8
	if _, err := aead.Open(nil, frame.Nonce[:], frame.Ciphertext, tampered.AssociatedData()); err == nil {
		t.Fatal("篡改槽号后解密不应成功")
	}
}
