package netsec

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// 多名字指向同一对象（硬链接）：核验应报告跨域共享及两侧最短路径，
// 断开一条链接后只更新该对象的状态。
func TestHardlinkMultipleNamesAndIncrementalUnlink(t *testing.T) {
	objA := ObjectID{Number: 1, Generation: 1}
	objB := ObjectID{Number: 2, Generation: 1}

	nodeA1 := &Node{ID: "a1", Object: objA}
	nodeA2 := &Node{ID: "a2", Object: objA}
	nodeA3 := &Node{ID: "a3", Object: objA}
	nodeB := &Node{ID: "b", Object: objB}
	dir1 := &Node{ID: "dir1", IsDir: true, Object: ObjectID{Number: 101, Generation: 1}, Entries: []Entry{
		{Name: "linkA1", ChildID: "a1"},
	}}
	hostDir := &Node{ID: "hostdir", IsDir: true, Object: ObjectID{Number: 102, Generation: 1}, Entries: []Entry{
		{Name: "hostlinkA", ChildID: "a3"},
		{Name: "hostlinkB", ChildID: "b"},
	}}
	root := &Node{ID: "root", IsDir: true, Object: ObjectID{Number: 100, Generation: 1}, Entries: []Entry{
		{Name: "dir1", ChildID: "dir1"},
		{Name: "linkA2", ChildID: "a2"},
		{Name: "fileB", ChildID: "b"},
	}}

	tree := &Tree{
		RootID: "root",
		Nodes: map[string]*Node{
			"root": root, "dir1": dir1, "hostdir": hostDir,
			"a1": nodeA1, "a2": nodeA2, "a3": nodeA3, "b": nodeB,
		},
		OutsideRefs: []OutsideRef{{Name: "host", ChildID: "hostdir"}},
	}

	checker, err := NewChecker(tree, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	reports := checker.Reports()
	want := []Report{
		{
			Object:      objA,
			InsidePath:  []string{"linkA2"}, // 比 dir1/linkA1 更短。
			OutsidePath: []string{"host", "hostlinkA"},
		},
		{
			Object:      objB,
			InsidePath:  []string{"fileB"},
			OutsidePath: []string{"host", "hostlinkB"},
		},
	}
	if !reflect.DeepEqual(reports, want) {
		t.Fatalf("Reports = %#v, want %#v", reports, want)
	}

	// 记录对象 B 的状态，断开 A 的链接后它必须原样保留。
	beforeB := checker.states[objB]

	if err := checker.Unlink("root", "linkA2"); err != nil {
		t.Fatalf("Unlink linkA2: %v", err)
	}
	reports = checker.Reports()
	want[0].InsidePath = []string{"dir1", "linkA1"}
	if !reflect.DeepEqual(reports, want) {
		t.Fatalf("after first Unlink, Reports = %#v, want %#v", reports, want)
	}
	if got := checker.states[objB]; got != beforeB {
		t.Fatalf("object B state was rebuilt although its links were untouched")
	}

	// 断开 A 在隔离区内最后一条链接：A 不再跨域共享，B 的报告保持不变。
	if err := checker.Unlink("dir1", "linkA1"); err != nil {
		t.Fatalf("Unlink linkA1: %v", err)
	}
	reports = checker.Reports()
	if len(reports) != 1 || reports[0].Object != objB {
		t.Fatalf("after removing all inside links of A, Reports = %#v", reports)
	}
	// A 在宿主侧仍有锚点引用，因此保留状态但内侧路径已清空。
	stA := checker.states[objA]
	if stA == nil {
		t.Fatal("object A state should be retained because the outside anchor still references it")
	}
	if stA.inside != nil {
		t.Fatalf("object A inside path = %v, want nil after all inside links are gone", stA.inside)
	}
	if !slices.Equal(stA.outside, []string{"host", "hostlinkA"}) {
		t.Fatalf("object A outside path = %v, want [host hostlinkA]", stA.outside)
	}

	// 错误路径。
	if err := checker.Unlink("missing", "x"); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Unlink unknown parent = %v, want ErrUnknownNode", err)
	}
	if err := checker.Unlink("b", "x"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("Unlink on file = %v, want ErrNotDirectory", err)
	}
	if err := checker.Unlink("root", "nope"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatalf("Unlink missing entry = %v, want ErrEntryNotFound", err)
	}
	if err := checker.Unlink("root", "dir1"); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("Unlink directory entry = %v, want ErrNotDirectory", err)
	}
}

// 允许共享集合中的对象不出现在报告中。
func TestAllowedObjectsFiltered(t *testing.T) {
	objA := ObjectID{Number: 1, Generation: 1}
	objB := ObjectID{Number: 2, Generation: 1}
	tree := sharedTwoObjectTree(objA, objB)

	checker, err := NewChecker(tree, map[ObjectID]struct{}{objB: {}})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	reports := checker.Reports()
	if len(reports) != 1 || reports[0].Object != objA {
		t.Fatalf("Reports = %#v, want only objA", reports)
	}
}

