package reorder

import (
	"reflect"
	"strings"
	"testing"
)

// doc 以节点序列模型构造文档：每个 id 的内容默认为 "c-<id>"。
func doc(ids ...string) []Paragraph {
	d := make([]Paragraph, len(ids))
	for i, id := range ids {
		d[i] = Paragraph{ID: id, Content: "c-" + id}
	}
	return d
}

// seq 把结果序列压成 "id:content" 列表，便于与期望的节点序列核对。
func seq(d []Paragraph) []string {
	s := make([]string, len(d))
	for i, p := range d {
		s[i] = p.ID + ":" + p.Content
	}
	return s
}

func ids(s ...string) []string {
	out := make([]string, len(s))
	for i, id := range s {
		out[i] = id + ":c-" + id
	}
	return out
}

func run(t *testing.T, d []Paragraph, b Batch) []Paragraph {
	t.Helper()
	out, err := Apply(d, b)
	if err != nil {
		t.Fatalf("Apply(%v) unexpected error: %v", b, err)
	}
	return out
}

func runErr(t *testing.T, d []Paragraph, b Batch) string {
	t.Helper()
	_, err := Apply(d, b)
	if err == nil {
		t.Fatalf("Apply(%v) expected error, got nil", b)
	}
	return err.Error()
}

