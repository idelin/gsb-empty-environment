package trie

import (
	"bytes"
	"io/ioutil"
	"os"
	"testing"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir, err := ioutil.TempDir("", "trie-test-")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return s, dir
}

func reopen(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustPut(t *testing.T, s *Store, k, v string) NodeID {
	t.Helper()
	id, err := s.Put([]byte(k), []byte(v))
	if err != nil {
		t.Fatalf("Put(%q): %v", k, err)
	}
	return id
}

func assertGet(t *testing.T, s interface {
	Get([]byte) ([]byte, error)
}, k, want string) {
	t.Helper()
	got, err := s.Get([]byte(k))
	if err != nil {
		t.Fatalf("Get(%q): %v", k, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, 想得到 %q", k, got, want)
	}
}

func TestPutGetEmptyAndBinaryKeys(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)

	binkey := []byte{0x00, 0x00, 0xff, 0x10, 0x00}
	big := bytes.Repeat([]byte{0xab}, 1000)

	if _, err := s.Put(nil, []byte("empty-val")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(binkey, big); err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, "", "empty-val") // 空键重复写
	mustPut(t, s, "a", "A")

	assertGet(t, s, "", "empty-val")
	assertGet(t, s, "a", "A")
	got, err := s.Get(binkey)
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("二进制键读回失败: got=%d err=%v", len(got), err)
	}
	if _, err := s.Get([]byte("missing")); err != ErrNotFound {
		t.Fatalf("不存在的键应 ErrNotFound, 得到 %v", err)
	}

	// 值切片的防御性拷贝：调用方事后修改不影响存储。
	mut := []byte("orig")
	if _, err := s.Put([]byte("k"), mut); err != nil {
		t.Fatal(err)
	}
	mut[0] = 'X'
	assertGet(t, s, "k", "orig")

	// 重开进程后依旧可读。
	s.Close()
	s2 := reopen(t, dir)
	assertGet(t, s2, "", "empty-val")
	assertGet(t, s2, "a", "A")
	if got, err := s2.Get(binkey); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("重启后二进制键读回失败")
	}
}

func TestDeletePrefixDoesNotDeleteChildren(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)
	for _, k := range []string{"a", "ab", "abc", "abd", "abe", "abx", "ac"} {
		mustPut(t, s, k, "v:"+k)
	}
	if _, err := s.Delete([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"a", "abc", "abd", "abe", "abx", "ac"} {
		if ok, _ := s.Has([]byte(k)); !ok {
			t.Fatalf("删 ab 后 %q 不应被删除", k)
		}
	}
	if ok, _ := s.Has([]byte("ab")); ok {
		t.Fatal(`删 ab 后 "ab" 自身应不存在`)
	}
	// 删不存在的键。
	if _, err := s.Delete([]byte("zzz")); err != ErrNotFound {
		t.Fatalf("删不存在键应 ErrNotFound, 得到 %v", err)
	}
}

func TestDeleteCompactionShrinksNodeAndGC(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)
	rBefore, _ := s.Root()
	mustPut(t, s, "abc", "1")
	rAfterPut, _ := s.Root()
	mustPut(t, s, "abc", "2") // 覆盖，只写路径上的新节点
	rAfterUpdate, _ := s.Root()

	if _, err := s.Delete([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	rEmpty, seq := s.Root()
	if !rEmpty.IsZero() {
		t.Fatal("唯一键删除后根应回到零值（整树被压缩消失）")
	}

	// Compact 回收：当前空树不引用任何节点，旧节点全部应被回收。
	removed, err := s.Compact()
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("Compact 应回收无主节点")
	}
	// 当前空树工作正常。
	if _, err := s.Get([]byte("abc")); err != ErrEmptyTree {
		t.Fatalf("空树应 ErrEmptyTree, 得到 %v", err)
	}

	// 给历史根打标签可以阻止回收。
	s2 := reopen(t, dir)
	if _, err := s2.Snapshot("old", rAfterUpdate); err == nil {
		t.Fatal("Compact 之后历史节点已被物理删除，对其打标签应失败（悬挂引用）")
	}

	// 再开一个干净目录验证标签保护。
	s3, dir3 := openTestStore(t)
	defer os.RemoveAll(dir3)
	mustPut(t, s3, "keep-tree/key", "1")
	rootKeep, _ := s3.Root()
	if _, err := s3.Snapshot("keep", rootKeep); err != nil {
		t.Fatal(err)
	}
	// 当前根与被保护根完全不相交的新子树：Compact 不应删除任何节点。
	mustPut(t, s3, "zzz-new-key", "2")
	n, err := s3.Compact()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("被标签根与当前根的全部可达节点都应保留, 实际删除 %d", n)
	}
	assertGet(t, s3.ViewAt(rootKeep), "keep-tree/key", "1")
	assertGet(t, s3, "zzz-new-key", "2")

	// 未使用变量安抚
	_ = rBefore
	_ = rAfterPut
	_ = seq
}

