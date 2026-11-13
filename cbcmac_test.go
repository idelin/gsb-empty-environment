package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"testing"
)

// newHostKey 生成随机 AES 密钥并返回宿主回调。
// 密钥只存在于闭包内，测试与被测代码一样无法读取明文密钥。
func newHostKey(t *testing.T) KeyHandle {
	t.Helper()
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand key: %v", err)
	}
	return func() (cipher.Block, error) {
		return aes.NewCipher(key)
	}
}

// randMessage 生成 n 块随机消息。
func randMessage(t *testing.T, blocks int) []byte {
	t.Helper()
	m := make([]byte, blocks*BlockSize)
	if _, err := rand.Read(m); err != nil {
		t.Fatalf("rand message: %v", err)
	}
	return m
}

func TestTagRejectsInvalidMessages(t *testing.T) {
	o := NewOracle(newHostKey(t))
	if _, err := o.Tag(nil); err != ErrEmptyMessage {
		t.Fatalf("empty message: got %v, want ErrEmptyMessage", err)
	}
	if _, err := o.Tag([]byte{}); err != ErrEmptyMessage {
		t.Fatalf("zero-length message: got %v, want ErrEmptyMessage", err)
	}
	if _, err := o.Tag(make([]byte, BlockSize+1)); err == nil {
		t.Fatal("unaligned message: want error")
	}
	if _, err := o.Tag(make([]byte, (MaxBlocks+1)*BlockSize)); err == nil {
		t.Fatal("oversized message: want error")
	}
	if o.Verify(make([]byte, BlockSize), make([]byte, BlockSize-1)) {
		t.Fatal("verify accepted wrong-length tag")
	}
}

func TestTagKnownVector(t *testing.T) {
	// NIST AES-128 测试密钥，全零单块消息：
	// tag = AES_K(0) = 7df76b0c1ab899b33e42f047b91b546f。
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
		0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	o := NewOracle(func() (cipher.Block, error) { return aes.NewCipher(key) })
	tag, err := o.Tag(make([]byte, BlockSize))
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	want := []byte{0x7d, 0xf7, 0x6b, 0x0c, 0x1a, 0xb8, 0x99, 0xb3,
		0x3e, 0x42, 0xf0, 0x47, 0xb9, 0x1b, 0x54, 0x6f}
	if !bytes.Equal(tag, want) {
		t.Fatalf("tag = %x, want %x", tag, want)
	}
	if !o.Verify(make([]byte, BlockSize), tag) {
		t.Fatal("verify rejected valid tag")
	}
}

func TestTamperFails(t *testing.T) {
	o := NewOracle(newHostKey(t))
	msg := randMessage(t, 3)
	tag, err := o.Tag(msg)
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	// 改动消息任一字节，tag 必须不同。
	for i := range msg {
		m := bytes.Clone(msg)
		m[i] ^= 0x01
		got, err := o.Tag(m)
		if err != nil {
			t.Fatalf("tag: %v", err)
		}
		if bytes.Equal(got, tag) {
			t.Fatalf("flip byte %d: tag unchanged", i)
		}
		if o.Verify(m, tag) {
			t.Fatalf("flip byte %d: verify accepted", i)
		}
	}
	// 改动 tag 任一字节，Verify 必须拒绝。
	for i := range tag {
		bad := bytes.Clone(tag)
		bad[i] ^= 0x80
		if o.Verify(msg, bad) {
			t.Fatalf("flip tag byte %d: verify accepted", i)
		}
	}
}
