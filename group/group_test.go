package group

import (
	"strings"
	"testing"
)

func mustSame(t *testing.T, s *Store, a, b string) {
	t.Helper()
	if err := s.LinkSame(a, b); err != nil {
		t.Fatalf("LinkSame(%s,%s) 意外失败: %v", a, b, err)
	}
}

func mustForbid(t *testing.T, s *Store, a, b string) {
	t.Helper()
	if err := s.LinkForbid(a, b); err != nil {
		t.Fatalf("LinkForbid(%s,%s) 意外失败: %v", a, b, err)
	}
}

func TestTransitiveClosureAndGroups(t *testing.T) {
	s := New()
	mustSame(t, s, "a", "b")
	mustSame(t, s, "b", "c")
	mustSame(t, s, "x", "y")

	if !s.Connected("a", "c") {
		t.Fatal("a 与 c 应传递同组")
	}
	if s.Connected("a", "x") {
		t.Fatal("a 与 x 不应同组")
	}
	groups := s.Groups()
	if len(groups) != 2 {
		t.Fatalf("期望 2 组, 得到 %v", groups)
	}
	if got := strings.Join(groups[0], ","); got != "a,b,c" {
		t.Fatalf("第一组应为 a,b,c, 得到 %s", got)
	}
}

func TestLinkSameRejectedWithPathEvidence(t *testing.T) {
	s := New()
	mustSame(t, s, "a", "b")
	mustSame(t, s, "b", "c")
	mustSame(t, s, "d", "e")
	mustForbid(t, s, "c", "d")

	err := s.LinkSame("a", "e")
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("期望 *ConflictError, 得到 %v", err)
	}
	if ce.Forbid != (Edge{"c", "d"}) {
		t.Fatalf("撞上的禁止边应为 c!~d, 得到 %+v", ce.Forbid)
	}
	// 证据: a 到 forbid 一端的路径, e 到另一端的路径。
	if got := strings.Join(ce.PathA, "~"); got != "a~b~c" {
		t.Fatalf("PathA 应为 a~b~c, 得到 %s", got)
	}
	if got := strings.Join(ce.PathB, "~"); got != "e~d" {
		t.Fatalf("PathB 应为 e~d, 得到 %s", got)
	}
	// 拒绝后图不变。
	if s.Connected("a", "e") {
		t.Fatal("被拒绝的关系不应生效")
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("拒绝后不变量应成立: %v", err)
	}
}

func TestLinkForbidRejectedWhenAlreadyGrouped(t *testing.T) {
	s := New()
	mustSame(t, s, "a", "b")
	mustSame(t, s, "b", "c")

	err := s.LinkForbid("a", "c")
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("期望 *ConflictError, 得到 %v", err)
	}
	if got := strings.Join(ce.SamePath, "~"); got != "a~b~c" {
		t.Fatalf("SamePath 应为 a~b~c, 得到 %s", got)
	}
}

func TestSelfForbidRejected(t *testing.T) {
	s := New()
	if err := s.LinkForbid("a", "a"); err == nil {
		t.Fatal("自身禁止关系应被拒绝")
	}
}

func TestUndoRollsBackAndReverifies(t *testing.T) {
	s := New()
	mustSame(t, s, "a", "b")
	mustForbid(t, s, "b", "c")
	mustSame(t, s, "c", "d")

	// 撤销 same c~d。
	if err := s.Undo(); err != nil {
		t.Fatalf("Undo 失败: %v", err)
	}
	if s.Connected("c", "d") {
		t.Fatal("撤销后 c 与 d 不应同组")
	}
	// 撤销 forbid b!~c, 之后 b~c 应可建立。
	if err := s.Undo(); err != nil {
		t.Fatalf("Undo 失败: %v", err)
	}
	mustSame(t, s, "b", "c")
	if !s.Connected("a", "c") {
		t.Fatal("a 应传递同组到 c")
	}
	// 撤销 same b~c 与 same a~b。
	if err := s.Undo(); err != nil {
		t.Fatalf("Undo 失败: %v", err)
	}
	if err := s.Undo(); err != nil {
		t.Fatalf("Undo 失败: %v", err)
	}
	if len(s.Groups()) != 0 {
		t.Fatalf("全部撤销后应无分组, 得到 %v", s.Groups())
	}
	if err := s.Undo(); err != ErrNothingToUndo {
		t.Fatalf("空日志应返回 ErrNothingToUndo, 得到 %v", err)
	}
}

func TestUnlinkSameSplitsComponent(t *testing.T) {
	s := New()
	mustSame(t, s, "a", "b")
	mustSame(t, s, "b", "c")
	if err := s.UnlinkSame("a", "b"); err != nil {
		t.Fatalf("UnlinkSame 失败: %v", err)
	}
	if s.Connected("a", "c") {
		t.Fatal("移除 a~b 后 a 与 c 不应连通")
	}
	if !s.Connected("b", "c") {
		t.Fatal("b 与 c 应仍同组")
	}
	// 撤销 UnlinkSame 应恢复连通。
	if err := s.Undo(); err != nil {
		t.Fatalf("Undo 失败: %v", err)
	}
	if !s.Connected("a", "c") {
		t.Fatal("回滚后 a 与 c 应恢复连通")
	}
}

func TestSuggestionsAreSoft(t *testing.T) {
	s := New()
	s.Suggest("a", "b", 0.92, "embedding 相似")
	s.Suggest("c", "d", 0.81, "名称相近")

	if s.Connected("a", "b") {
		t.Fatal("软建议不应形成硬关系")
	}
	if len(s.Groups()) != 0 {
		t.Fatal("软建议不应产生分组")
	}
	sugs := s.Suggestions()
	if len(sugs) != 2 || sugs[0].Score != 0.92 {
		t.Fatalf("建议记录异常: %+v", sugs)
	}
	// 软建议不产生事务, Undo 应无可撤销。
	if err := s.Undo(); err != ErrNothingToUndo {
		t.Fatalf("软建议不应入事务日志, 得到 %v", err)
	}
}

func TestIdempotentLinks(t *testing.T) {
	s := New()
	mustSame(t, s, "a", "b")
	mustSame(t, s, "a", "b") // 重复
	mustSame(t, s, "b", "a") // 反向重复
	mustForbid(t, s, "x", "y")
	mustForbid(t, s, "y", "x") // 反向重复
	if got := len(s.Groups()); got != 1 {
		t.Fatalf("期望 1 组, 得到 %d", got)
	}
	// 仅两条有效事务。
	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(); err != ErrNothingToUndo {
		t.Fatalf("期望空日志, 得到 %v", err)
	}
}