// 断开对象在两侧的全部链接后，其缓存状态应被清理。
func TestUnlinkRemovesStateWhenObjectFullyUnreferenced(t *testing.T) {
	obj := ObjectID{Number: 3, Generation: 2}
	root := &Node{ID: "root", IsDir: true, Object: ObjectID{Number: 100, Generation: 1}, Entries: []Entry{
		{Name: "inside", ChildID: "n"},
	}}
	tree := &Tree{
		RootID: "root",
		Nodes: map[string]*Node{
			"root": root,
			"n":    {ID: "n", Object: obj},
		},
		OutsideRefs: []OutsideRef{{Name: "host", ChildID: "n"}},
	}

	checker, err := NewChecker(tree, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	if len(checker.Reports()) != 1 {
		t.Fatalf("Reports = %#v, want one cross-domain report", checker.Reports())
	}

	// 先移除宿主侧锚点：对象只剩内侧可达，不再违规但状态保留。
	tree.OutsideRefs = nil
	checker.refresh(obj)
	if len(checker.Reports()) != 0 {
		t.Fatalf("Reports = %#v, want none after outside anchor removed", checker.Reports())
	}
	if _, ok := checker.states[obj]; !ok {
		t.Fatal("object state should remain while still reachable from inside")
	}

	// 再断开内侧最后一条链接：对象彻底失去引用，状态被清理。
	if err := checker.Unlink("root", "inside"); err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if _, ok := checker.states[obj]; ok {
		t.Fatal("fully unreferenced object state should be removed")
	}
}

// 目录环：后代目录项回指祖先，BFS 必须终止且仍能找到环内对象。
func TestDirectoryCycle(t *testing.T) {
	objF := ObjectID{Number: 7, Generation: 3}
	nodeF := &Node{ID: "f", Object: objF}
	dirLoop := &Node{ID: "loop", IsDir: true, Object: ObjectID{Number: 201, Generation: 1}}
	root := &Node{ID: "root", IsDir: true, Object: ObjectID{Number: 200, Generation: 1}, Entries: []Entry{
		{Name: "loop", ChildID: "loop"},
	}}
	// 回指根形成环，并在环目录中挂一个文件。
	dirLoop.Entries = []Entry{
		{Name: "back", ChildID: "root"},
		{Name: "f", ChildID: "f"},
	}

	tree := &Tree{
		RootID: "root",
		Nodes: map[string]*Node{
			"root": root, "loop": dirLoop, "f": nodeF,
		},
		OutsideRefs: []OutsideRef{{Name: "host", ChildID: "loop"}},
	}

	checker, err := NewChecker(tree, nil)
	if err != nil {
		t.Fatalf("NewChecker with cycle: %v", err)
	}
	reports := checker.Reports()
	// 环目录对象本身也从两侧可达，必须一并报告；文件对象的路径最短。
	if len(reports) != 2 {
		t.Fatalf("Reports = %#v, want two reports (loop dir and file)", reports)
	}
	var fileReport *Report
	for i := range reports {
		if reports[i].Object == objF {
			fileReport = &reports[i]
		}
	}
	if fileReport == nil {
		t.Fatalf("object %v missing from reports %#v", objF, reports)
	}
	if !slices.Equal(fileReport.InsidePath, []string{"loop", "f"}) {
		t.Fatalf("InsidePath = %v, want [loop f]", fileReport.InsidePath)
	}
	if !slices.Equal(fileReport.OutsidePath, []string{"host", "f"}) {
		t.Fatalf("OutsidePath = %v, want [host f]", fileReport.OutsidePath)
	}

	// 环目录报告：内侧经过 root/loop，外侧锚点直接命中。
	loopObj := ObjectID{Number: 201, Generation: 1}
	var loopReport *Report
	for i := range reports {
		if reports[i].Object == loopObj {
			loopReport = &reports[i]
		}
	}
	if loopReport == nil {
		t.Fatalf("loop directory object %v missing from reports", loopObj)
	}
	if !slices.Equal(loopReport.InsidePath, []string{"loop"}) ||
		!slices.Equal(loopReport.OutsidePath, []string{"host"}) {
		t.Fatalf("loop paths unexpected: %#v", loopReport)
	}
}

// 对象号代次复用：同号不同代必须被视为不同对象，
// 只有两侧都可达的那一代构成跨域共享。
func TestObjectNumberGenerationReuse(t *testing.T) {
	oldGen := ObjectID{Number: 5, Generation: 1}
	newGen := ObjectID{Number: 5, Generation: 2}

	oldNode := &Node{ID: "old", Object: oldGen} // 仅隔离区可达。
	newNode := &Node{ID: "new", Object: newGen} // 两侧都可达。
	root := &Node{ID: "root", IsDir: true, Object: ObjectID{Number: 300, Generation: 1}, Entries: []Entry{
		{Name: "old", ChildID: "old"},
		{Name: "new", ChildID: "new"},
	}}
	tree := &Tree{
		RootID: "root",
		Nodes: map[string]*Node{
			"root": root, "old": oldNode, "new": newNode,
		},
		OutsideRefs: []OutsideRef{{Name: "host", ChildID: "new"}},
	}

	checker, err := NewChecker(tree, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	reports := checker.Reports()
	if len(reports) != 1 {
		t.Fatalf("Reports = %#v, want exactly one shared object", reports)
	}
	if reports[0].Object != newGen {
		t.Fatalf("reported object = %v, want generation 2", reports[0].Object)
	}
	if !slices.Equal(reports[0].InsidePath, []string{"new"}) ||
		!slices.Equal(reports[0].OutsidePath, []string{"host"}) {
		t.Fatalf("unexpected paths: %#v", reports[0])
	}

	// 两个代次在状态表中必须各自独立。
	if _, ok := checker.states[oldGen]; !ok {
		t.Fatal("old generation object missing from state table")
	}
	if checker.states[oldGen].outside != nil {
		t.Fatal("old generation must not be reachable from outside")
	}
}

// 等长最短路径按路径段字典序确定，保证结果稳定可复现。
func TestShortestPathTieBreak(t *testing.T) {
	obj := ObjectID{Number: 9, Generation: 1}
	root := &Node{ID: "root", IsDir: true, Object: ObjectID{Number: 400, Generation: 1}, Entries: []Entry{
		{Name: "bbb", ChildID: "n1"},
		{Name: "aaa", ChildID: "n2"},
	}}
	tree := &Tree{
		RootID: "root",
		Nodes: map[string]*Node{
			"root": root,
			"n1":   {ID: "n1", Object: obj},
			"n2":   {ID: "n2", Object: obj},
		},
		OutsideRefs: []OutsideRef{
			{Name: "zeta", ChildID: "n1"},
			{Name: "alpha", ChildID: "n2"},
		},
	}

	checker, err := NewChecker(tree, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	reports := checker.Reports()
	if len(reports) != 1 {
		t.Fatalf("Reports = %#v", reports)
	}
	if !slices.Equal(reports[0].InsidePath, []string{"aaa"}) {
		t.Fatalf("InsidePath = %v, want [aaa]", reports[0].InsidePath)
	}
	if !slices.Equal(reports[0].OutsidePath, []string{"alpha"}) {
		t.Fatalf("OutsidePath = %v, want [alpha]", reports[0].OutsidePath)
	}
}

func TestNewCheckerValidation(t *testing.T) {
	obj := ObjectID{Number: 1, Generation: 1}
	good := &Tree{
		RootID: "root",
		Nodes: map[string]*Node{
			"root": {ID: "root", IsDir: true, Object: ObjectID{Number: 100, Generation: 1}},
			"f":    {ID: "f", Object: obj},
		},
	}

	if _, err := NewChecker(nil, nil); !errors.Is(err, ErrEmptyTree) {
		t.Fatalf("nil tree = %v, want ErrEmptyTree", err)
	}

	missingRoot := *good
	missingRoot.RootID = "ghost"
	if _, err := NewChecker(&missingRoot, nil); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("unknown root = %v, want ErrUnknownNode", err)
	}

	dangling := *good
	dangling.Nodes = cloneNodes(good.Nodes)
	dangling.Nodes["root"].Entries = []Entry{{Name: "x", ChildID: "gone"}}
	if _, err := NewChecker(&dangling, nil); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("dangling child = %v, want ErrUnknownNode", err)
	}

	badOutside := *good
	badOutside.OutsideRefs = []OutsideRef{{Name: "h", ChildID: "gone"}}
	if _, err := NewChecker(&badOutside, nil); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("dangling outside ref = %v, want ErrUnknownNode", err)
	}

	emptyAnchor := *good
	emptyAnchor.OutsideRefs = []OutsideRef{{Name: "", ChildID: "f"}}
	if _, err := NewChecker(&emptyAnchor, nil); err == nil {
		t.Fatal("empty outside anchor name should be rejected")
	}
}

func sharedTwoObjectTree(objA, objB ObjectID) *Tree {
	root := &Node{ID: "root", IsDir: true, Object: ObjectID{Number: 100, Generation: 1}, Entries: []Entry{
		{Name: "a", ChildID: "na"},
		{Name: "b", ChildID: "nb"},
	}}
	return &Tree{
		RootID: "root",
		Nodes: map[string]*Node{
			"root": root,
			"na":   {ID: "na", Object: objA},
			"nb":   {ID: "nb", Object: objB},
		},
		OutsideRefs: []OutsideRef{
			{Name: "hostA", ChildID: "na"},
			{Name: "hostB", ChildID: "nb"},
		},
	}
}

func cloneNodes(in map[string]*Node) map[string]*Node {
	out := make(map[string]*Node, len(in))
	for id, node := range in {
		cp := *node
		cp.Entries = append([]Entry(nil), node.Entries...)
		out[id] = &cp
	}
	return out
}
