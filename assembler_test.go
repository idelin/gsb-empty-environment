package assembler

import (
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func ev(trace, span, parent string, sec int) Event {
	return Event{TraceID: trace, SpanID: span, ParentID: parent, At: base.Add(time.Duration(sec) * time.Second)}
}

func issuesOfKind(asm *Assembler, kind IssueKind) []Issue {
	var out []Issue
	for _, is := range asm.Issues() {
		if is.Kind == kind {
			out = append(out, is)
		}
	}
	return out
}

func childIDs(s *Span) map[string]bool {
	ids := map[string]bool{}
	for _, c := range s.Children {
		ids[c.SpanID] = true
	}
	return ids
}

// 子先于父到达：父到达后孤儿正确归位。
func TestChildBeforeParent(t *testing.T) {
	asm := New(5 * time.Second)
	asm.Ingest(ev("t1", "child", "mid", 0))
	asm.Ingest(ev("t1", "mid", "root", 1))
	asm.Ingest(ev("t1", "root", "", 2))

	roots := asm.Roots("t1")
	if len(roots) != 1 || roots[0].SpanID != "root" {
		t.Fatalf("expected single root, got %+v", roots)
	}
	mid := roots[0].Children
	if len(mid) != 1 || mid[0].SpanID != "mid" {
		t.Fatalf("mid not attached under root: %+v", mid)
	}
	if got := childIDs(mid[0]); !got["child"] {
		t.Fatalf("child not reattached under mid: %v", got)
	}
	if len(asm.Orphans("t1")) != 0 {
		t.Fatalf("orphans should be empty, got %+v", asm.Orphans("t1"))
	}
	if len(asm.Issues()) != 0 {
		t.Fatalf("no issues expected, got %+v", asm.Issues())
	}
}

// 根晚到：整棵子树在根到达后归位。
func TestLateRootReattach(t *testing.T) {
	asm := New(5 * time.Second)
	asm.Ingest(ev("t1", "grandchild", "child", 0))
	asm.Ingest(ev("t1", "child", "root", 1))
	// grandchild 已挂到已知节点 child 下，只有 child 自己还在等 root。
	if n := len(asm.Orphans("t1")); n != 1 {
		t.Fatalf("expected 1 orphan before root, got %d", n)
	}
	asm.Ingest(ev("t1", "root", "", 2))

	roots := asm.Roots("t1")
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	child := roots[0].Children
	if len(child) != 1 || len(child[0].Children) != 1 || child[0].Children[0].SpanID != "grandchild" {
		t.Fatalf("subtree not reattached correctly: %+v", child)
	}
	if len(asm.Orphans("t1")) != 0 || len(asm.Unresolved("t1")) != 0 {
		t.Fatalf("nothing should remain pending")
	}
}

// 重复 span 同父：幂等更新，不产生问题、不重复挂接。
func TestDuplicateSameParentIdempotent(t *testing.T) {
	asm := New(5 * time.Second)
	asm.Ingest(ev("t1", "root", "", 0))
	e := ev("t1", "a", "root", 1)
	e.Name = "v1"
	asm.Ingest(e)
	e2 := ev("t1", "a", "root", 2)
	e2.Name = "v2"
	asm.Ingest(e2)

	root := asm.Roots("t1")[0]
	if len(root.Children) != 1 {
		t.Fatalf("duplicate attach: %+v", root.Children)
	}
	if root.Children[0].Name != "v2" {
		t.Fatalf("idempotent update should refresh name, got %q", root.Children[0].Name)
	}
	if len(asm.Issues()) != 0 {
		t.Fatalf("no issues expected, got %+v", asm.Issues())
	}
}

// 重复 span 不同父：报 parent_conflict，保留首次归属。
func TestDuplicateDifferentParentConflict(t *testing.T) {
	asm := New(5 * time.Second)
	asm.Ingest(ev("t1", "root", "", 0))
	asm.Ingest(ev("t1", "other", "", 1))
	asm.Ingest(ev("t1", "a", "root", 2))
	asm.Ingest(ev("t1", "a", "other", 3))

	conflicts := issuesOfKind(asm, IssueParentConflict)
	if len(conflicts) != 1 || conflicts[0].SpanID != "a" {
		t.Fatalf("expected 1 parent_conflict for a, got %+v", conflicts)
	}
	a := asm.Lookup("t1", "a")
	if a.ParentID != "root" || a.Parent().SpanID != "root" {
		t.Fatalf("original parent must be kept, got %+v", a)
	}
	if got := childIDs(asm.Lookup("t1", "other")); got["a"] {
		t.Fatalf("a must not be attached to other")
	}
}

// 结构循环：A↔B 互相为父，报 structural_cycle 且不挂接成环。
func TestStructuralCycle(t *testing.T) {
	asm := New(5 * time.Second)
	asm.Ingest(ev("t1", "a", "b", 0))
	asm.Ingest(ev("t1", "b", "a", 1))

	cycles := issuesOfKind(asm, IssueCycle)
	if len(cycles) != 1 {
		t.Fatalf("expected 1 structural_cycle, got %+v", cycles)
	}
	a, b := asm.Lookup("t1", "a"), asm.Lookup("t1", "b")
	if a.Parent() == b && b.Parent() == a {
		t.Fatalf("cycle must not be linked")
	}
	// 成环被拒绝的一方进入 unresolved，而不是孤儿或根。
	if len(asm.Unresolved("t1")) != 1 {
		t.Fatalf("expected 1 unresolved, got %+v", asm.Unresolved("t1"))
	}
	if len(asm.Roots("t1")) != 0 {
		t.Fatalf("cyclic spans must not become roots")
	}
}

// 跨 trace 错误引用：父 ID 属于其他 trace，报 cross_trace_reference。
func TestCrossTraceReference(t *testing.T) {
	asm := New(5 * time.Second)
	asm.Ingest(ev("t1", "root", "", 0))
	asm.Ingest(ev("t2", "spy", "root", 1)) // root 属于 t1

	x := issuesOfKind(asm, IssueCrossTrace)
	if len(x) != 1 || x[0].TraceID != "t2" || x[0].SpanID != "spy" {
		t.Fatalf("expected cross_trace_reference for t2/spy, got %+v", x)
	}
	if got := childIDs(asm.Lookup("t1", "root")); got["spy"] {
		t.Fatalf("spy must not be attached across traces")
	}
	if len(asm.Roots("t2")) != 0 {
		t.Fatalf("spy must not be promoted to root")
	}
}

// 缺父超时：孤儿进入 unresolved，绝不挂到任意根下。
func TestMissingParentTimeout(t *testing.T) {
	asm := New(5 * time.Second)
	asm.Ingest(ev("t1", "root", "", 0))
	asm.Ingest(ev("t1", "lost", "ghost", 1))
	asm.Ingest(ev("t1", "lost-child", "lost", 2)) // 挂在已知孤儿 lost 下

	asm.AdvanceTo(base.Add(10 * time.Second))

	missing := issuesOfKind(asm, IssueMissingParent)
	if len(missing) != 1 || missing[0].SpanID != "lost" {
		t.Fatalf("expected missing_parent_timeout for lost, got %+v", missing)
	}
	un := asm.Unresolved("t1")
	if len(un) != 1 || un[0].SpanID != "lost" {
		t.Fatalf("lost should be unresolved, got %+v", un)
	}
	// 缺父的 span 及其子树不得挂到 root 或任何其他根下。
	root := asm.Roots("t1")[0]
	if len(root.Children) != 0 {
		t.Fatalf("orphan subtree must not be attached to arbitrary root: %+v", root.Children)
	}
	if len(asm.Roots("t1")) != 1 {
		t.Fatalf("lost must not become a root itself")
	}
	// lost-child 仍挂在 lost 下，随 lost 一起与森林隔离。
	if got := childIDs(un[0]); !got["lost-child"] {
		t.Fatalf("lost-child should stay under lost: %v", got)
	}
}

// 超时前到达的父不算缺父：TTL 内归位不报问题。
func TestParentArrivesWithinTTL(t *testing.T) {
	asm := New(5 * time.Second)
	asm.Ingest(ev("t1", "child", "root", 0))
	asm.Ingest(ev("t1", "root", "", 4)) // 4s < 5s TTL
	asm.AdvanceTo(base.Add(30 * time.Second))

	if len(asm.Issues()) != 0 {
		t.Fatalf("no issues expected, got %+v", asm.Issues())
	}
	if got := childIDs(asm.Roots("t1")[0]); !got["child"] {
		t.Fatalf("child should be attached to root")
	}
}
