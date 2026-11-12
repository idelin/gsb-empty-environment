package netsec

import (
	"reflect"
	"strings"
	"testing"
)

// testGraph models a small filesystem:
//
//	0 "/"            (global root)
//	1 "/jail"        (confinement root in most tests)
//	2 "/jail/etc"
//	3 "/jail/etc/ssl"
//	4 "/jail/home"
//	5 "/etc"
//	6 "/etc/ssh"
//	7 "/tmp"
func testGraph(t *testing.T) *Graph {
	t.Helper()
	g, err := NewGraph([]Dir{
		{Name: "/", Parent: 0},
		{Name: "/jail", Parent: 0},
		{Name: "/jail/etc", Parent: 1},
		{Name: "/jail/etc/ssl", Parent: 2},
		{Name: "/jail/home", Parent: 1},
		{Name: "/etc", Parent: 0},
		{Name: "/etc/ssh", Parent: 5},
		{Name: "/tmp", Parent: 0},
	})
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func findReport(t *testing.T, rep *Report, id int) HandleReport {
	t.Helper()
	for _, hr := range rep.Handles {
		if hr.Handle.ID == id {
			return hr
		}
	}
	t.Fatalf("no report for handle %d", id)
	return HandleReport{}
}

func TestInsideHandleCappedAtRoot(t *testing.T) {
	g := testGraph(t)
	rep, err := Analyze(&Input{
		Graph:   g,
		Root:    1,
		Cwd:     2,
		Handles: []Handle{{ID: 3, Object: 2}},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !rep.Safe() {
		t.Fatalf("expected safe report, close set %v", rep.CloseSet)
	}
	if !rep.Verified {
		t.Fatal("report not verified")
	}
	hr := findReport(t, rep, 3)
	if !hr.Capped {
		t.Fatal("inside handle must be capped at root")
	}
	want := []ObjectID{1, 2, 3, 4}
	if !reflect.DeepEqual(hr.Reachable, want) {
		t.Fatalf("reachable = %v, want %v", hr.Reachable, want)
	}
	if hr.Escapes || hr.Witness != nil {
		t.Fatalf("inside handle must not escape, witness %v", hr.Witness)
	}
}

func TestExternalHandleEscapesUncapped(t *testing.T) {
	g := testGraph(t)
	rep, err := Analyze(&Input{
		Graph:   g,
		Root:    1,
		Cwd:     2,
		Handles: []Handle{{ID: 7, Object: 6}},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	hr := findReport(t, rep, 7)
	if hr.Capped {
		t.Fatal("external handle must not be capped")
	}
	if !hr.Escapes {
		t.Fatal("external handle must escape")
	}
	// From /etc/ssh the whole graph is reachable: up to "/" and back
	// down into the jail.
	if len(hr.Reachable) != g.NumObjects() {
		t.Fatalf("reachable = %v, want all %d objects", hr.Reachable, g.NumObjects())
	}
	if want := []ObjectID{6}; !reflect.DeepEqual(hr.Witness, want) {
		t.Fatalf("witness = %v, want %v", hr.Witness, want)
	}
	if want := []int{7}; !reflect.DeepEqual(rep.CloseSet, want) {
		t.Fatalf("close set = %v, want %v", rep.CloseSet, want)
	}
	if rep.CwdEscapes {
		t.Fatal("cwd is inside the root and must not be flagged")
	}
	if !rep.Verified {
		t.Fatal("report not verified")
	}
}

func TestCwdOutsideRootEscapes(t *testing.T) {
	g := testGraph(t)
	// Classic chroot mistake: root switched but cwd left outside.
	rep, err := Analyze(&Input{Graph: g, Root: 1, Cwd: 5})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !rep.CwdEscapes {
		t.Fatal("cwd outside the root must be flagged")
	}
	if want := []int{CwdHandleID}; !reflect.DeepEqual(rep.CloseSet, want) {
		t.Fatalf("close set = %v, want %v", rep.CloseSet, want)
	}
	cwd := findReport(t, rep, CwdHandleID)
	if cwd.Kind != HandleCwd {
		t.Fatalf("kind = %v, want HandleCwd", cwd.Kind)
	}
	if want := []ObjectID{5}; !reflect.DeepEqual(cwd.Witness, want) {
		t.Fatalf("witness = %v, want %v", cwd.Witness, want)
	}
	if !rep.Verified {
		t.Fatal("report not verified")
	}
}

func TestMinimalCloseSetPicksOnlyEscapingHandles(t *testing.T) {
	g := testGraph(t)
	rep, err := Analyze(&Input{
		Graph: g,
		Root:  1,
		Cwd:   4,
		Handles: []Handle{
			{ID: 10, Object: 4}, // inside, safe
			{ID: 11, Object: 7}, // /tmp, escapes
			{ID: 12, Object: 6}, // /etc/ssh, escapes
			{ID: 13, Object: 1}, // root itself, capped, safe
		},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if want := []int{11, 12}; !reflect.DeepEqual(rep.CloseSet, want) {
		t.Fatalf("close set = %v, want %v", rep.CloseSet, want)
	}
	if rep.Safe() {
		t.Fatal("report must not be safe while handles escape")
	}
	if !rep.Verified {
		t.Fatal("report not verified")
	}
	rootHandle := findReport(t, rep, 13)
	if want := []ObjectID{1, 2, 3, 4}; !reflect.DeepEqual(rootHandle.Reachable, want) {
		t.Fatalf("root handle reachable = %v, want %v", rootHandle.Reachable, want)
	}
}

func TestWitnessPathThroughGlobalRoot(t *testing.T) {
	g := testGraph(t)
	// Root at /jail/etc: a handle inside it is capped there, but a
	// handle at /jail/home is outside the new root and escapes.
	rep, err := Analyze(&Input{
		Graph:   g,
		Root:    2,
		Cwd:     3,
		Handles: []Handle{{ID: 5, Object: 4}},
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	hr := findReport(t, rep, 5)
	if !hr.Escapes {
		t.Fatal("handle at /jail/home must escape root /jail/etc")
	}
	if want := []ObjectID{4}; !reflect.DeepEqual(hr.Witness, want) {
		t.Fatalf("witness = %v, want %v", hr.Witness, want)
	}
	desc := g.DescribePath(hr.Witness)
	if !strings.Contains(desc, "/jail/home") {
		t.Fatalf("DescribePath = %q, want it to mention /jail/home", desc)
	}
}

func TestDescribePathAnnotatesParentHops(t *testing.T) {
	g := testGraph(t)
	got := g.DescribePath([]ObjectID{3, 2, 1})
	want := "/jail/etc/ssl --..--> /jail/etc --..--> /jail"
	if got != want {
		t.Fatalf("DescribePath = %q, want %q", got, want)
	}
}

func TestTooManyHandles(t *testing.T) {
	g := testGraph(t)
	handles := make([]Handle, MaxHandles+1)
	for i := range handles {
		handles[i] = Handle{ID: i, Object: 4}
	}
	if _, err := Analyze(&Input{Graph: g, Root: 1, Cwd: 2, Handles: handles}); err == nil {
		t.Fatal("expected error for more than MaxHandles handles")
	}
	// Exactly MaxHandles must be accepted.
	handles = handles[:MaxHandles]
	rep, err := Analyze(&Input{Graph: g, Root: 1, Cwd: 2, Handles: handles})
	if err != nil {
		t.Fatalf("Analyze with MaxHandles handles: %v", err)
	}
	if !rep.Safe() {
		t.Fatalf("all handles inside root, close set = %v", rep.CloseSet)
	}
}

func TestInputValidation(t *testing.T) {
	g := testGraph(t)
	cases := []struct {
		name string
		in   *Input
	}{
		{"nil input", nil},
		{"root out of range", &Input{Graph: g, Root: 99, Cwd: 2}},
		{"cwd out of range", &Input{Graph: g, Root: 1, Cwd: 99}},
		{"handle object out of range", &Input{Graph: g, Root: 1, Cwd: 2, Handles: []Handle{{ID: 1, Object: 99}}}},
		{"duplicate handle IDs", &Input{Graph: g, Root: 1, Cwd: 2, Handles: []Handle{{ID: 1, Object: 2}, {ID: 1, Object: 4}}}},
		{"reserved cwd handle ID", &Input{Graph: g, Root: 1, Cwd: 2, Handles: []Handle{{ID: CwdHandleID, Object: 2}}}},
	}
	for _, tc := range cases {
		if _, err := Analyze(tc.in); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestGraphValidation(t *testing.T) {
	if _, err := NewGraph(nil); err == nil {
		t.Fatal("empty graph: expected error")
	}
	if _, err := NewGraph([]Dir{{Name: "x", Parent: 5}}); err == nil {
		t.Fatal("parent out of range: expected error")
	}
	// Cycle without a self-loop global root.
	if _, err := NewGraph([]Dir{
		{Name: "a", Parent: 1},
		{Name: "b", Parent: 0},
	}); err == nil {
		t.Fatal("parent cycle: expected error")
	}
}

func TestHandleKindString(t *testing.T) {
	if HandleInherited.String() != "inherited" || HandleCwd.String() != "cwd" {
		t.Fatalf("unexpected kind strings: %v %v", HandleInherited, HandleCwd)
	}
}
