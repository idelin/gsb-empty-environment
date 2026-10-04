package main

import (
	"math/big"
	"testing"
)

func TestEnumCheckProbabilities(t *testing.T) {
	a := makeArchive(1, 2, 3)
	res, err := EnumCheck(a, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if res.Depth != 2 {
		t.Fatal("小归档应枚举到深度 2")
	}
	// 第一抽：命中次数必须严格等于权重
	for i, w := range []int64{1, 2, 3} {
		got := res.FirstCounts[a.Blocks[i].ID]
		if got == nil || got.Cmp(big.NewInt(w)) != 0 {
			t.Fatalf("块 %s 命中 %v 次，期望 %d", a.Blocks[i].ID, got, w)
		}
	}
	// 两抽：有序对 (i,j) 命中次数必须等于 w_i*w_j
	ws := []int64{1, 2, 3}
	for i := range ws {
		for j := range ws {
			key := a.Blocks[i].ID + "->" + a.Blocks[j].ID
			got := res.PairCounts[key]
			if i == j {
				if got != nil && got.Sign() != 0 {
					t.Fatalf("无放回抽样中 %s 不应出现", key)
				}
				continue
			}
			want := ws[i] * ws[j]
			if got == nil || got.Cmp(big.NewInt(want)) != 0 {
				t.Fatalf("有序对 %s 命中 %v 次，期望 %d", key, got, want)
			}
		}
	}
	if problems := res.Verify(a); len(problems) != 0 {
		t.Fatalf("枚举核对应通过: %v", problems)
	}
}

func TestEnumCheckRejectsLargeArchive(t *testing.T) {
	a := makeArchive(1 << 30)
	if _, err := EnumCheck(a, 1<<20); err == nil {
		t.Fatal("超过枚举上限应报错")
	}
}

func TestEnumVerifyDetectsTampering(t *testing.T) {
	a := makeArchive(1, 2)
	res, err := EnumCheck(a, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// 人为篡改计数，Verify 必须能发现
	res.FirstCounts[a.Blocks[0].ID] = big.NewInt(99)
	if problems := res.Verify(a); len(problems) == 0 {
		t.Fatal("篡改后的枚举结果不应通过校验")
	}
}
