package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"
)

func makeArchive(lengths ...int64) *Archive {
	a := &Archive{}
	for i, l := range lengths {
		data := []byte(fmt.Sprintf("data-%d", i))
		sum := sha256.Sum256(data)
		a.Blocks = append(a.Blocks, &Block{
			ID:     fmt.Sprintf("b%d", i),
			Length: big.NewInt(l),
			Digest: hex.EncodeToString(sum[:]),
			Data:   data,
		})
	}
	return a
}

func ids(blocks []*Block) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, b.ID)
	}
	return out
}

func TestSampleReproducible(t *testing.T) {
	a := makeArchive(3, 1, 4, 1, 5, 9, 2, 6)
	p1, r1, err := Sample(a, "caller-seed", 4)
	if err != nil {
		t.Fatal(err)
	}
	p2, r2, err := Sample(a, "caller-seed", 4)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(p1)) != fmt.Sprint(ids(p2)) {
		t.Fatal("相同种子的抽样结果必须一致")
	}
	for i := range r1 {
		if r1[i].Roll.Cmp(r2[i].Roll) != 0 {
			t.Fatal("相同种子的抽样过程（随机值）必须一致")
		}
	}
}

func TestSampleWithoutReplacement(t *testing.T) {
	a := makeArchive(1, 2, 3, 4, 5)
	picked, _, err := Sample(a, "s", 5)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, b := range picked {
		if seen[b.ID] {
			t.Fatalf("块 %s 被重复抽中，违反无放回", b.ID)
		}
		seen[b.ID] = true
	}
	if len(picked) != 5 {
		t.Fatal("应抽中全部 5 个不同的块")
	}
}

// 恢复/追加抽样不得改变已选块：k 次抽样必须是 k+m 次抽样的前缀。
func TestSampleResumeKeepsPrefix(t *testing.T) {
	a := makeArchive(7, 1, 3, 9, 4, 4, 2, 8, 5, 6)
	short, _, err := Sample(a, "resume-seed", 3)
	if err != nil {
		t.Fatal(err)
	}
	long, _, err := Sample(a, "resume-seed", 8)
	if err != nil {
		t.Fatal(err)
	}
	for i := range short {
		if short[i].ID != long[i].ID {
			t.Fatalf("恢复抽样改变了已选块: 第 %d 位 %s -> %s", i, short[i].ID, long[i].ID)
		}
	}
}

func TestSampleTooMany(t *testing.T) {
	a := makeArchive(1, 2)
	if _, _, err := Sample(a, "s", 3); err == nil {
		t.Fatal("抽样数超过块总数应报错")
	}
}

// 极大总长度：单块权重接近 2^63，总和超过 2^64，验证大整数路径。
func TestSampleHugeTotalLength(t *testing.T) {
	a := &Archive{}
	huge := new(big.Int).Lsh(big.NewInt(1), 63) // 2^63
	for i := 0; i < 4; i++ {
		data := []byte(fmt.Sprintf("huge-%d", i))
		sum := sha256.Sum256(data)
		a.Blocks = append(a.Blocks, &Block{
			ID:     fmt.Sprintf("h%d", i),
			Length: new(big.Int).Add(huge, big.NewInt(int64(i))),
			Digest: hex.EncodeToString(sum[:]),
			Data:   data,
		})
	}
	total := a.TotalLength()
	if total.BitLen() != 66 { // 约 4 * 2^63 = 2^65
		t.Fatalf("总长度应为 66 位大整数，实际 %d 位", total.BitLen())
	}
	picked, records, err := Sample(a, "big-seed", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(picked) != 3 {
		t.Fatal("应抽中 3 块")
	}
	for _, r := range records {
		if r.Roll.Sign() < 0 || r.Roll.Cmp(r.Remaining) >= 0 {
			t.Fatalf("第 %d 抽随机值越界", r.Step)
		}
	}
}
