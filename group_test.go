package entitygroup

import (
	"strings"
	"testing"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
}

func mustConflict(t *testing.T, err error) *ConflictError {
	t.Helper()
	if err == nil {
		t.Fatal("期望冲突错误，得到 nil")
	}
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("期望 *ConflictError，得到 %T: %v", err, err)
	}
	return ce
}

func TestTransitiveClosureGroups(t *testing.T) {
	s := New()
	mustOK(t, s.AddMustTogether("a", "b"))
	mustOK(t, s.AddMustTogether("b", "c"))
	mustOK(t, s.AddMustTogether("x", "y"))

	ok, p := s.Connected("a", "c")
	if !ok {
		t.Fatal("a 与 c 应经传递闭包同组")
	}
	if strings.Join(p, "") != "abc" {
		t.Fatalf("路径证据应为 a-b-c，得到 %v", p)
	}
	if ok, _ := s.Connected("a", "x"); ok {
		t.Fatal("a 与 x 不应同组")
	}
	groups := s.Groups()
	if len(groups) != 2 {
		t.Fatalf("应有 2 个分组，得到 %v", groups)
	}
}

func TestMustNotTogetherRejectedWithPathEvidence(t *testing.T) {
	s := New()
	mustOK(t, s.AddMustTogether("a", "b"))
	mustOK(t, s.AddMustTogether("b", "c"))

	ce := mustConflict(t, s.AddMustNotTogether("a", "c"))
	if ce.Kind != MustNotTogether {
		t.Fatalf("冲突类型错误: %v", ce.Kind)
	}
	if strings.Join(ce.Path, "") != "abc" {
		t.Fatalf("路径证据应为 a-b-c，得到 %v", ce.Path)
	}
	if s.Forbidden("a", "c") {
		t.Fatal("被拒绝的关系不应写入 Store")
	}
}

func TestMustTogetherRejectedAgainstForbiddenEdge(t *testing.T) {
	s := New()
	mustOK(t, s.AddMustNotTogether("x", "y"))
	mustOK(t, s.AddMustTogether("x", "a"))
	mustOK(t, s.AddMustTogether("y", "b"))

	// 合并 {x,a} 与 {y,b} 会让禁止边 x-y 两端同组，必须拒绝。
	ce := mustConflict(t, s.AddMustTogether("a", "b"))
	if ce.Kind != MustTogether {
		t.Fatalf("冲突类型错误: %v", ce.Kind)
	}
	if ce.ForbiddenEdge != [2]string{"x", "y"} && ce.ForbiddenEdge != [2]string{"y", "x"} {
		t.Fatalf("禁止边证据错误: %v", ce.ForbiddenEdge)
	}
	// 路径证据应连接禁止边两端，且经过拟新增的 a-b。
	joined := strings.Join(ce.Path, ",")
	if ce.Path[0] != "x" || ce.Path[len(ce.Path)-1] != "y" {
		t.Fatalf("路径应连接 x 与 y，得到 %v", ce.Path)
	}
	if !strings.Contains(joined, "a,b") {
		t.Fatalf("路径应经过拟新增边 a-b，得到 %v", ce.Path)
	}
	if ok, _ := s.Connected("a", "b"); ok {
		t.Fatal("被拒绝的关系不应写入 Store")
	}
}

func TestSelfMustNotTogetherRejected(t *testing.T) {
	s := New()
	if ce := mustConflict(t, s.AddMustNotTogether("a", "a")); len(ce.Path) != 1 || ce.Path[0] != "a" {
		t.Fatalf("自环证据错误: %v", ce.Path)
	}
}

func TestDuplicateRelationsAreNoop(t *testing.T) {
	s := New()
	mustOK(t, s.AddMustTogether("a", "b"))
	mustOK(t, s.AddMustTogether("a", "b"))
	mustOK(t, s.AddMustTogether("b", "a"))
	if s.PendingTx() != 1 {
		t.Fatalf("重复关系不应记入事务日志，得到 %d 条", s.PendingTx())
	}
}

func TestSuggestionNeverFormsHardRelation(t *testing.T) {
	s := New()
	s.SuggestSimilar("a", "b", 0.97)
	s.SuggestSimilar("a", "c", 0.88)

	if ok, _ := s.Connected("a", "b"); ok {
		t.Fatal("相似度建议不得自动形成硬关系")
	}
	if s.Forbidden("a", "b") {
		t.Fatal("相似度建议不得形成禁止关系")
	}
	if len(s.Groups()) != 0 {
		t.Fatal("不应产生任何分组")
	}
	if got := s.Suggestions(); len(got) != 2 || got[0].Score != 0.97 {
		t.Fatalf("建议列表错误: %v", got)
	}
	// 建议与禁止关系互不干扰。
	mustOK(t, s.AddMustNotTogether("a", "b"))
	if !s.Forbidden("a", "b") {
		t.Fatal("显式禁止关系应正常生效")
	}
}

func TestUndoRollsBackLastTransaction(t *testing.T) {
	s := New()
	mustOK(t, s.AddMustTogether("a", "b"))
	mustOK(t, s.AddMustTogether("b", "c"))
	mustOK(t, s.AddMustNotTogether("a", "d"))

	desc, ok := s.Undo()
	if !ok || desc != "must-not-together(a, d)" {
		t.Fatalf("撤销描述错误: %q, %v", desc, ok)
	}
	if s.Forbidden("a", "d") {
		t.Fatal("撤销后禁止关系应消失")
	}
	if _, ok := s.Undo(); !ok {
		t.Fatal("应还能继续撤销")
	}
	if ok, _ := s.Connected("a", "c"); ok {
		t.Fatal("撤销 b-c 后 a 与 c 不应再连通")
	}
	if ok, _ := s.Connected("a", "b"); !ok {
		t.Fatal("a-b 关系应保留")
	}
	if _, ok := s.Undo(); !ok {
		t.Fatal("应能撤销最后一条")
	}
	if _, ok := s.Undo(); ok {
		t.Fatal("空日志上撤销应返回 false")
	}
	if len(s.Groups()) != 0 {
		t.Fatal("全部撤销后不应有分组")
	}
}

func TestUndoRestoresConflictFreedom(t *testing.T) {
	s := New()
	mustOK(t, s.AddMustTogether("a", "b"))
	mustOK(t, s.AddMustNotTogether("a", "c"))
	// 当前 b 与 c 禁止同组（a-b 同组且 a-c 禁止）。
	mustConflict(t, s.AddMustTogether("b", "c"))
	// 撤销禁止关系后，b-c 应可合并。
	if _, ok := s.Undo(); !ok {
		t.Fatal("撤销失败")
	}
	mustOK(t, s.AddMustTogether("b", "c"))
	if ok, _ := s.Connected("a", "c"); !ok {
		t.Fatal("回滚后重试应成功并形成闭包")
	}
}

func TestRejectedRelationNotLogged(t *testing.T) {
	s := New()
	mustOK(t, s.AddMustTogether("a", "b"))
	mustConflict(t, s.AddMustNotTogether("a", "b"))
	if s.PendingTx() != 1 {
		t.Fatalf("被拒绝的关系不应进入事务日志，得到 %d 条", s.PendingTx())
	}
}
