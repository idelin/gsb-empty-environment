package diskbtree

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "diskbtree-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func key(i int) []byte { return []byte(fmt.Sprintf("key%05d", i)) }
func val(i, round int) []byte {
	return []byte(fmt.Sprintf("val-%05d-r%d", i, round))
}

// collectAll 用范围扫描收集全部键值, 同时验证扫描结果严格递增(无重复)。
func collectAll(t *testing.T, it *Iterator) map[string]string {
	t.Helper()
	got := make(map[string]string)
	var prev []byte
	for it.Next() {
		k, v := it.Key(), it.Value()
		if prev != nil && bytes.Compare(prev, k) >= 0 {
			t.Fatalf("scan out of order or duplicate: %q then %q", prev, k)
		}
		prev = k
		got[string(k)] = string(v)
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// checkTree 校验结构不变量: 父子指针、分隔键、叶链三者一致。
func checkTree(t *testing.T, db *DB) {
	t.Helper()
	var chainLeaves []uint64 // 中序遍历得到的叶子序列
	var depthOfLeaf = -1
	var walk func(id uint64, lo, hi []byte, depth int)
	walk = func(id uint64, lo, hi []byte, depth int) {
		pg, err := db.readPage(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range pg.keys {
			if lo != nil && bytes.Compare(k, lo) < 0 {
				t.Fatalf("page %d: key %q below lower bound %q", id, k, lo)
			}
			if hi != nil && bytes.Compare(k, hi) >= 0 {
				t.Fatalf("page %d: key %q above upper bound %q", id, k, hi)
			}
		}
		if pg.typ == pageTypeLeaf {
			if depthOfLeaf >= 0 && depthOfLeaf != depth {
				t.Fatalf("leaves at different depths: %d vs %d", depthOfLeaf, depth)
			}
			depthOfLeaf = depth
			chainLeaves = append(chainLeaves, id)
			return
		}
		if len(pg.children) != len(pg.keys)+1 {
			t.Fatalf("page %d: %d keys but %d children", id, len(pg.keys), len(pg.children))
		}
		for i, c := range pg.children {
			var clo, chi []byte
			if i > 0 {
				clo = pg.keys[i-1]
			}
			if i < len(pg.keys) {
				chi = pg.keys[i]
			}
			walk(c, clo, chi, depth+1)
		}
	}
	db.mu.Lock()
	root := db.root
	db.mu.Unlock()
	walk(root, nil, nil, 0)

	// 逻辑叶链必须与父子指针的中序叶序列一致:
	// 每个叶子的 nextKey == 中序后继叶子的首键, 末尾叶子无后继。
	for i, id := range chainLeaves {
		pg, err := db.readPage(id)
		if err != nil {
			t.Fatal(err)
		}
		if i == len(chainLeaves)-1 {
			if pg.hasNext {
				t.Fatalf("last leaf %d should not have next", id)
			}
			continue
		}
		if !pg.hasNext {
			t.Fatalf("leaf %d missing next pointer", id)
		}
		succ, err := db.readPage(chainLeaves[i+1])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(pg.nextKey, succ.keys[0]) {
			t.Fatalf("leaf %d nextKey %q != successor first key %q", id, pg.nextKey, succ.keys[0])
		}
	}
}

func TestInsertGetScanSmallPages(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	db, err := Open(dir, &Options{PageSize: 128}) // 小页: 每页只装几条, 分裂频繁
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const n = 500
	for i := 0; i < n; i++ {
		if err := db.Put(key(i), val(i, 0)); err != nil {
			t.Fatal(err)
		}
	}
	checkTree(t, db)

	// 点查
	for i := 0; i < n; i++ {
		v, ok, err := db.Get(key(i))
		if err != nil || !ok {
			t.Fatalf("Get(%d) ok=%v err=%v", i, ok, err)
		}
		if string(v) != string(val(i, 0)) {
			t.Fatalf("Get(%d) = %q", i, v)
		}
	}
	if _, ok, _ := db.Get(key(n)); ok {
		t.Fatal("Get of missing key returned true")
	}

	// 全量扫描
	got := collectAll(t, db.Scan(nil, nil))
	if len(got) != n {
		t.Fatalf("full scan returned %d keys, want %d", len(got), n)
	}

	// 范围扫描 [100, 200)
	it := db.Scan(key(100), key(200))
	cnt := 0
	for it.Next() {
		cnt++
	}
	if cnt != 100 {
		t.Fatalf("range scan returned %d keys, want 100", cnt)
	}
}

func TestDuplicateKeyOverwrite(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	db, err := Open(dir, &Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const n = 100
	for round := 0; round < 3; round++ {
		for i := 0; i < n; i++ {
			if err := db.Put(key(i), val(i, round)); err != nil {
				t.Fatal(err)
			}
		}
	}
	checkTree(t, db)

	// 覆盖后每个键只出现一次, 且为最后一轮的值。
	got := collectAll(t, db.Scan(nil, nil))
	if len(got) != n {
		t.Fatalf("scan returned %d keys after overwrite, want %d", len(got), n)
	}
	for i := 0; i < n; i++ {
		if got[string(key(i))] != string(val(i, 2)) {
			t.Fatalf("key %d = %q, want %q", i, got[string(key(i))], val(i, 2))
		}
	}
}

// TestCrashRecovery 在提交协议的每个注入点、多个崩溃时机上模拟崩溃,
// 重开后验证: 状态要么是崩溃前最后一个已提交状态, 要么完整包含
// 崩溃中的那次 Put —— 不存在部分可见的中间态, 且树结构一致。
func TestCrashRecovery(t *testing.T) {
	points := []FaultPoint{FaultBeforeDataSync, FaultAfterDataSync, FaultAfterMetaWrite}
	for _, fp := range points {
		for crashAt := 1; crashAt <= 25; crashAt++ {
			dir := tempDir(t)
			db, err := Open(dir, &Options{PageSize: 128})
			if err != nil {
				t.Fatal(err)
			}
			committed := make(map[string]string)
			for i := 0; i < crashAt-1; i++ {
				if err := db.Put(key(i), val(i, 0)); err != nil {
					t.Fatal(err)
				}
				committed[string(key(i))] = string(val(i, 0))
			}
			// 武装钩子: 下一次 Put 在指定注入点"崩溃"。
			crashed := false
			db.SetCrashHook(func(f FaultPoint) bool { return f == fp })
			func() {
				defer func() {
					if r := recover(); r == ErrSimulatedCrash {
						crashed = true
					} else if r != nil {
						panic(r)
					}
				}()
				db.Put(key(crashAt-1), val(crashAt-1, 0))
			}()
			if !crashed {
				t.Fatalf("%s at op %d: expected simulated crash", fp, crashAt)
			}
			db.Close() // 模拟进程退出(句柄丢弃)

			// 模拟重启: 恢复。
			db2, err := Open(dir, &Options{PageSize: 128})
			if err != nil {
				t.Fatalf("%s at op %d: reopen: %v", fp, crashAt, err)
			}
			got := collectAll(t, db2.Scan(nil, nil))
			// 允许两种完整状态: 不含或完整包含崩溃中的那次 Put。
			inFlight := string(key(crashAt - 1))
			_, hasInFlight := got[inFlight]
			want := make(map[string]string)
			for k, v := range committed {
				want[k] = v
			}
			if hasInFlight {
				want[inFlight] = string(val(crashAt-1, 0))
			}
			if len(got) != len(want) {
				t.Fatalf("%s at op %d: got %d keys, want %d", fp, crashAt, len(got), len(want))
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("%s at op %d: key %q = %q, want %q", fp, crashAt, k, got[k], v)
				}
			}
			checkTree(t, db2)

			// 恢复后可以继续写入。
			if err := db2.Put(key(9999), val(9999, 0)); err != nil {
				t.Fatal(err)
			}
			checkTree(t, db2)
			db2.Close()
			os.RemoveAll(dir)
		}
	}
}

// TestSnapshotSurvivesRootSplit 根分裂及后续写入不影响旧快照。
func TestSnapshotSurvivesRootSplit(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	db, err := Open(dir, &Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const first = 60
	for i := 0; i < first; i++ {
		if err := db.Put(key(i), val(i, 0)); err != nil {
			t.Fatal(err)
		}
	}
	snap := db.Snapshot()

	// 继续大量写入(触发多次根分裂), 并覆盖快照前的键。
	for i := first; i < 400; i++ {
		if err := db.Put(key(i), val(i, 1)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < first; i++ {
		if err := db.Put(key(i), val(i, 1)); err != nil {
			t.Fatal(err)
		}
	}
	checkTree(t, db)

	// 旧快照: 恰好是前 first 个键的旧值。
	got := collectAll(t, snap.Scan(nil, nil))
	if len(got) != first {
		t.Fatalf("snapshot scan returned %d keys, want %d", len(got), first)
	}
	for i := 0; i < first; i++ {
		if got[string(key(i))] != string(val(i, 0)) {
			t.Fatalf("snapshot key %d = %q, want %q", i, got[string(key(i))], val(i, 0))
		}
	}
	// 新状态: 400 个键, 全是新值。
	cur := collectAll(t, db.Scan(nil, nil))
	if len(cur) != 400 {
		t.Fatalf("current scan returned %d keys, want 400", len(cur))
	}
	for i := 0; i < 400; i++ {
		if cur[string(key(i))] != string(val(i, 1)) {
			t.Fatalf("current key %d = %q, want %q", i, cur[string(key(i))], val(i, 1))
		}
	}
}

// TestRandomizedAgainstMap 随机插入/覆盖, 与 map 对照, 验证跨叶扫描无重复遗漏。
func TestRandomizedAgainstMap(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	db, err := Open(dir, &Options{PageSize: 96})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	want := make(map[string]string)
	seed := uint64(12345)
	rnd := func(n int) int { // 简单 LCG, 避免引入额外依赖
		seed = seed*6364136223846793005 + 1442695040888963407
		return int((seed >> 33) % uint64(n))
	}
	for op := 0; op < 2000; op++ {
		i := rnd(300)
		r := rnd(1000)
		if err := db.Put(key(i), val(i, r)); err != nil {
			t.Fatal(err)
		}
		want[string(key(i))] = string(val(i, r))
	}
	checkTree(t, db)

	got := collectAll(t, db.Scan(nil, nil))
	if len(got) != len(want) {
		t.Fatalf("scan returned %d keys, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("key %q = %q, want %q", k, got[k], v)
		}
	}

	// 随机区间扫描与排序后的 map 对照。
	var keys []string
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for trial := 0; trial < 50; trial++ {
		a, b := rnd(len(keys)), rnd(len(keys))
		if a > b {
			a, b = b, a
		}
		it := db.Scan([]byte(keys[a]), []byte(keys[b]))
		cnt := 0
		for it.Next() {
			cnt++
		}
		if cnt != b-a {
			t.Fatalf("range [%q,%q) returned %d, want %d", keys[a], keys[b], cnt, b-a)
		}
	}
}
