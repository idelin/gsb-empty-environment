package mutex

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- 测试替身 ----

type fakeProc struct {
	alive bool
	start string
}

type fakeInspector struct {
	mu    sync.Mutex
	procs map[int]fakeProc
}

func newFakeInspector() *fakeInspector {
	return &fakeInspector{procs: map[int]fakeProc{
		os.Getpid(): {alive: true, start: "boot-A"},
	}}
}

func (f *fakeInspector) Inspect(pid int) ProcInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.procs[pid]
	if !ok {
		return ProcInfo{}
	}
	return ProcInfo{Alive: p.alive, StartToken: p.start}
}

func (f *fakeInspector) set(pid int, alive bool, start string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.procs[pid] = fakeProc{alive: alive, start: start}
}

type fixture struct {
	dir   string
	clock *FakeClock
	insp  *fakeInspector
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{
		dir:   t.TempDir(),
		clock: NewFakeClock(time.Unix(1_700_000_000, 0)),
		insp:  newFakeInspector(),
	}
}

func (f *fixture) handle(t *testing.T, ttl time.Duration, hooks Hooks) *Handle {
	t.Helper()
	h, err := New(f.dir, "test", &Options{
		Clock:     f.clock,
		Inspector: f.insp,
		LeaseTTL:  ttl,
		Hooks:     hooks,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

// ---- 基本获取/释放 ----

func TestAcquireReleaseAndContention(t *testing.T) {
	fx := newFixture(t)
	a := fx.handle(t, time.Minute, Hooks{})
	b := fx.handle(t, time.Minute, Hooks{})

	tokA, err := a.Acquire()
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	if tokA != 1 {
		t.Fatalf("first token = %d, want 1", tokA)
	}

	if _, err := b.Acquire(); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("B acquire while held: err = %v, want ErrLockHeld", err)
	}
	if _, st, _ := b.Inspect(); st != StateLive {
		t.Fatalf("state = %v, want live", st)
	}

	if err := a.Release(); err != nil {
		t.Fatalf("A release: %v", err)
	}
	if _, st, _ := b.Inspect(); st != StateFree {
		t.Fatalf("state = %v, want free", st)
	}

	tokB, err := b.Acquire()
	if err != nil {
		t.Fatalf("B acquire: %v", err)
	}
	if tokB != 2 {
		t.Fatalf("token after release = %d, want 2 (monotonic)", tokB)
	}
}

// ---- 租约过期：失联接管 + 旧令牌失效 ----

func TestLeaseExpiryTakeoverAndStaleToken(t *testing.T) {
	fx := newFixture(t)
	a := fx.handle(t, 10*time.Second, Hooks{})
	b := fx.handle(t, 10*time.Second, Hooks{})

	tokA, err := a.Acquire()
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}

	// 持有者进程仍存活，但租约停止续约 → 失联。
	fx.clock.Advance(11 * time.Second)
	if _, st, _ := b.Inspect(); st != StateLost {
		t.Fatalf("state = %v, want lost", st)
	}

	tokB, err := b.Acquire()
	if err != nil {
		t.Fatalf("B takeover: %v", err)
	}
	if tokB <= tokA {
		t.Fatalf("token not increasing: old=%d new=%d", tokA, tokB)
	}

	// 旧持有者"恢复"后：续约、释放、凭旧令牌提交都必须被拒绝。
	if err := a.Renew(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("A renew after takeover: err = %v, want ErrNotOwner", err)
	}
	if err := a.Release(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("A release after takeover: err = %v, want ErrNotOwner", err)
	}
	if err := b.ValidateToken(tokA); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("validate old token: err = %v, want ErrStaleToken", err)
	}
	if err := b.ValidateToken(tokB); err != nil {
		t.Fatalf("validate current token: %v", err)
	}

	// 新持有者的记录未被旧持有者破坏。
	if rec, st, _ := b.Inspect(); st != StateLive || rec.OwnerID != b.OwnerID() {
		t.Fatalf("record clobbered: %+v state=%v", rec, st)
	}
}

