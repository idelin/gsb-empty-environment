package procid

import (
	"errors"
	"testing"
)

const testPID = 31337

type testEnv struct {
	proc   *fakeProcFS
	opener *fakeOpener
	fd     *fakePidFD
	binder *Binder
}

func newTestEnv(t *testing.T, comm, exe string, starttime uint64) *testEnv {
	t.Helper()
	proc := newFakeProcFS()
	opener := newFakeOpener()
	fd := opener.register(&fakePidFD{pid: testPID, alive: true})
	proc.put(testPID, &fakeProcEntry{
		stat: statLine(testPID, comm, starttime),
		exe:  exe,
	})
	return &testEnv{
		proc:   proc,
		opener: opener,
		fd:     fd,
		binder: &Binder{Proc: proc, Opener: opener},
	}
}

func baseCred() PeerCred {
	return PeerCred{PID: testPID, UID: 1000, GID: 1000}
}

// 合法进程：绑定与后续认证均通过，基线字段被正确记录。
func TestBindAndVerifyLegitimate(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)

	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer binding.Close()

	if binding.PID() != testPID || binding.UID() != 1000 || binding.GID() != 1000 {
		t.Fatalf("credential fields wrong: %+v", binding)
	}
	if binding.Starttime() != 5000 || binding.Exe() != "/usr/sbin/goodd" ||
		binding.Comm() != "goodd" {
		t.Fatalf("baseline fields wrong: %+v", binding)
	}
	if err := binding.Verify(); err != nil {
		t.Fatalf("Verify on unchanged process: %v", err)
	}
	if env.opener.calls != 1 {
		t.Fatalf("opener called %d times, want 1", env.opener.calls)
	}
}

// 伪造 comm：进程名含空格和右括号。解析必须以最后一个 ')' 为准，
// 绑定成功且 starttime 正确；之后 comm 再被改写也不影响认证结论。
func TestForgedComm(t *testing.T) {
	forged := "e)v i l (rm -rf /) "
	env := newTestEnv(t, forged, "/usr/sbin/goodd", 777)

	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind with forged comm: %v", err)
	}
	if binding.Starttime() != 777 || binding.Comm() != forged {
		t.Fatalf("forged comm parsed wrong: start=%d comm=%q",
			binding.Starttime(), binding.Comm())
	}

	// 认证前攻击者再次改写 comm，starttime/exe 不变，身份仍然成立。
	env.proc.entries[testPID].stat = statLine(testPID, "totally different ) name", 777)
	if err := binding.Verify(); err != nil {
		t.Fatalf("Verify must not depend on comm, got %v", err)
	}
}

// PID 复用场景一：原 task 已退出，pidfd 探活失败，直接拒绝。
func TestVerifyRejectsExitedProcess(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	env.fd.alive = false
	err = binding.Verify()
	if !errors.Is(err, ErrProcessExited) {
		t.Fatalf("got %v, want ErrProcessExited", err)
	}
}

// PID 复用场景二：同一数字 PID 已被新 task 占用，starttime 不同。
// 即便 pidfd 探活被假实现误判为存活，starttime 比对仍必须拦下。
func TestVerifyRejectsReusedPIDByStarttime(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// 模拟 PID 回收：procfs 上同 PID 的 stat 变成新进程的启动时刻。
	env.proc.entries[testPID].stat = statLine(testPID, "goodd", 99999)
	err = binding.Verify()
	if !errors.Is(err, ErrPIDReused) {
		t.Fatalf("got %v, want ErrPIDReused", err)
	}
}

// PID 复用场景三：stat 自报 PID 与请求 PID 不符（半回收状态读到了
// 属于另一个 task 的文件），同样按复用拒绝。
func TestVerifyRejectsStatPIDMismatch(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	env.proc.entries[testPID].stat = statLine(testPID+1, "goodd", 5000)
	err = binding.Verify()
	if !errors.Is(err, ErrPIDReused) {
		t.Fatalf("got %v, want ErrPIDReused", err)
	}
}

// 完整复用链：原进程退出 -> procfs 条目消失 -> 新进程占用同一 PID，
// pidfd 仍指向死掉的旧 task，认证被拒绝。
func TestVerifyFullPIDRecycling(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	env.fd.alive = false
	delete(env.proc.entries, testPID)
	env.proc.put(testPID, &fakeProcEntry{
		stat: statLine(testPID, "impostord", 424242),
		exe:  "/usr/sbin/impostord",
	})

	if err := binding.Verify(); !errors.Is(err, ErrProcessExited) {
		t.Fatalf("got %v, want ErrProcessExited", err)
	}
}

