package main

import (
	"math/big"
	"testing"
)

func TestVerifyDigest(t *testing.T) {
	a := makeArchive(5)
	if !a.Blocks[0].VerifyDigest() {
		t.Fatal("摘要应匹配")
	}
	a.Blocks[0].Data = []byte("tampered")
	if a.Blocks[0].VerifyDigest() {
		t.Fatal("篡改内容后摘要不应匹配")
	}
}

func TestTotalLengthBig(t *testing.T) {
	a := &Archive{Blocks: []*Block{
		{ID: "x", Length: new(big.Int).Lsh(big.NewInt(1), 64), Digest: ""},
		{ID: "y", Length: new(big.Int).Lsh(big.NewInt(1), 64), Digest: ""},
	}}
	total := a.TotalLength()
	if total.BitLen() != 66 {
		t.Fatalf("总长度应超过 64 位，实际 %d 位", total.BitLen())
	}
}

func TestValidate(t *testing.T) {
	a := makeArchive(1, 2)
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	a.Blocks[1].ID = a.Blocks[0].ID
	if err := a.Validate(); err == nil {
		t.Fatal("重复块 ID 应报错")
	}
}
