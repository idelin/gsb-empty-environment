package netsec

import (
	"errors"
	"testing"
)

func newTestTree(t *testing.T) *ProcessTree {
	t.Helper()
	tree := NewProcessTreeWithInit(100, 1000)
	// init(100)
	//   attacker(101, uid 1000)
	//     child(102)
	//       grandchild(103)
	//   browser(104, uid 1000)
	//     browserChild(105)
	//   sshAgent(106, uid 1001)
	mustOK(t, tree.ForkAs(100, 101, 1000, nil))
	mustOK(t, tree.ForkAs(101, 102, 1000, nil))
	mustOK(t, tree.ForkAs(102, 103, 1000, nil))
	mustOK(t, tree.ForkAs(100, 104, 1000, nil))
	mustOK(t, tree.ForkAs(104, 105, 1000, nil))
	mustOK(t, tree.ForkAs(100, 106, 1001, nil))
	return tree
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func TestForkInheritsCredentials(t *testing.T) {
	tree := NewProcessTree()
	parent, err := tree.Fork(1)
	mustOK(t, err)

	parentProc, err := tree.Lookup(parent.PID)
	mustOK(t, err)
	if parentProc.UID != 0 || parentProc.PPID != 1 {
		t.Fatalf("unexpected parent snapshot: %+v", parentProc)
	}

	mustOK(t, tree.ForkAs(parent.PID, 7, 1000, NewCapSet(CAP_SYS_PTRACE)))
	child, err := tree.Fork(7)
	mustOK(t, err)

	snapshot, err := tree.Lookup(child.PID)
	mustOK(t, err)
	if snapshot.UID != 1000 || !snapshot.Caps.Has(CAP_SYS_PTRACE) {
		t.Fatalf("child did not inherit uid/caps: %+v", snapshot)
	}
	if snapshot.PPID != 7 {
		t.Fatalf("child ppid = %d, want 7", snapshot.PPID)
	}
}

func TestForkRejectsUnknownParentAndDuplicate(t *testing.T) {
	tree := NewProcessTree()
	if _, err := tree.Fork(999); !errors.Is(err, ErrNoSuchProcess) {
		t.Fatalf("fork under missing parent: got %v, want ErrNoSuchProcess", err)
	}
	mustOK(t, tree.ForkAs(1, 2, 0, nil))
	assertErrorIs(t, tree.ForkAs(1, 2, 0, nil), ErrProcessExists)
	assertErrorIs(t, tree.Add(3, 999, 0, nil), ErrNoSuchProcess)
}

func TestIsDescendant(t *testing.T) {
	tree := newTestTree(t)

	cases := []struct {
		name     string
		ancestor int
		pid      int
		want     bool
	}{
		{"direct child", 101, 102, true},
		{"grandchild", 101, 103, true},
		{"init ancestor", 100, 103, true},
		{"sibling branch", 101, 104, false},
		{"uncle to nephew", 101, 105, false},
		{"self is not descendant", 101, 101, false},
		{"upward direction", 102, 101, false},
		{"missing ancestor", 999, 102, false},
		{"missing pid", 101, 999, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tree.IsDescendant(tc.ancestor, tc.pid); got != tc.want {
				t.Fatalf("IsDescendant(%d, %d) = %v, want %v", tc.ancestor, tc.pid, got, tc.want)
			}
		})
	}
}

func TestExitReparentsOrphansToInit(t *testing.T) {
	tree := newTestTree(t)

	// 退出前 101 是 102/103 的祖先。
	if !tree.IsDescendant(101, 103) {
		t.Fatal("precondition: grandchild should be descendant of attacker")
	}

	mustOK(t, tree.Exit(101))

	if _, err := tree.Lookup(101); !errors.Is(err, ErrNoSuchProcess) {
		t.Fatalf("exited process still visible: %v", err)
	}

	// 直接子进程 102 重挂到 init。
	reparented, err := tree.Lookup(102)
	mustOK(t, err)
	if reparented.PPID != 100 {
		t.Fatalf("orphan ppid = %d, want init 100", reparented.PPID)
	}

	// 103 经新父链挂在 init 下，不再属于原祖先 101。
	if tree.IsDescendant(101, 103) {
		t.Fatal("grandchild must no longer belong to exited ancestor")
	}
	if !tree.IsDescendant(100, 103) {
		t.Fatal("grandchild should now be reachable from init")
	}

	// 孙进程的直接父进程仍是 102，只是 102 的父进程变成 init。
	grand, err := tree.Lookup(103)
	mustOK(t, err)
	if grand.PPID != 102 {
		t.Fatalf("grandchild ppid = %d, want 102", grand.PPID)
	}
}

func TestExitRemovesPtracerAuthorization(t *testing.T) {
	tree := newTestTree(t)
	mustOK(t, tree.SetPtracer(104, 101))

	tracer, ok, err := tree.HasPtracer(104)
	mustOK(t, err)
	if !ok || tracer != 101 {
		t.Fatalf("ptracer = (%d, %v), want (101, true)", tracer, ok)
	}

	mustOK(t, tree.Exit(104))
	if _, _, err := tree.HasPtracer(104); !errors.Is(err, ErrNoSuchProcess) {
		t.Fatalf("ptracer state survived exit: %v", err)
	}

	// 目标仍在、仅撤销授权的路径。
	mustOK(t, tree.SetPtracer(106, PtracerAny))
	mustOK(t, tree.ClearPtracer(106))
	if _, ok, err := tree.HasPtracer(106); err != nil || ok {
		t.Fatalf("after clear: ok=%v err=%v, want false/nil", ok, err)
	}
}

func TestInitCannotExit(t *testing.T) {
	tree := NewProcessTreeWithInit(100, 0)
	assertErrorIs(t, tree.Exit(100), ErrInitExited)
	assertErrorIs(t, tree.Exit(999), ErrNoSuchProcess)
}

func TestSetPtracerValidation(t *testing.T) {
	tree := newTestTree(t)

	assertErrorIs(t, tree.SetPtracer(999, 101), ErrNoSuchProcess)
	assertErrorIs(t, tree.SetPtracer(104, 999), ErrNoSuchProcess)

	// PR_SET_PTRACER_ANY 不依赖具体进程，始终可设置。
	mustOK(t, tree.SetPtracer(104, PtracerAny))
	tracer, ok, err := tree.HasPtracer(104)
	mustOK(t, err)
	if !ok || tracer != PtracerAny {
		t.Fatalf("ptracer any = (%d, %v), want (0, true)", tracer, ok)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	tree := newTestTree(t)
	first, err := tree.Lookup(101)
	mustOK(t, err)
	first.UID = 4242
	first.Caps = NewCapSet(CAP_SYS_PTRACE)

	second, err := tree.Lookup(101)
	mustOK(t, err)
	if second.UID == 4242 || second.Caps.Has(CAP_SYS_PTRACE) {
		t.Fatalf("mutating snapshot leaked into tree: %+v", second)
	}
}

func TestConcurrentAccess(t *testing.T) {
	tree := NewProcessTree()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for range 100 {
			child, err := tree.Fork(1)
			if err == nil {
				_ = tree.SetPtracer(child.PID, PtracerAny)
				_, _ = tree.Lookup(child.PID)
				_ = tree.Exit(child.PID)
			}
		}
	}()

	for range 100 {
		_ = tree.IsDescendant(1, 2)
		_ = tree.PIDs()
	}
	<-done
}
