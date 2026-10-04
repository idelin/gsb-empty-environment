package main

import (
	"math/big"
	"testing"
)

// stubSource 按顺序返回固定字节，用于精确测试拒绝采样。
type stubSource struct {
	data []byte
	off  int
}

func (s *stubSource) Read(p []byte) {
	for i := range p {
		p[i] = s.data[s.off%len(s.data)]
		s.off++
	}
}

func TestStreamDeterministic(t *testing.T) {
	a, b := newStream("seed-1"), newStream("seed-1")
	pa, pb := make([]byte, 100), make([]byte, 100)
	a.Read(pa)
	b.Read(pb)
	for i := range pa {
		if pa[i] != pb[i] {
			t.Fatal("相同种子应产生相同字节流")
		}
	}
	c := newStream("seed-2")
	pc := make([]byte, 100)
	c.Read(pc)
	if string(pa) == string(pc) {
		t.Fatal("不同种子应产生不同字节流")
	}
}

func TestUniformBigRejectsOutOfRange(t *testing.T) {
	// n=3，候选域 [0,4)：字节 3 必须被拒绝，随后返回 2
	src := &stubSource{data: []byte{3, 2}}
	got := uniformBig(src, big.NewInt(3))
	if got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("期望拒绝 3 后返回 2，实际 %s", got)
	}
}

func TestUniformBigMasksHighBits(t *testing.T) {
	// n=3 只占 2 bit，0xFF 屏蔽高位后变为 3，仍被拒绝；0x81 -> 1 被接受
	src := &stubSource{data: []byte{0xFF, 0x81}}
	got := uniformBig(src, big.NewInt(3))
	if got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("期望返回 1，实际 %s", got)
	}
}

func TestUniformBigHugeRange(t *testing.T) {
	// 超过 200 位的总长度：验证大整数路径且结果恒在 [0, n) 内
	n := new(big.Int).Lsh(big.NewInt(1), 200)
	n.Add(n, big.NewInt(12345))
	s := newStream("huge")
	for i := 0; i < 1000; i++ {
		r := uniformBig(s, n)
		if r.Sign() < 0 || r.Cmp(n) >= 0 {
			t.Fatalf("随机值 %s 超出 [0, n)", r)
		}
	}
}
