package trie

import (
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findChildRef 沿 key 走到"最后一条边"，返回 (标签, 终节点引用, 父节点引用)。
func findChildRef(t *testing.T, s *Store, root NodeID, key string) (byte, NodeID, NodeID) {
	t.Helper()
	parent := root
	for i := 0; i < len(key)-1; i++ {
		n, err := s.loadNode(parent)
		if err != nil {
			t.Fatal(err)
		}
		parent = n.children[key[i]]
	}
	n, err := s.loadNode(parent)
	if err != nil {
		t.Fatal(err)
	}
	tag := key[len(key)-1]
	child, ok := n.children[tag]
	if !ok {
		t.Fatalf("键 %q 的末条边不存在", key)
	}
	return tag, child, parent
}

func TestCorruptedReferenceIsLocated(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)

	mustPut(t, s, "ab", "v-ab")
	mustPut(t, s, "xy", "v-xy")
	root, _ := s.Root()

	// 破坏 "ab" 叶子（与 "xy" 位于完全不同的子树）。
	tag, victim, parent := findChildRef(t, s, root, "ab")
	if victim.IsZero() || parent.IsZero() || tag != 'b' {
		t.Fatalf("定位测试前置异常 tag=%q", tag)
	}
	vp := s.shardPath(victim)
	if err := ioutil.WriteFile(vp, []byte("CORRUPTED-BYTES"), 0644); err != nil {
		t.Fatal(err)
	}
	// 模拟新进程视角（或外部篡改）：重开后缓存为空，强制读盘校验。
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, dir)
	root, _ = s.Root()

	// 经 Get 读取必须返回带节点引用、父引用、边标签的 VerifyError。
	_, err := s.Get([]byte("ab"))
	ve, ok := err.(*VerifyError)
	if !ok {
		t.Fatalf("期望 *VerifyError, 得到 %T: %v", err, err)
	}
	if ve.NodeID != victim {
		t.Fatalf("损坏定位错误: got %s want %s", ve.NodeID.Hex()[:12], victim.Hex()[:12])
	}
	if ve.Parent != parent || ve.Tag == nil || *ve.Tag != 'b' {
		t.Fatalf("VerifyError 缺少父节点/边标签定位信息: parent=%v tag=%v", !ve.Parent.IsZero(), ve.Tag)
	}
	if !strings.Contains(ve.Error(), victim.Hex()) {
		t.Fatalf("错误信息应包含完整节点引用, got: %v", ve)
	}
	t.Logf("损坏定位信息: %v", ve)

	// Verify() 巡检同样定位到该节点。
	if err := s.Verify(); err == nil {
		t.Fatal("Verify 应发现损坏")
	} else if v2, ok := err.(*VerifyError); !ok || v2.NodeID != victim {
		t.Fatalf("Verify 定位错误: %v", err)
	}

	// 另一棵子树 "xy" 不受影响（内容寻址、不可变节点相互独立）。
	if _, err := s.Get([]byte("xy")); err != nil {
		t.Fatalf("未损坏的兄弟子树应可读: %v", err)
	}
}

func TestDanglingReferenceIsLocated(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)

	mustPut(t, s, "ab", "v-ab")
	mustPut(t, s, "xy", "v-xy")
	root, _ := s.Root()
	_, victim, parent := findChildRef(t, s, root, "ab")

	if err := os.Remove(s.shardPath(victim)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = reopen(t, dir)
	_, err := s.Get([]byte("ab"))
	ve, ok := err.(*VerifyError)
	if !ok {
		t.Fatalf("期望 *VerifyError, 得到 %v", err)
	}
	if ve.NodeID != victim || ve.Parent != parent {
		t.Fatalf("悬挂引用定位错误: node=%s parent-match=%v", ve.NodeID.Hex()[:12], ve.Parent == parent)
	}
	if !strings.Contains(ve.Reason, "缺失") {
		t.Fatalf("原因应说明文件缺失: %s", ve.Reason)
	}
}