func TestMoveAfterSurvivingPredecessor(t *testing.T) {
	got := run(t, doc("a", "b", "c", "d"), Batch{
		Moves: []Move{{ID: "d", Predecessor: "a"}},
	})
	if want := ids("a", "d", "b", "c"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestMoveToFront(t *testing.T) {
	got := run(t, doc("a", "b", "c"), Batch{
		Moves: []Move{{ID: "c", Predecessor: ""}},
	})
	if want := ids("c", "a", "b"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestPredecessorDeletedFallsBackAlongOldOrder(t *testing.T) {
	// 旧序 a b c d：删 b，d 指定移到 b 后；b 已删，沿旧序回退到 a。
	got := run(t, doc("a", "b", "c", "d"), Batch{
		Deletes: []string{"b"},
		Moves:   []Move{{ID: "d", Predecessor: "b"}},
	})
	if want := ids("a", "d", "c"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestFallbackSkipsChainOfDeleted(t *testing.T) {
	// 旧序 a b c d e：删 b、c，e 指定移到 c 后；c、b 均删，回退到 a。
	got := run(t, doc("a", "b", "c", "d", "e"), Batch{
		Deletes: []string{"b", "c"},
		Moves:   []Move{{ID: "e", Predecessor: "c"}},
	})
	if want := ids("a", "e", "d"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestFallbackToFrontWhenNoSurvivor(t *testing.T) {
	// 旧序 a b c：删 a、b，c 指定移到 b 后；无存活前驱，移到最前。
	got := run(t, doc("a", "b", "c"), Batch{
		Deletes: []string{"a", "b"},
		Moves:   []Move{{ID: "c", Predecessor: "b"}},
	})
	if want := ids("c"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestFallbackSkipsSelf(t *testing.T) {
	// 旧序 x a b：删 b，a 指定移到 b 后；回退时跳过自身 a，锚定 x。
	got := run(t, doc("x", "a", "b"), Batch{
		Deletes: []string{"b"},
		Moves:   []Move{{ID: "a", Predecessor: "b"}},
	})
	if want := ids("x", "a"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestAnchorChainThroughMovedNode(t *testing.T) {
	// 旧序 a b c d：b 移到 d 后，d 移到 a 后；锚点 d 自身被移动，b 跟随 d。
	got := run(t, doc("a", "b", "c", "d"), Batch{
		Moves: []Move{{ID: "b", Predecessor: "d"}, {ID: "d", Predecessor: "a"}},
	})
	if want := ids("a", "d", "b", "c"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestFallbackAnchorMayItselfBeMoved(t *testing.T) {
	// 旧序 a b c：删 b，c 移到 b 后（回退锚定 a），a 移到最前；c 跟随 a。
	got := run(t, doc("a", "b", "c"), Batch{
		Deletes: []string{"b"},
		Moves:   []Move{{ID: "c", Predecessor: "b"}, {ID: "a", Predecessor: ""}},
	})
	if want := ids("a", "c"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestSameAnchorKeepsOldRelativeOrder(t *testing.T) {
	// c、d 都移到 a 后，保持旧相对顺序 c 先于 d。
	got := run(t, doc("a", "b", "c", "d"), Batch{
		Moves: []Move{{ID: "d", Predecessor: "a"}, {ID: "c", Predecessor: "a"}},
	})
	if want := ids("a", "c", "d", "b"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestCombinedDeleteMoveModify(t *testing.T) {
	// 同批：删 b；e 移到 b 后（回退锚定 a）；修改 c、e 内容。
	got := run(t, doc("a", "b", "c", "d", "e"), Batch{
		Deletes:  []string{"b"},
		Moves:    []Move{{ID: "e", Predecessor: "b"}},
		Modifies: map[string]string{"c": "C2", "e": "E2"},
	})
	want := []string{"a:c-a", "e:E2", "c:C2", "d:c-d"}
	if !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestModifyWithoutMoves(t *testing.T) {
	got := run(t, doc("a", "b"), Batch{Modifies: map[string]string{"b": "B2"}})
	want := []string{"a:c-a", "b:B2"}
	if !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}

func TestCircularMoveTwoNodes(t *testing.T) {
	msg := runErr(t, doc("a", "b", "c"), Batch{
		Moves: []Move{{ID: "a", Predecessor: "b"}, {ID: "b", Predecessor: "a"}},
	})
	if !strings.Contains(msg, "circular") {
		t.Fatalf("expected circular error, got %q", msg)
	}
}

func TestCircularMoveThreeNodes(t *testing.T) {
	msg := runErr(t, doc("a", "b", "c", "d"), Batch{
		Moves: []Move{
			{ID: "a", Predecessor: "b"},
			{ID: "b", Predecessor: "c"},
			{ID: "c", Predecessor: "a"},
		},
	})
	if !strings.Contains(msg, "circular") {
		t.Fatalf("expected circular error, got %q", msg)
	}
}

func TestSelfMoveRejected(t *testing.T) {
	runErr(t, doc("a", "b"), Batch{Moves: []Move{{ID: "a", Predecessor: "a"}}})
}

func TestModifyDeletedRejected(t *testing.T) {
	runErr(t, doc("a", "b"), Batch{
		Deletes:  []string{"b"},
		Modifies: map[string]string{"b": "B2"},
	})
}

func TestMoveDeletedRejected(t *testing.T) {
	runErr(t, doc("a", "b"), Batch{
		Deletes: []string{"b"},
		Moves:   []Move{{ID: "b", Predecessor: "a"}},
	})
}

func TestUnknownIDsRejected(t *testing.T) {
	runErr(t, doc("a"), Batch{Deletes: []string{"zz"}})
	runErr(t, doc("a"), Batch{Moves: []Move{{ID: "zz", Predecessor: "a"}}})
	runErr(t, doc("a"), Batch{Moves: []Move{{ID: "a", Predecessor: "zz"}}})
	runErr(t, doc("a"), Batch{Modifies: map[string]string{"zz": "x"}})
}

func TestDuplicateMoveRejected(t *testing.T) {
	runErr(t, doc("a", "b", "c"), Batch{
		Moves: []Move{{ID: "c", Predecessor: "a"}, {ID: "c", Predecessor: "b"}},
	})
}

func TestEmptyBatchKeepsSequence(t *testing.T) {
	got := run(t, doc("a", "b", "c"), Batch{})
	if want := ids("a", "b", "c"); !reflect.DeepEqual(seq(got), want) {
		t.Fatalf("got %v, want %v", seq(got), want)
	}
}