// exe 被替换场景一：readlink 出现 " (deleted)" 后缀（unlink 后重建）。
func TestVerifyRejectsDeletedExe(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	env.proc.entries[testPID].exe = "/usr/sbin/goodd (deleted)"
	if err := binding.Verify(); !errors.Is(err, ErrExeDeleted) {
		t.Fatalf("got %v, want ErrExeDeleted", err)
	}
}

// exe 被替换场景二：路径整体变化（同名升级、换镜像等）。
func TestVerifyRejectsChangedExe(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	env.proc.entries[testPID].exe = "/usr/local/sbin/goodd"
	if err := binding.Verify(); !errors.Is(err, ErrExeChanged) {
		t.Fatalf("got %v, want ErrExeChanged", err)
	}
}

// 绑定当时 exe 已 deleted，直接拒绝且必须关闭已打开的 pidfd。
func TestBindRejectsDeletedExeAndClosesFD(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd (deleted)", 5000)
	_, err := env.binder.Bind(baseCred())
	if !errors.Is(err, ErrExeDeleted) {
		t.Fatalf("got %v, want ErrExeDeleted", err)
	}
	if !env.fd.closed {
		t.Fatal("pidfd leaked after failed Bind")
	}
}

func TestBindRejectsInvalidPID(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	if _, err := env.binder.Bind(PeerCred{PID: 0}); !errors.Is(err, ErrInvalidPID) {
		t.Fatalf("got %v, want ErrInvalidPID", err)
	}
	if _, err := env.binder.Bind(PeerCred{PID: -1}); !errors.Is(err, ErrInvalidPID) {
		t.Fatalf("got %v, want ErrInvalidPID", err)
	}
}

func TestBindFailuresClosePidFD(t *testing.T) {
	// stat 读取失败。
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	env.proc.entries[testPID].statErr = errors.New("boom")
	if _, err := env.binder.Bind(baseCred()); err == nil {
		t.Fatal("expected stat error")
	} else if !env.fd.closed {
		t.Fatal("pidfd leaked on stat failure")
	}

	// exe 读取失败，使用新的 fd（上一个已关闭）。
	env2 := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	env2.proc.entries[testPID].exeErr = errors.New("boom")
	if _, err := env2.binder.Bind(baseCred()); err == nil {
		t.Fatal("expected exe error")
	} else if !env2.fd.closed {
		t.Fatal("pidfd leaked on exe failure")
	}

	// stat 损坏。
	env3 := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	env3.proc.entries[testPID].stat = "garbage"
	if _, err := env3.binder.Bind(baseCred()); !errors.Is(err, ErrStatUnparseable) {
		t.Fatalf("got %v, want ErrStatUnparseable", err)
	} else if !env3.fd.closed {
		t.Fatal("pidfd leaked on malformed stat")
	}
}

func TestBinderNotConfigured(t *testing.T) {
	if _, err := (&Binder{}).Bind(baseCred()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v, want ErrNotConfigured", err)
	}
	var nilBinding *Binding
	if err := nilBinding.Verify(); !errors.Is(err, ErrBindingClosed) {
		t.Fatalf("got %v, want ErrBindingClosed", err)
	}
}

func TestBindingClose(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := binding.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !env.fd.closed {
		t.Fatal("pidfd not closed")
	}
	if err := binding.Verify(); !errors.Is(err, ErrBindingClosed) {
		t.Fatalf("got %v, want ErrBindingClosed", err)
	}
	// 重复 Close 必须安全。
	if err := binding.Close(); err != nil {
		t.Fatalf("double Close: %v", err)
	}
}

// procfs 条目在两次认证之间消失（进程退出但探活前的竞态窗口），
// 读取错误必须原样上抛为失败，绝不放行。
func TestVerifyFailsWhenProcEntryVanishes(t *testing.T) {
	env := newTestEnv(t, "goodd", "/usr/sbin/goodd", 5000)
	binding, err := env.binder.Bind(baseCred())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	delete(env.proc.entries, testPID)
	if err := binding.Verify(); err == nil {
		t.Fatal("expected error when procfs entry vanishes")
	}
}
