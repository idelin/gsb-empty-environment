package freeze_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"example.org/netsec/q0324/freeze"
)

// fakeSystem 是一个可编程的进程表，同时实现 freeze.Enumerator 与
// freeze.Freezer。enumHooks[i] 在第 i+1 次枚举前触发，用于模拟枚举
// 与冻结之间的并发变化（派生、退出、PID 复用、跨树迁入）。
type fakeSystem struct {
	procs         map[int]fakeProc
	nextStart     uint64
	enumHooks     []func(*fakeSystem)
	enumCalls     int
	freezeErr     map[int]error
	alreadyFrozen map[int]bool
	freezeLog     []freeze.Key
	thawLog       []freeze.Key
}

type fakeProc struct {
	ppid   int
	start  uint64
	frozen bool
}

var errStale = errors.New("stale process instance")

func newFakeSystem() *fakeSystem {
	return &fakeSystem{
		procs:         make(map[int]fakeProc),
		nextStart:     1,
		freezeErr:     make(map[int]error),
		alreadyFrozen: make(map[int]bool),
	}
}

func (s *fakeSystem) add(pid, ppid int) {
	s.procs[pid] = fakeProc{ppid: ppid, start: s.nextStart}
	s.nextStart++
}

func (s *fakeSystem) spawn(ppid int) int {
	for pid := 1000; ; pid++ {
		if _, ok := s.procs[pid]; !ok {
			s.add(pid, ppid)
			return pid
		}
	}
}

func (s *fakeSystem) kill(pid int) { delete(s.procs, pid) }

// reusePID 模拟 PID 复用：旧实例退出，同一 PID 被分配给新实例（新启动代）。
func (s *fakeSystem) reusePID(pid, ppid int) {
	delete(s.procs, pid)
	s.add(pid, ppid)
}

func (s *fakeSystem) reparent(pid, ppid int) {
	p := s.procs[pid]
	p.ppid = ppid
	s.procs[pid] = p
}

func (s *fakeSystem) start(pid int) uint64 { return s.procs[pid].start }

func (s *fakeSystem) isFrozen(pid int) bool { return s.procs[pid].frozen }

func (s *fakeSystem) Enumerate(context.Context) ([]freeze.Process, error) {
	if s.enumCalls < len(s.enumHooks) && s.enumHooks[s.enumCalls] != nil {
		s.enumHooks[s.enumCalls](s)
	}
	s.enumCalls++
	out := make([]freeze.Process, 0, len(s.procs))
	for pid, p := range s.procs {
		out = append(out, freeze.Process{PID: pid, PPID: p.ppid, Start: p.start})
	}
	return out, nil
}

func (s *fakeSystem) Freeze(_ context.Context, pid int, start uint64) error {
	p, ok := s.procs[pid]
	if !ok || p.start != start {
		return errStale
	}
	if s.alreadyFrozen[pid] {
		return freeze.ErrAlreadyFrozen
	}
	if err := s.freezeErr[pid]; err != nil {
		return err
	}
	p.frozen = true
	s.procs[pid] = p
	s.freezeLog = append(s.freezeLog, freeze.Key{PID: pid, Start: start})
	return nil
}

func (s *fakeSystem) Thaw(_ context.Context, pid int, start uint64) error {
	p, ok := s.procs[pid]
	if !ok || p.start != start {
		return errStale
	}
	if !p.frozen {
		return fmt.Errorf("pid %d not frozen", pid)
	}
	p.frozen = false
	s.procs[pid] = p
	s.thawLog = append(s.thawLog, freeze.Key{PID: pid, Start: start})
	return nil
}

func pids(keys []freeze.Key) []int {
	out := make([]int, len(keys))
	for i, k := range keys {
		out[i] = k.PID
	}
	return out
}

func containsPID(keys []freeze.Key, pid int) bool {
	for _, k := range keys {
		if k.PID == pid {
			return true
		}
	}
	return false
}

func reversed(keys []freeze.Key) []freeze.Key {
	out := make([]freeze.Key, len(keys))
	for i, k := range keys {
		out[len(keys)-1-i] = k
	}
	return out
}

func equalKeys(a, b []freeze.Key) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStableTree(t *testing.T) {
	sys := newFakeSystem()
	sys.add(1, 0) // init
	sys.add(100, 1)
	sys.add(101, 100)
	sys.add(102, 101)
	sys.add(103, 101)
	sys.add(200, 1) // 树外进程

	ctl := &freeze.Controller{Enumerator: sys, Freezer: sys}
	res, err := ctl.FreezeTree(context.Background(), freeze.Key{PID: 100, Start: sys.start(100)})
	if err != nil {
		t.Fatalf("FreezeTree: %v", err)
	}
	if res.Rounds != 2 {
		t.Fatalf("Rounds = %d, want 2", res.Rounds)
	}
	for _, pid := range []int{100, 101, 102, 103} {
		if !containsPID(res.Frozen, pid) {
			t.Errorf("Frozen 缺少 pid %d", pid)
		}
	}
	if containsPID(res.Frozen, 200) {
		t.Errorf("Frozen 不应包含树外 pid 200")
	}
	if res.Frozen[0].PID != 100 {
		t.Errorf("根应最先冻结，got %v", pids(res.Frozen))
	}
	if len(sys.thawLog) != 0 {
		t.Errorf("成功路径不应恢复，thawLog = %v", sys.thawLog)
	}
}