// ---- 持有者死亡：陈旧接管 ----

func TestStaleOwnerProcessDead(t *testing.T) {
	fx := newFixture(t)
	a := fx.handle(t, time.Minute, Hooks{})
	b := fx.handle(t, time.Minute, Hooks{})

	if _, err := a.Acquire(); err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	fx.insp.set(os.Getpid(), false, "") // 模拟进程崩溃

	if _, st, _ := b.Inspect(); st != StateStale {
		t.Fatalf("state = %v, want stale", st)
	}
	tokB, err := b.Acquire()
	if err != nil {
		t.Fatalf("B takeover: %v", err)
	}
	if tokB != 2 {
		t.Fatalf("token = %d, want 2", tokB)
	}
}

// ---- PID 复用：不能误认原持有者 ----

func TestPIDReuseNotConfusedWithOriginalOwner(t *testing.T) {
	fx := newFixture(t)
	a := fx.handle(t, time.Minute, Hooks{})
	b := fx.handle(t, time.Minute, Hooks{})

	if _, err := a.Acquire(); err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	// 原进程退出，OS 把同一 PID 分给了另一个进程（启动时间不同）。
	fx.insp.set(os.Getpid(), true, "boot-B")

	if _, st, _ := b.Inspect(); st != StateStale {
		t.Fatalf("state = %v, want stale (pid reused)", st)
	}
	if _, err := b.Acquire(); err != nil {
		t.Fatalf("B takeover after pid reuse: %v", err)
	}

	// 对照：PID 存活且启动标识一致时，必须视为 live 并拒绝获取。
	fx2 := newFixture(t)
	c := fx2.handle(t, time.Minute, Hooks{})
	d := fx2.handle(t, time.Minute, Hooks{})
	if _, err := c.Acquire(); err != nil {
		t.Fatalf("C acquire: %v", err)
	}
	if _, err := d.Acquire(); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("D acquire: err = %v, want ErrLockHeld", err)
	}
}

// ---- 释放别人的锁应被拒绝 ----

func TestReleaseOthersLockRejected(t *testing.T) {
	fx := newFixture(t)
	a := fx.handle(t, time.Minute, Hooks{})
	b := fx.handle(t, time.Minute, Hooks{})

	if _, err := a.Acquire(); err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	if err := b.Release(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("B release: err = %v, want ErrNotOwner", err)
	}
	if err := b.Renew(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("B renew: err = %v, want ErrNotOwner", err)
	}
	if rec, st, _ := a.Inspect(); st != StateLive || rec.OwnerID != a.OwnerID() {
		t.Fatalf("lock disturbed by foreign release: %+v %v", rec, st)
	}
}

// ---- 令牌单调性：跨多次获取/释放严格递增 ----

func TestFencingTokenMonotonic(t *testing.T) {
	fx := newFixture(t)
	var prev uint64
	for i := 0; i < 5; i++ {
		h := fx.handle(t, time.Minute, Hooks{})
		tok, err := h.Acquire()
		if err != nil {
			t.Fatalf("acquire #%d: %v", i, err)
		}
		if tok <= prev {
			t.Fatalf("token not increasing: %d <= %d", tok, prev)
		}
		prev = tok
		if err := h.Release(); err != nil {
			t.Fatalf("release #%d: %v", i, err)
		}
	}
}

// ---- 故障注入：提交前崩溃不留残局 ----