func TestTamperedRootFailsVerification(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)
	mustPut(t, s, "ab", "v")
	root, _ := s.Root()

	// 破坏根节点自身：VerifyError 应标为根节点（Parent 为零）。
	if err := ioutil.WriteFile(s.shardPath(root), []byte("xxxx"), 0644); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = reopen(t, dir)
	root, _ = s.Root()
	if err := s.Verify(); err == nil {
		t.Fatal("根损坏必须被 Verify 发现")
	} else if ve, ok := err.(*VerifyError); !ok || ve.NodeID != root || !ve.Parent.IsZero() {
		t.Fatalf("根损坏定位错误: %v", err)
	}

	// 清单里写一个根本不存在的根引用：打开/快照校验应失败。
	bad := strings.Repeat("ab", 32)
	manifest := `{"version":1,"root":"` + bad + `","sequence":9,"roots":{}}`
	if err := os.Remove(filepath.Join(dir, rootsBackup)); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(filepath.Join(dir, rootsFile), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	s3, err := Open(dir)
	if err != nil {
		t.Fatalf("含非法根引用的清单结构性可读时应能打开（惰性校验）: %v", err)
	}
	defer s3.Close()
	err = s3.Verify()
	if err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("Verify 应报告清单根引用损坏并给出引用, 得到: %v", err)
	}
}

// hookPhase 定义一个注入场景：在 hook 位置返回 ErrInjected，模拟进程被杀。
type hookPhase struct {
	name      string
	install   func(h *FaultHooks, fire func() error)
	committed bool // 注入点是否已过提交点（重开后应看到新根）
}

func onceErr(err error) func() error {
	fired := false
	return func() error {
		if fired {
			return nil
		}
		fired = true
		return err
	}
}

var allPhaseNames = []string{
	"BeforeFlushNodes", "BeforeNodeRename", "BeforeNodeDirSync",
	"BeforeRootsWrite", "BeforeRootsRename", "BeforeRootsDirSync",
	"BeforeRootsBackup", "AfterCommit",
}

func hooksFor(phase string) FaultHooks {
	h := FaultHooks{}
	fire := onceErr(ErrInjected)
	switch phase {
	case "BeforeFlushNodes":
		h.BeforeFlushNodes = fire
	case "BeforeNodeRename":
		h.BeforeNodeRename = fire
	case "BeforeNodeDirSync":
		h.BeforeNodeDirSync = fire
	case "BeforeRootsWrite":
		h.BeforeRootsWrite = fire
	case "BeforeRootsRename":
		h.BeforeRootsRename = fire
	case "BeforeRootsDirSync":
		h.BeforeRootsDirSync = fire
	case "BeforeRootsBackup":
		h.BeforeRootsBackup = fire
	case "AfterCommit":
		h.AfterCommit = fire
	}
	return h
}

