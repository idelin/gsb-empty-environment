package netsec

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDomainLabelShape(t *testing.T) {
	// 域标签恰为一块：15 字节 ASCII + 一个零字节。
	if DomainLabel[15] != 0x00 {
		t.Fatal("domain label must end with zero byte")
	}
	for i := 0; i < 15; i++ {
		if DomainLabel[i] < 0x21 || DomainLabel[i] > 0x7e {
			t.Fatalf("domain label byte %d not printable ASCII: %#x", i, DomainLabel[i])
		}
	}
}

func TestHardenedRoundTrip(t *testing.T) {
	h := NewHardenedOracle(newHostKey(t))
	for blocks := 1; blocks <= MaxBlocks; blocks++ {
		msg := randMessage(t, blocks)
		tag, err := h.Tag(msg)
		if err != nil {
			t.Fatalf("blocks=%d tag: %v", blocks, err)
		}
		if !h.Verify(msg, tag) {
			t.Fatalf("blocks=%d verify rejected valid tag", blocks)
		}
	}
	if _, err := h.Tag(nil); err != ErrEmptyMessage {
		t.Fatalf("empty message: got %v, want ErrEmptyMessage", err)
	}
}

// TestHardenedLengthBlock 长度块参与 MAC：同一消息内容，
// 声明块数不同则标签不同；篡改长度块后校验失败。
func TestHardenedLengthBlock(t *testing.T) {
	k := newHostKey(t)
	h := NewHardenedOracle(k)
	raw := NewOracle(k)

	m1 := randMessage(t, 1)
	m2 := append(bytes.Clone(m1), randMessage(t, 1)...)
	tag1, err := h.Tag(m1)
	if err != nil {
		t.Fatalf("tag m1: %v", err)
	}
	tag2, err := h.Tag(m2)
	if err != nil {
		t.Fatalf("tag m2: %v", err)
	}
	if bytes.Equal(tag1, tag2) {
		t.Fatal("tags equal for different block counts")
	}
	if h.Verify(m2, tag1) || h.Verify(m1, tag2) {
		t.Fatal("verify accepted tag across different lengths")
	}

	// 手工改动长度块，标签必须改变。
	input, err := encode(m1)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	binary.BigEndian.PutUint64(input[BlockSize+8:], 2)
	alt, err := raw.Tag(input)
	if err != nil {
		t.Fatalf("tag altered input: %v", err)
	}
	if bytes.Equal(alt, tag1) {
		t.Fatal("tag unchanged after length block tamper")
	}
}

// TestDomainSeparation 旧格式（裸消息）tag 不得在新格式中接受，
// 新格式 tag 也不得在旧格式中接受。
func TestDomainSeparation(t *testing.T) {
	k := newHostKey(t)
	raw := NewOracle(k)
	h := NewHardenedOracle(k)

	msg := randMessage(t, 2)
	oldTag, err := raw.Tag(msg)
	if err != nil {
		t.Fatalf("raw tag: %v", err)
	}
	newTag, err := h.Tag(msg)
	if err != nil {
		t.Fatalf("hardened tag: %v", err)
	}
	if bytes.Equal(oldTag, newTag) {
		t.Fatal("old and new tags collide")
	}
	if h.Verify(msg, oldTag) {
		t.Fatal("hardened verify accepted legacy tag")
	}
	if raw.Verify(msg, newTag) {
		t.Fatal("legacy verify accepted hardened tag")
	}
}

// TestHardenedBlocksForgery 加固格式下拼接伪造不再适用：
// 长度前缀使 F 的编码与 N 的编码长度块不同，tag 无法复用。
func TestHardenedBlocksForgery(t *testing.T) {
	h := NewHardenedOracle(newHostKey(t))
	M := randMessage(t, 1)
	N := randMessage(t, 1)
	tagM, err := h.Tag(M)
	if err != nil {
		t.Fatalf("tag M: %v", err)
	}
	tagN, err := h.Tag(N)
	if err != nil {
		t.Fatalf("tag N: %v", err)
	}
	F, err := Forge(M, N, tagM)
	if err != nil {
		t.Fatalf("forge: %v", err)
	}
	if h.Verify(F, tagN) {
		t.Fatal("hardened format accepted concatenation forgery")
	}
}
