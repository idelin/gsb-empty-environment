package merkle

import (
	"bytes"
	"testing"
)

func TestRootDeterministicAndSensitive(t *testing.T) {
	recs := makeRecords(1000)
	r1 := Build(recs).Root()
	r2 := Build(makeRecords(1000)).Root()
	if r1 != r2 {
		t.Fatal("相同输入应产生相同根哈希")
	}
	recs[500] = []byte("tampered")
	if Build(recs).Root() == r1 {
		t.Fatal("篡改任意记录后根哈希必须变化")
	}
}

func TestTreeShape(t *testing.T) {
	tree := Build(makeRecords(1000000))
	if tree.Count() != 1000000 {
		t.Fatalf("Count 应为 1000000，得到 %d", tree.Count())
	}
	// 16 叉树：1e6 -> 62500 -> 3907 -> 245 -> 16 -> 1，共 6 层
	if tree.Height() != 6 {
		t.Fatalf("100 万条记录的 16 叉树应为 6 层，得到 %d", tree.Height())
	}
}

func TestProofRoundTrip(t *testing.T) {
	recs := makeRecords(1000)
	tree := Build(recs)
	root := tree.Root()
	for _, idx := range []uint64{0, 1, 15, 16, 17, 500, 998, 999} {
		proof, err := tree.Prove(idx)
		if err != nil {
			t.Fatalf("生成证明失败: %v", err)
		}
		if !Verify(root, recs[idx], proof) {
			t.Fatalf("下标 %d 的包含证明校验失败", idx)
		}
	}
}

func TestProofRejectsTampering(t *testing.T) {
	recs := makeRecords(1000)
	tree := Build(recs)
	root := tree.Root()
	proof, err := tree.Prove(500)
	if err != nil {
		t.Fatal(err)
	}
	if Verify(root, []byte("forged-record"), proof) {
		t.Fatal("篡改的记录不应通过校验")
	}
	other := Build(makeRecords(1000))
	otherRoot := other.Root()
	// 针对另一棵树的根校验（记录 500 被改）
	recs2 := makeRecords(1000)
	recs2[500] = []byte("tampered")
	if bytes.Equal(root[:], otherRoot[:]) == false {
		t.Fatal("相同记录的两棵树根应一致")
	}
	if Verify(Build(recs2).Root(), recs[500], proof) {
		t.Fatal("证明不应匹配被篡改后的树根")
	}
}

func TestProveOutOfRange(t *testing.T) {
	tree := Build(makeRecords(10))
	if _, err := tree.Prove(10); err != ErrIndexOutOfRange {
		t.Fatalf("越界下标应返回 ErrIndexOutOfRange，得到 %v", err)
	}
	if _, err := tree.Prove(9); err != nil {
		t.Fatalf("合法下标不应报错: %v", err)
	}
}

func TestEmptyTree(t *testing.T) {
	tree := Build(nil)
	if tree.Root() != Zero || tree.Height() != 0 || tree.Count() != 0 {
		t.Fatal("空树的根应为 Zero，高度与计数应为 0")
	}
}