func TestInjectedCrashRecovery(t *testing.T) {
	var phases []hookPhase
	for i, name := range allPhaseNames {
		name := name
		phases = append(phases, hookPhase{
			name:    name,
			install: func(h *FaultHooks, fire func() error) { *h = hooksFor(name) },
			// i=5 是 BeforeRootsDirSync：roots.json rename（提交点）已过，重开后新根已生效。
			committed: i >= 5,
		})
	}

	for _, ph := range phases {
		ph := ph
		t.Run(ph.name, func(t *testing.T) {
			s, dir := openTestStore(t)
			defer os.RemoveAll(dir)
			mustPut(t, s, "seed", "s")
			oldRoot, oldSeq := s.Root()

			s.SetFaultHooks(hooksFor(ph.name))
			_, err := s.Put([]byte("crash-key"), []byte("v"))
			if err != ErrInjected {
				t.Fatalf("%s: 期望 ErrInjected, 得到 %v", ph.name, err)
			}

			// 同一进程内的内存状态：
			// 提交点之前 -> 旧根、旧序列号；提交点之后 -> 磁盘已是新根。
			curRoot, curSeq := s.Root()
			if ph.committed {
				// BeforeRootsDirSync / BeforeRootsBackup：磁盘已提交，
				// 内存尚未发布（AfterCommit 除外）；直接模拟进程死亡重启。
				if ph.name == "AfterCommit" && curRoot == oldRoot {
					t.Fatal("AfterCommit 后内存应为新根")
				}
			} else {
				if curRoot != oldRoot || curSeq != oldSeq {
					t.Fatalf("%s: 中止后内存必须保持旧根与旧序列号", ph.name)
				}
				// 事务中止后存储仍可继续使用（再写一次成功）。
				s.SetFaultHooks(FaultHooks{})
				if _, err := s.Put([]byte("after-retry"), []byte("v")); err != nil {
					t.Fatalf("中止后重试写入失败: %v", err)
				}
			}

			// "进程重启"：重新 Open。临时文件被清扫，清单要么旧要么新，绝不半损坏。
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s2 := reopen(t, dir)
			if err := s2.Verify(); err != nil {
				t.Fatalf("%s: 重启后 Verify 失败: %v", ph.name, err)
			}
			if !ph.committed {
				if ok, _ := s2.Has([]byte("crash-key")); ok {
					t.Fatalf("%s: 提交点前崩溃，重启后不应看到 crash-key", ph.name)
				}
				assertGet(t, s2, "seed", "s")
			} else {
				assertGet(t, s2, "crash-key", "v")
				assertGet(t, s2, "seed", "s")
			}
		})
	}
}

func TestOrphanedNodesAfterCrashReclaimed(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)
	mustPut(t, s, "k1", "v1")

	s.SetFaultHooks(FaultHooks{BeforeRootsRename: onceErr(ErrInjected)})
	if _, err := s.Put([]byte("k2"), []byte("v2")); err != ErrInjected {
		t.Fatalf("期望注入错误, 得到 %v", err)
	}
	s.SetFaultHooks(FaultHooks{})

	// 节点已落盘但根没切换 -> 无主垃圾。重启后 Compact 回收。
	s2 := reopen(t, dir)
	removed, err := s2.Compact()
	if err != nil {
		t.Fatal(err)
	}
	if removed == 0 {
		t.Fatal("崩溃前落盘但未提交的节点应被 Compact 回收")
	}
	assertGet(t, s2, "k1", "v1")
	if ok, _ := s2.Has([]byte("k2")); ok {
		t.Fatal("k2 从未提交, 不应存在")
	}
}

func TestBackupManifestRecovery(t *testing.T) {
	s, dir := openTestStore(t)
	defer os.RemoveAll(dir)
	mustPut(t, s, "k", "v1")
	if _, err := s.Snapshot("tag1", s.CurrentView().Root()); err != nil {
		t.Fatal(err)
	}

	// 破坏主清单（模拟断电导致主文件损坏），备份完好。
	if err := ioutil.WriteFile(filepath.Join(dir, rootsFile), []byte("{ broken json"), 0644); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("主清单损坏时应回退备份: %v", err)
	}
	assertGet(t, s2, "k", "v1")
	if got := s2.Snapshots(); len(got) != 1 || got[0].Label != "tag1" {
		t.Fatalf("备份中的标签应保留: %+v", got)
	}
}

func TestNodeIDParseAndFormat(t *testing.T) {
	if _, err := ParseNodeID("zz"); err == nil {
		t.Fatal("非法十六进制应报错")
	}
	if _, err := ParseNodeID(hex.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("长度错误应报错")
	}
	if !zeroID.IsZero() {
		t.Fatal("零值应为空")
	}
	id := hashNode([]byte("x"))
	back, err := ParseNodeID(id.Hex())
	if err != nil || back != id {
		t.Fatal("Hex/ParseNodeID 往返失败")
	}
}
