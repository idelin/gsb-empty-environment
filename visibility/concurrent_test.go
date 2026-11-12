package visibility

import (
	"sync"
	"testing"
)

// 并发冒烟：写入/删除/压缩、授权变更、句柄发放/释放与多读者查询同时进行，
// 依赖 go test -race 捕获数据竞争；查询要么成功，要么返回句柄已释放错误。
func TestConcurrentAccess(t *testing.T) {
	ix := New()
	az := NewAuthz().Grant("d0", "d1", "d2")

	const writers = 3
	var wg sync.WaitGroup

	// 长期读者：持有的根与快照在整个测试期间有效。
	readerRoot := ix.Head()
	readerSnap := az.Snapshot()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			if _, err := ix.Query(readerRoot, readerSnap, []string{"a", "b"}); err != nil && err != ErrRootReleased && err != ErrSnapshotReleased {
				t.Errorf("concurrent query: %v", err)
				return
			}
		}
	}()

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			doc := DocID("d" + string(rune('0'+w)))
			for i := 0; i < 200; i++ {
				r := ix.Put(doc, []string{"a", "b"})
				ix.Compact()
				_ = ix.Delete(doc)
				r.Release()
			}
		}(w)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		snaps := make([]*Snapshot, 0, 12)
		for i := 0; i < 200; i++ {
			snaps = append(snaps, az.Snapshot())
			az.Revoke("d1")
			az.Grant("d1")
			if len(snaps) > 10 {
				snaps[0].Release()
				snaps = snaps[1:]
			}
		}
		for _, s := range snaps {
			s.Release()
		}
	}()

	wg.Wait()
	if err := readerRoot.Release(); err != nil {
		t.Fatal(err)
	}
	if err := readerSnap.Release(); err != nil {
		t.Fatal(err)
	}
}
