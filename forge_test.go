package netsec

import (
	"bytes"
	"fmt"
	"testing"
)

// TestForgeAllLengths 对 M、N 各 1..8 块（总长不超过上限）验证
// 拼接公式 MAC(M || (N_first XOR tagM) || N_rest) = tagN。
// 攻击者只调用本地 oracle 的 Tag，不接触密钥。
func TestForgeAllLengths(t *testing.T) {
	o := NewOracle(newHostKey(t))
	for m := 1; m <= MaxBlocks; m++ {
		for n := 1; n <= MaxBlocks-m; n++ {
			t.Run(fmt.Sprintf("M=%d/N=%d", m, n), func(t *testing.T) {
				M := randMessage(t, m)
				N := randMessage(t, n)
				tagM, err := o.Tag(M)
				if err != nil {
					t.Fatalf("tag M: %v", err)
				}
				tagN, err := o.Tag(N)
				if err != nil {
					t.Fatalf("tag N: %v", err)
				}
				F, err := Forge(M, N, tagM)
				if err != nil {
					t.Fatalf("forge: %v", err)
				}
				tagF, err := o.Tag(F)
				if err != nil {
					t.Fatalf("tag F: %v", err)
				}
				if !bytes.Equal(tagF, tagN) {
					t.Fatalf("MAC(F) = %x, want tagN = %x", tagF, tagN)
				}
				if !o.Verify(F, tagN) {
					t.Fatal("oracle rejected forged message")
				}
			})
		}
	}
}

// TestForgeChainProof 用调试 oracle 的逐块链值证明伪造机理：
// F 处理完 M 后链值等于 tagM；之后每块链值与 N 完全重合。
func TestForgeChainProof(t *testing.T) {
	d := NewDebugOracle(newHostKey(t))
	M := randMessage(t, 2)
	N := randMessage(t, 3)
	tagM, err := d.Tag(M)
	if err != nil {
		t.Fatalf("tag M: %v", err)
	}
	F, err := Forge(M, N, tagM)
	if err != nil {
		t.Fatalf("forge: %v", err)
	}
	chainF, err := d.ChainValues(F)
	if err != nil {
		t.Fatalf("chain F: %v", err)
	}
	chainN, err := d.ChainValues(N)
	if err != nil {
		t.Fatalf("chain N: %v", err)
	}
	// F 的第 len(M)/16 块之后的链值即 tagM。
	if got := chainF[len(M)/BlockSize-1]; !bytes.Equal(got, tagM) {
		t.Fatalf("chain after M = %x, want tagM = %x", got, tagM)
	}
	// 从 N_first 起，F 与 N 的链值逐块一致。
	for i, s := range chainN {
		if got := chainF[len(M)/BlockSize+i]; !bytes.Equal(got, s) {
			t.Fatalf("chain divergence at N block %d: F=%x N=%x", i, got, s)
		}
	}
}

// TestForgeRejectsBadInput 检查伪造构造的输入校验。
func TestForgeRejectsBadInput(t *testing.T) {
	good := randMessage(t, 1)
	tag := make([]byte, BlockSize)
	if _, err := Forge(nil, good, tag); err == nil {
		t.Fatal("empty M accepted")
	}
	if _, err := Forge(good, nil, tag); err == nil {
		t.Fatal("empty N accepted")
	}
	if _, err := Forge(good, good, tag[:8]); err == nil {
		t.Fatal("short tagM accepted")
	}
	big := randMessage(t, MaxBlocks)
	if _, err := Forge(big, big, tag); err == nil {
		t.Fatal("oversized forgery accepted")
	}
}
