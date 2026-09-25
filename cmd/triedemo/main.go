// Command triedemo 演示不可变路径复制字节前缀树：
// 前缀扫描、删除压缩、历史根快照、路径复制共享与引用校验。
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	trie "immutabletrie"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func short(id trie.NodeID) string {
	h := id.Hex()
	if h == "" {
		return "<空>"
	}
	return h[:12]
}

func dump(s *trie.Store, title string) {
	fmt.Println("--- " + title + " ---")
	err := s.ScanPrefix(nil, func(kv trie.KV) error {
		fmt.Printf("  %q = %q\n", display(kv.Key), string(kv.Value))
		return nil
	})
	must(err)
}

func display(k []byte) string {
	for _, b := range k {
		if b < 0x20 || b > 0x7e {
			return "0x" + hex.EncodeToString(k)
		}
	}
	return string(k)
}

func main() {
	dir := flag.String("dir", filepath.Join(os.TempDir(), "triedemo"), "数据目录（调用方指定）")
	flag.Parse()

	// 允许重复运行：清空后从零开始，保证样例输出确定。
	must(os.RemoveAll(*dir))
	s, err := trie.Open(*dir)
	must(err)
	defer s.Close()
	fmt.Println("数据目录:", *dir)

	// 1) 写入：二进制键、空键与普通键。
	keys := []string{"abc", "ab", "a", "abd"}
	for _, k := range keys {
		_, err := s.Put([]byte(k), []byte("v:"+k))
		must(err)
	}
	emptyRoot, err := s.Put(nil, []byte("v:<empty>"))
	must(err)
	binRoot, err := s.Put([]byte{0x00, 0xff, 0x10}, []byte("v:binary"))
	must(err)
	dump(s, "初始树（字典序，空键最前，二进制键以 0x 显示）")
	fmt.Printf("当前根: %s\n", short(binRoot))
	_ = emptyRoot

	// 2) 前缀扫描。
	fmt.Println("--- 前缀扫描 ab ---")
	must(s.ScanPrefix([]byte("ab"), func(kv trie.KV) error {
		fmt.Printf("  %q = %q\n", display(kv.Key), string(kv.Value))
		return nil
	}))

	// 3) 删 "ab" 不能删掉 "abc"。
	r1, _ := s.Root()
	_, err = s.Delete([]byte("ab"))
	must(err)
	dump(s, `删除 "ab" 后（"abc" 仍在）`)
	has, err := s.Has([]byte("abc"))
	must(err)
	fmt.Printf(`"abc" 仍然存在: %v`+"\n", has)
	r2, _ := s.Root()

	// 4) 历史根仍可读旧值。
	old := s.ViewAt(r1)
	v, err := old.Get([]byte("ab"))
	fmt.Printf("历史根 %s 上 ab = %q（删除前的旧值）\n", short(r1), string(v))
	must(err)
	if _, err := s.ViewAt(r2).Get([]byte("ab")); err != trie.ErrNotFound {
		must(fmt.Errorf("期望新根上 ab 已删除"))
	}
	fmt.Println("当前根上 ab 已删除")

	// 5) 路径复制：只改一片叶子，不重写整树。
	before := s.Stats().NodesWritten
	r3, err := s.Put([]byte("abc"), []byte("updated"))
	must(err)
	delta := s.Stats().NodesWritten - before
	fmt.Printf("更新叶子 abc：新根 %s，本次仅写盘 %d 个节点（路径长度），兄弟子树未重写\n", short(r3), delta)

	// 6) 根快照打标签。
	tagRoot, err := s.Snapshot("release-1", r2)
	must(err)
	fmt.Printf("快照 release-1 -> 根 %s\n", short(tagRoot))
	for _, info := range s.Snapshots() {
		fmt.Printf("  标签 %s @seq=%d root=%s\n", info.Label, info.Sequence, short(info.Root))
	}

	// 7) 删除压缩 + Compact 物理回收。
	_, err = s.Delete([]byte("abd"))
	must(err)
	_, err = s.Delete([]byte("a"))
	must(err)
	removed, err := s.Compact()
	must(err)
	dump(s, "继续删除 a / abd 并 Compact 后")
	fmt.Printf("Compact 物理回收无主节点: %d 个；release-1 历史根仍受保护\n", removed)

	fmt.Println("--- 损坏注入（直接改写磁盘字节） ---")
	// 取当前根上 abc 叶子的实际节点引用（Compact 后旧根节点可能已回收）。
	curRootID, _ := s.Root()
	curView := s.ViewAt(curRootID)
	victimID, err := trie.LeafNodeID(curView, []byte("abc"))
	must(err)
	idHex := victimID.Hex()
	target := filepath.Join(*dir, "nodes", idHex[:2], idHex)
	must(ioutil.WriteFile(target, []byte("garbage-not-a-node"), 0644))
	// Verify 绕过内存缓存强制读盘，模拟巡检 / 新进程读到外部损坏。
	if err := s.Verify(); err != nil {
		if ve, ok := err.(*trie.VerifyError); ok {
			fmt.Printf("校验失败并定位到节点 %s\n", ve.NodeID.Hex())
			fmt.Printf("  原因: %s\n", ve.Reason)
		} else {
			must(err)
		}
	} else {
		must(fmt.Errorf("期望 VerifyError"))
	}
}