func TestSnapshotHistoryAcrossRestart(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)
	mustPut(t, s, "ab", "v1")
	r1, _ := s.Root()
	mustPut(t, s, "abc", "v2")
	r2, _ := s.Root()
	mustPut(t, s, "ab", "v3")
	r3, _ := s.Root()

	for label, root := range map[string]NodeID{"r1": r1, "r2": r2} {
		if _, err := s.Snapshot(label, root); err != nil {
			t.Fatal(err)
		}
	}

	// 当前根：ab=v3，r1 上 ab=v1。
	assertGet(t, s, "ab", "v3")
	assertGet(t, s.ViewAt(r1), "ab", "v1")
	if _, err := s.ViewAt(r1).Get([]byte("abc")); err != ErrNotFound {
		t.Fatalf("r1 上不应有 abc, 得到 %v", err)
	}
	assertGet(t, s.ViewAt(r2), "abc", "v2")
	assertGet(t, s.ViewAt(r3), "ab", "v3")

	// 重启后当前根与标签全部保留，历史值仍可读。
	s.Close()
	s2 := reopen(t, dir)
	assertGet(t, s2, "ab", "v3")
	assertGet(t, s2.ViewAt(r1), "ab", "v1")
	labels := s2.Snapshots()
	if len(labels) != 2 || labels[0].Label != "r1" || labels[1].Label != "r2" {
		t.Fatalf("快照标签异常: %+v", labels)
	}

	// 覆盖标签、删除标签。
	if _, err := s2.Snapshot("r1", r3); err != nil {
		t.Fatal(err)
	}
	assertGet(t, s2.ViewAt(r3), "ab", "v3")
	if err := s2.DeleteSnapshot("r1"); err != nil {
		t.Fatal(err)
	}
	if err := s2.DeleteSnapshot("r1"); err != ErrSnapshotMissing {
		t.Fatalf("重复删除标签应 ErrSnapshotMissing, 得到 %v", err)
	}
}

func TestPathCopySharing(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)
	// 建立有兄弟子树的树。
	mustPut(t, s, "prefix/aaa/1", "1")
	mustPut(t, s, "prefix/aaa/2", "2")
	mustPut(t, s, "prefix/bbb/1", "3")
	baseWritten := s.Stats().NodesWritten

	before, _ := s.Root()
	// 只改一片叶子。
	nr, err := s.Put([]byte("prefix/aaa/1"), []byte("updated"))
	if err != nil {
		t.Fatal(err)
	}
	delta := s.Stats().NodesWritten - baseWritten
	if delta == 0 {
		t.Fatal("更新应写入新节点")
	}

	// 新根与旧根共享兄弟子树：找 "prefix/" 之后 bbb 分支的节点引用，应相同。
	childAt := func(root NodeID, key string) NodeID {
		id := root
		for i := 0; i < len(key); i++ {
			n, err := s.loadNode(id)
			if err != nil {
				t.Fatal(err)
			}
			id = n.children[key[i]]
		}
		return id
	}
	oldBBB := childAt(before, "prefix/bbb")
	newBBB := childAt(nr, "prefix/bbb")
	if oldBBB.IsZero() || oldBBB != newBBB {
		t.Fatalf("未改动的兄弟子树必须共享同一节点引用: old=%s new=%s", oldBBB.Hex()[:8], newBBB.Hex()[:8])
	}

	// 写路径长度 = key 长度 + 1（含根），因为每个节点都不同。
	if int(delta) != len("prefix/aaa/1")+1 {
		t.Fatalf("只应重写路径上的 %d 个节点, 实际 %d", len("prefix/aaa/1")+1, delta)
	}

	// 幂等写相同值：零写入、根不变。
	idBefore, _ := s.Root()
	nr2, err := s.Put([]byte("prefix/aaa/1"), []byte("updated"))
	if err != nil {
		t.Fatal(err)
	}
	if nr2 != idBefore {
		t.Fatal("相同值重复写不应产生新根")
	}
	if d := s.Stats().NodesWritten - baseWritten - delta; d != 0 {
		t.Fatalf("幂等写不应写盘, 实际 %d", d)
	}
}

func TestPrefixScanOrder(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)
	s.Put(nil, []byte("EMPTY"))
	for _, k := range []string{"a", "ab", "abc", "abd", "b", "\x00binary", "aa", "a\x00"} {
		mustPut(t, s, k, "v")
	}
	var got []string
	err := s.ScanPrefix([]byte("a"), func(kv KV) error {
		got = append(got, string(kv.Key))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "a\x00", "aa", "ab", "abc", "abd"}
	if len(got) != len(want) {
		t.Fatalf("前缀扫描结果数不符: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("前缀扫描顺序错误: got=%q want=%q", got, want)
		}
	}

	// 全树扫描：空键在最前，严格字节字典序。
	var all []string
	if err := s.ScanPrefix(nil, func(kv KV) error {
		all = append(all, string(kv.Key))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if all[0] != "" {
		t.Fatalf("空键应排最前, 得到 %q", all[0])
	}
	for i := 1; i < len(all); i++ {
		if all[i-1] > all[i] {
			t.Fatalf("扫描结果非字典序: %q > %q", all[i-1], all[i])
		}
	}

	// 扫描回调可中止。
	count := 0
	err = s.ScanPrefix(nil, func(kv KV) error {
		count++
		if count == 2 {
			return errUserStop
		}
		return nil
	})
	if err != errUserStop || count != 2 {
		t.Fatalf("扫描中止异常 err=%v count=%d", err, count)
	}
}

var errUserStop = stringErr("stop")

type stringErr string

func (e stringErr) Error() string { return string(e) }