func TestFaultInjectionBeforeCommit(t *testing.T) {
	fx := newFixture(t)
	boom := errors.New("injected crash")
	h := fx.handle(t, time.Minute, Hooks{BeforeCommit: func() error { return boom }})

	if _, err := h.Acquire(); !errors.Is(err, boom) {
		t.Fatalf("acquire err = %v, want injected error", err)
	}
	if _, st, _ := h.Inspect(); st != StateFree {
		t.Fatalf("state = %v, want free (no partial commit)", st)
	}
	entries, _ := os.ReadDir(fx.dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

// ---- 故障注入：提交后崩溃，记录已持久化，可恢复 ----

func TestFaultInjectionAfterCommit(t *testing.T) {
	fx := newFixture(t)
	boom := errors.New("injected crash after durable commit")
	var once sync.Once
	inject := func() error {
		var err error
		once.Do(func() { err = boom })
		return err
	}
	h := fx.handle(t, time.Minute, Hooks{AfterCommit: inject})

	// 第一次 Acquire：记录已落盘，但结果对调用方表现为失败。
	if _, err := h.Acquire(); !errors.Is(err, boom) {
		t.Fatalf("acquire err = %v, want injected error", err)
	}
	rec, st, err := h.Inspect()
	if err != nil || st != StateLive || rec.Token != 1 {
		t.Fatalf("after crash: rec=%+v state=%v err=%v, want live token=1", rec, st, err)
	}

	// 恢复：同一句柄重新 Acquire，发现记录是自己的 → 幂等恢复。
	tok, err := h.Acquire()
	if err != nil {
		t.Fatalf("recovery acquire: %v", err)
	}
	if tok != 1 {
		t.Fatalf("recovery token = %d, want 1 (no double increment)", tok)
	}
}

// ---- 并发获取：同一时刻只有一个赢家 ----

func TestConcurrentAcquireSingleWinner(t *testing.T) {
	fx := newFixture(t)
	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []uint64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := fx.handle(t, time.Minute, Hooks{})
			tok, err := h.Acquire()
			if err == nil {
				mu.Lock()
				winners = append(winners, tok)
				mu.Unlock()
			} else if !errors.Is(err, ErrLockHeld) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if len(winners) != 1 {
		t.Fatalf("winners = %v, want exactly one", winners)
	}
}

// ---- 真实跨进程：崩溃恢复 + 旧令牌拒绝 ----

func TestCrossProcessCrashRecovery(t *testing.T) {
	if os.Getenv("MUTEX_TEST_HELPER") == "1" {
		helperProcess()
		return
	}
	dir := t.TempDir()
	parent, err := New(dir, "test", &Options{LeaseTTL: 30 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestCrossProcessCrashRecovery")
	cmd.Env = append(os.Environ(), "MUTEX_TEST_HELPER=1", "MUTEX_TEST_DIR="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	var childToken uint64
	if _, err := fmt.Fscanf(bufio.NewReader(stdout), "TOKEN=%d\n", &childToken); err != nil {
		t.Fatalf("read child token: %v", err)
	}

	// 子进程存活持锁：父进程获取应失败，状态为 live。
	if _, err := parent.Acquire(); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("acquire while child holds: err = %v, want ErrLockHeld", err)
	}
	rec, st, _ := parent.Inspect()
	if st != StateLive || rec.PID != cmd.Process.Pid {
		t.Fatalf("state=%v rec=%+v, want live owned by pid %d", st, rec, cmd.Process.Pid)
	}

	// 模拟子进程崩溃（SIGKILL），并等待其被回收。
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	cmd.Wait()

	// 父进程看到陈旧锁并接管，令牌递增。
	if _, st, _ := parent.Inspect(); st != StateStale {
		t.Fatalf("state after crash = %v, want stale", st)
	}
	tok, err := parent.Acquire()
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if tok != childToken+1 {
		t.Fatalf("token = %d, want child+1 = %d", tok, childToken+1)
	}

	// 崩溃的旧持有者若"醒来"凭旧令牌提交，必须被拒绝。
	if err := parent.ValidateToken(childToken); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("validate crashed owner's token: err = %v, want ErrStaleToken", err)
	}
	if err := parent.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func helperProcess() {
	h, err := New(os.Getenv("MUTEX_TEST_DIR"), "test", &Options{LeaseTTL: 30 * time.Second})
	if err != nil {
		os.Exit(1)
	}
	tok, err := h.Acquire()
	if err != nil {
		os.Exit(1)
	}
	fmt.Printf("TOKEN=%d\n", tok)
	time.Sleep(time.Minute) // 等待父进程杀死
}
