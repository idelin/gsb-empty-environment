package logalloc

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func tempDir(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "logalloc-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

// 预留 -> 提交 -> 发布 的正常路径，切点逐序推进。
func TestReserveCommitPublish(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	l, rep, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if rep.NextSeq != 1 || rep.Checkpoint != 0 {
		t.Fatalf("空日志恢复报告异常: %+v", rep)
	}

	a := l.Allocator()
	for i := uint64(1); i <= 3; i++ {
		seq, err := a.Reserve()
		if err != nil {
			t.Fatal(err)
		}
		if seq != i {
			t.Fatalf("期望序号 %d, 实际 %d", i, seq)
		}
		if err := a.Commit(seq, []byte(fmt.Sprintf("data-%d", seq))); err != nil {
			t.Fatal(err)
		}
	}

	p := l.Publisher()
	for i := uint64(1); i <= 3; i++ {
		out, err := p.Next()
		if err != nil {
			t.Fatal(err)
		}
		if out.Seq != i || out.Skipped || string(out.Data) != fmt.Sprintf("data-%d", i) {
			t.Fatalf("发布结果异常: %+v", out)
		}
	}
	if _, err := p.Next(); err != ErrNothingToPublish {
		t.Fatalf("期望 ErrNothingToPublish, 实际 %v", err)
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 预留后未提交形成空洞：发布流被挡住，显式跳过后才能推进。
func TestHoleBlocksPublishUntilSkipped(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	l, _, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	a := l.Allocator()
	s1, _ := a.Reserve()
	s2, _ := a.Reserve()
	s3, _ := a.Reserve()
	a.Commit(s1, []byte("a"))
	// s2 预留后"失败"，留下空洞
	a.Commit(s3, []byte("c"))

	p := l.Publisher()
	if _, err := p.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Next(); err != ErrHoleNotClosed {
		t.Fatalf("空洞应挡住发布流, 实际 %v", err)
	}
	if p.Checkpoint() != 1 {
		t.Fatalf("切点不应越过空洞, 实际 %d", p.Checkpoint())
	}

	// 显式跳过封闭空洞
	if err := a.Skip(s2); err != nil {
		t.Fatal(err)
	}
	out, err := p.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !out.Skipped || out.Seq != s2 {
		t.Fatalf("期望跳过记录, 实际 %+v", out)
	}
	out, err = p.Next()
	if err != nil || out.Seq != s3 || string(out.Data) != "c" {
		t.Fatalf("期望继续发布 s3, 实际 %+v, %v", out, err)
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 崩溃恢复：区分已预留未决定与已跳过，序号不重复分配，切点保持。
func TestRecoveryDistinguishesUndecidedAndSkipped(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()

	l, _, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := l.Allocator()
	s1, _ := a.Reserve()
	s2, _ := a.Reserve()
	s3, _ := a.Reserve()
	s4, _ := a.Reserve()
	a.Commit(s1, []byte("a"))
	a.Skip(s2) // 已跳过：终态
	a.Commit(s3, []byte("c"))
	// s4 预留后未决定 -> 崩溃前遗留的空洞
	p := l.Publisher()
	p.Next() // 发布 s1，切点 = 1
	l.Close()

	// 恢复
	l2, rep, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if !reflect.DeepEqual(rep.Undecided, []uint64{s4}) {
		t.Fatalf("未决定集合异常: %v", rep.Undecided)
	}
	if !reflect.DeepEqual(rep.Skipped, []uint64{s2}) {
		t.Fatalf("已跳过集合异常: %v", rep.Skipped)
	}
	if !reflect.DeepEqual(rep.Committed, []uint64{s3}) {
		t.Fatalf("待发布集合异常: %v", rep.Committed)
	}
	if rep.Checkpoint != 1 {
		t.Fatalf("切点应恢复为 1, 实际 %d", rep.Checkpoint)
	}
	if rep.NextSeq != 5 {
		t.Fatalf("分配起点应为 5, 实际 %d", rep.NextSeq)
	}

	// 不重复分配：新预留从 5 开始
	seq, err := l2.Allocator().Reserve()
	if err != nil || seq != 5 {
		t.Fatalf("恢复后分配应从 5 开始, 实际 %d, %v", seq, err)
	}

	// 发布流从切点继续：s2 是跳过记录，s3 有数据，s4 仍是空洞
	p2 := l2.Publisher()
	out, err := p2.Next()
	if err != nil || !out.Skipped || out.Seq != s2 {
		t.Fatalf("期望跳过 s2, 实际 %+v, %v", out, err)
	}
	out, err = p2.Next()
	if err != nil || out.Seq != s3 || string(out.Data) != "c" {
		t.Fatalf("期望数据 s3, 实际 %+v, %v", out, err)
	}
	if _, err := p2.Next(); err != ErrHoleNotClosed {
		t.Fatalf("s4 空洞应挡住发布流, 实际 %v", err)
	}
	// 恢复后裁决遗留空洞：补写数据封闭
	if err := l2.Allocator().Commit(s4, []byte("d")); err != nil {
		t.Fatal(err)
	}
	out, err = p2.Next()
	if err != nil || out.Seq != s4 || string(out.Data) != "d" {
		t.Fatalf("期望补发的 s4, 实际 %+v, %v", out, err)
	}
	if err := l2.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 决定不可更改：重复 commit/skip 报错；未预留不能决定。
func TestDecisionRules(t *testing.T) {
	dir, cleanup := tempDir(t)
	defer cleanup()
	l, _, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	a := l.Allocator()

	if err := a.Commit(99, nil); err != ErrNotReserved {
		t.Fatalf("期望 ErrNotReserved, 实际 %v", err)
	}
	seq, _ := a.Reserve()
	a.Commit(seq, []byte("x"))
	if err := a.Commit(seq, nil); err != ErrAlreadyDecided {
		t.Fatalf("期望 ErrAlreadyDecided, 实际 %v", err)
	}
	if err := a.Skip(seq); err != ErrAlreadyDecided {
		t.Fatalf("期望 ErrAlreadyDecided, 实际 %v", err)
	}
}

// 损坏的 WAL 必须在恢复时被拒绝：重复分配、切点不连续、切点越过空洞等。
func TestReplayRejectsCorruption(t *testing.T) {
	cases := map[string]string{
		"duplicate reserve": `{"type":"reserve","seq":1}
{"type":"reserve","seq":1}
`,
		"checkpoint gap": `{"type":"reserve","seq":1}
{"type":"commit","seq":1,"data":"eA=="}
{"type":"checkpoint","seq":2}
`,
		"checkpoint over hole": `{"type":"reserve","seq":1}
{"type":"checkpoint","seq":1}
`,
		"decide without reserve": `{"type":"commit","seq":7,"data":"eA=="}
`,
		"double decision": `{"type":"reserve","seq":1}
{"type":"skip","seq":1}
{"type":"commit","seq":1,"data":"eA=="}
`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir, cleanup := tempDir(t)
			defer cleanup()
			if err := ioutil.WriteFile(filepath.Join(dir, "logalloc.wal"), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Open(dir); err == nil {
				t.Fatalf("损坏 WAL 应被拒绝: %s", name)
			}
		})
	}
}