func TestForkAfterEnumerate(t *testing.T) {
	sys := newFakeSystem()
	sys.add(1, 0)
	sys.add(100, 1)
	sys.add(101, 100)

	var child int
	// 第二轮枚举前，已枚举过的父进程派生新子进程。
	sys.enumHooks = []func(*fakeSystem){
		nil,
		func(s *fakeSystem) { child = s.spawn(101) },
	}

	ctl := &freeze.Controller{Enumerator: sys, Freezer: sys}
	res, err := ctl.FreezeTree(context.Background(), freeze.Key{PID: 100, Start: sys.start(100)})
	if err != nil {
		t.Fatalf("FreezeTree: %v", err)
	}
	if res.Rounds != 3 {
		t.Fatalf("Rounds = %d, want 3", res.Rounds)
	}
	if !containsPID(res.Frozen, child) {
		t.Errorf("枚举后派生的子进程 %d 未被冻结: %v", child, pids(res.Frozen))
	}
}

func TestPIDReuse(t *testing.T) {
	sys := newFakeSystem()
	sys.add(1, 0)
	sys.add(100, 1)
	sys.add(101, 100)
	sys.add(300, 1) // 树外进程，同样发生 PID 复用

	oldStart := sys.start(101)
	oldExternalStart := sys.start(300)
	// 第二轮枚举前：树内 101 退出且 PID 被复用（仍在树内）；
	// 树外 300 也被复用，但不应被卷入。
	sys.enumHooks = []func(*fakeSystem){
		nil,
		func(s *fakeSystem) {
			s.reusePID(101, 100)
			s.reusePID(300, 1)
		},
	}

	ctl := &freeze.Controller{Enumerator: sys, Freezer: sys}
	res, err := ctl.FreezeTree(context.Background(), freeze.Key{PID: 100, Start: sys.start(100)})
	if err != nil {
		t.Fatalf("FreezeTree: %v", err)
	}
	if res.Rounds != 3 {
		t.Fatalf("Rounds = %d, want 3", res.Rounds)
	}

	// 旧实例按旧启动代冻结，复用后的新实例必须按新启动代重新冻结。
	wantLog := []freeze.Key{
		{PID: 100, Start: sys.start(100)},
		{PID: 101, Start: oldStart},
		{PID: 101, Start: sys.start(101)},
	}
	if !equalKeys(sys.freezeLog, wantLog) {
		t.Errorf("freezeLog = %v, want %v", sys.freezeLog, wantLog)
	}
	for _, k := range res.Frozen {
		if k.PID == 101 && k.Start == oldStart {
			t.Errorf("结果中不应保留已复用的旧实例 %v", k)
		}
	}
	if containsPID(sys.freezeLog, 300) {
		t.Errorf("树外复用进程 300 不应被冻结")
	}
	if sys.start(300) == oldExternalStart {
		t.Fatal("测试前置条件失败：300 未发生复用")
	}
}

func TestCrossTreeMigration(t *testing.T) {
	sys := newFakeSystem()
	sys.add(1, 0)
	sys.add(100, 1)
	sys.add(101, 100)
	sys.add(300, 1) // 初始在树外

	// 第二轮枚举前，300 被重新挂接到树内（跨树迁入）。
	sys.enumHooks = []func(*fakeSystem){
		nil,
		func(s *fakeSystem) { s.reparent(300, 101) },
	}

	ctl := &freeze.Controller{Enumerator: sys, Freezer: sys}
	res, err := ctl.FreezeTree(context.Background(), freeze.Key{PID: 100, Start: sys.start(100)})
	if err != nil {
		t.Fatalf("FreezeTree: %v", err)
	}
	if res.Rounds != 3 {
		t.Fatalf("Rounds = %d, want 3", res.Rounds)
	}
	if !containsPID(res.Frozen, 300) {
		t.Errorf("迁入树内的进程 300 未被冻结: %v", pids(res.Frozen))
	}
}

