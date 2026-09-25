package diskdeque

import (
	"os"
	"testing"
)

// 两端操作只重写边界块：操作前后，中间块文件的内容必须逐字节不变。
func TestMiddleBlocksNotRewritten(t *testing.T) {
	d := openTest(t, &Options{BlockCapacity: 3, MaxBlocks: 64})
	pushRange(t, d, true,
		"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9")
	if len(d.m.blocks) < 3 {
		t.Fatalf("expected multiple blocks, got %d", len(d.m.blocks))
	}

	// 记录中间块编号与内容哈希
	midID := d.m.blocks[len(d.m.blocks)/2]
	path := d.dir + "/" + blockName(midID)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	pushRange(t, d, true, "r1", "r2")
	pushRange(t, d, false, "l1", "l2")

	tx := d.Begin()
	if _, err := tx.PopRight(); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.PopLeft(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("middle block %d disappeared or changed: %v", midID, err)
	}
	if string(after) != string(before) {
		t.Fatalf("middle block %d was rewritten", midID)
	}

	// 块文件数量与元素总数匹配，说明没有整块搬迁
	files := blockFiles(t, d.dir)
	if len(files) != len(d.m.blocks) {
		t.Fatalf("block files %d != meta blocks %d", len(files), len(d.m.blocks))
	}
}