func TestPermissionDeniedRollback(t *testing.T) {
	sys := newFakeSystem()
	sys.add(1, 0)
	sys.add(100, 1)
	sys.add(101, 100)
	sys.add(102, 101)
	sys.add(103, 101)

	permErr := errors.New("operation not permitted")
	sys.freezeErr[103] = permErr // 冻结中途权限失败

	ctl := &freeze.Controller{Enumerator: sys, Freezer: sys}
	_, err := ctl.FreezeTree(context.Background(), freeze.Key{PID: 100, Start: sys.start(100)})
	if err == nil {
		t.Fatal("权限失败不应返回成功")
	}
	if !errors.Is(err, permErr) {
		t.Fatalf("err = %v, 应包含权限错误", err)
	}

	// 冻结顺序为父先子后：100, 101, 102；103 失败未被冻结。
	wantFrozen := []int{100, 101, 102}
	if got := pids(sys.freezeLog); fmt.Sprint(got) != fmt.Sprint(wantFrozen) {
		t.Errorf("freezeLog = %v, want %v", got, wantFrozen)
	}
	// 恢复按反向依赖（子先父后），且仅恢复本次冻结者。
	if got := pids(sys.thawLog); fmt.Sprint(got) != fmt.Sprint([]int{102, 101, 100}) {
		t.Errorf("thawLog = %v, want [102 101 100]", got)
	}
	if containsPID(sys.thawLog, 103) {
		t.Errorf("冻结失败的 103 不应被恢复")
	}
	for _, pid := range wantFrozen {
		if sys.isFrozen(pid) {
			t.Errorf("pid %d 回滚后仍处冻结态", pid)
		}
	}
}

func TestAlreadyFrozenNotThawed(t *testing.T) {
	sys := newFakeSystem()
	sys.add(1, 0)
	sys.add(100, 1)
	sys.add(101, 100)
	sys.add(102, 101)

	sys.alreadyFrozen[101] = true // 101 已被其他主体冻结
	permErr := errors.New("operation not permitted")
	sys.freezeErr[102] = permErr

	ctl := &freeze.Controller{Enumerator: sys, Freezer: sys}
	_, err := ctl.FreezeTree(context.Background(), freeze.Key{PID: 100, Start: sys.start(100)})
	if !errors.Is(err, permErr) {
		t.Fatalf("err = %v, 应包含权限错误", err)
	}

	// 仅本次运行冻结的 100 被恢复；101 属于他人，不得触碰。
	if got := pids(sys.thawLog); fmt.Sprint(got) != fmt.Sprint([]int{100}) {
		t.Errorf("thawLog = %v, want [100]", got)
	}
	if containsPID(sys.thawLog, 101) {
		t.Errorf("他人冻结的 101 不应被恢复")
	}
}

func TestIncompleteForkBomb(t *testing.T) {
	sys := newFakeSystem()
	sys.add(1, 0)
	sys.add(100, 1)

	// 每轮枚举前都有新子进程派生，闭包永远无法稳定。
	sys.enumHooks = []func(*fakeSystem){
		func(s *fakeSystem) { s.spawn(100) },
		func(s *fakeSystem) { s.spawn(100) },
		func(s *fakeSystem) { s.spawn(100) },
	}

	ctl := &freeze.Controller{Enumerator: sys, Freezer: sys, MaxRounds: 3}
	_, err := ctl.FreezeTree(context.Background(), freeze.Key{PID: 100, Start: sys.start(100)})
	if !errors.Is(err, freeze.ErrIncomplete) {
		t.Fatalf("err = %v, 应为 ErrIncomplete", err)
	}

	// 未完成不是成功：本次冻结的实例必须全部按逆序恢复。
	if !equalKeys(sys.thawLog, reversed(sys.freezeLog)) {
		t.Errorf("thawLog = %v, 应为 freezeLog %v 的逆序", sys.thawLog, sys.freezeLog)
	}
	if len(sys.freezeLog) == 0 {
		t.Error("前置条件失败：应发生过冻结")
	}
	for pid, p := range sys.procs {
		if p.frozen {
			t.Errorf("回滚后 pid %d 仍处冻结态", pid)
		}
	}
}

func TestRootReusedDuringFreeze(t *testing.T) {
	sys := newFakeSystem()
	sys.add(1, 0)
	sys.add(100, 1)
	sys.add(101, 100)

	// 第二轮枚举前，根进程退出且 PID 被复用。
	sys.enumHooks = []func(*fakeSystem){
		nil,
		func(s *fakeSystem) { s.reusePID(100, 1) },
	}

	ctl := &freeze.Controller{Enumerator: sys, Freezer: sys}
	_, err := ctl.FreezeTree(context.Background(), freeze.Key{PID: 100, Start: sys.start(100)})
	if !errors.Is(err, freeze.ErrRootChanged) {
		t.Fatalf("err = %v, 应为 ErrRootChanged", err)
	}
	// 复用后的根 PID 不得被误恢复：101 已解冻，新 100 从未被冻结。
	if !sys.isFrozen(101) && !sys.isFrozen(100) {
		return
	}
	t.Errorf("回滚后仍存在冻结进程: 100=%v 101=%v", sys.isFrozen(100), sys.isFrozen(101))
}
