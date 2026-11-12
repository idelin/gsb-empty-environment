package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// executableEntries 统计普通目录树中引用目标对象代次且仍可执行的名字数。
func executableEntries(t *testing.T, fs *ModelFS, target ObjectRef) int {
	t.Helper()
	executable := 0
	for _, entry := range fs.Graph().NamesOf(target) {
		if fs.ExecutableAt(entry.Parent, entry.Name) {
			executable++
		}
	}
	return executable
}

// assertBlockedEverywhere 断言引用目标代次的所有名字都无法执行。
func assertBlockedEverywhere(t *testing.T, fs *ModelFS, target ObjectRef) {
	t.Helper()
	if got := executableEntries(t, fs, target); got != 0 {
		t.Fatalf("expected 0 executable entries for %s, got %d", target, got)
	}
}

func TestQuarantineAllHardLinks(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")
	fs.Link(target, root, "alias1")
	fs.Link(target, root, "alias2")

	report, err := Quarantine(fs, fs.Graph(), target)
	if err != nil || !report.Complete() {
		t.Fatalf("quarantine failed: err=%v report=%+v", err, report)
	}
	if len(report.Moved) != 3 {
		t.Fatalf("expected 3 moved records, got %d", len(report.Moved))
	}
	if fs.QuarantineLen() != 3 {
		t.Fatalf("expected 3 quarantine entries, got %d", fs.QuarantineLen())
	}
	for _, name := range []string{"evil", "alias1", "alias2"} {
		if fs.EntryExists(root, name) {
			t.Fatalf("name %q should have left the normal tree", name)
		}
	}
	if !fs.GateInstalled(target) {
		t.Fatal("execution gate must stay active after quarantine")
	}
	if len(fs.Graph().NamesOf(target)) != 0 {
		t.Fatal("no name may still reference the quarantined object generation")
	}
	assertBlockedEverywhere(t, fs, target)

	if err := report.Rollback(fs); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	for _, name := range []string{"evil", "alias1", "alias2"} {
		if !fs.EntryExists(root, name) {
			t.Fatalf("name %q should be restored", name)
		}
		if !fs.ExecutableAt(root, name) {
			t.Fatalf("restored name %q should be executable again", name)
		}
	}
	if fs.GateCount() != 0 {
		t.Fatalf("gate must be removed after full rollback, count=%d", fs.GateCount())
	}
}

func TestNewHardLinkAndTransientFailureAreCovered(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")
	fs.Link(target, root, "alias1")

	// 先固定事务输入快照，再新增硬链接并注入一次移动故障。事务首轮按旧
	// 快照处理，刷新引用图后必须发现新链接，且新链接自门禁安装起即被拦截。
	snapshot := fs.Graph()
	fs.Link(target, root, "late-link")
	fs.FailNextMoves = 1
	fs.MoveErr = errors.New("simulated transient I/O error")

	report, err := Quarantine(fs, snapshot, target)
	if err != nil || !report.Complete() {
		t.Fatalf("quarantine should converge across rounds: err=%v report=%+v", err, report)
	}
	if len(report.Moved) != 3 {
		t.Fatalf("expected 3 moved names including late link, got %d", len(report.Moved))
	}
	if fs.EntryExists(root, "late-link") {
		t.Fatal("late-created hard link must be quarantined too")
	}
	if fs.ExecAllowed(target) {
		t.Fatal("target object generation must remain denied execution")
	}
	assertBlockedEverywhere(t, fs, target)
}

func TestRenameDuringQuarantineIsFollowed(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	dir := fs.Mkdir(root, "sub")
	target := fs.Create(root, "evil")
	fs.Link(target, root, "alias1")

	// 事务输入快照生成后宿主上发生跨目录改名；事务必须按对象代次追踪，
	// 而不是盲信快照中的旧路径。
	snapshot := fs.Graph()
	fs.Rename(root, "alias1", dir, "moved-alias")

	report, err := Quarantine(fs, snapshot, target)
	if err != nil || !report.Complete() {
		t.Fatalf("quarantine must follow renamed entries: err=%v report=%+v", err, report)
	}
	if fs.EntryExists(root, "evil") || fs.EntryExists(dir, "moved-alias") {
		t.Fatal("both original and renamed names must leave the normal tree")
	}
	if len(report.Moved) != 2 {
		t.Fatalf("expected 2 moved names, got %d", len(report.Moved))
	}
	assertBlockedEverywhere(t, fs, target)
}

func TestNameRedirectedOrVanishedDoesNotBlockQuarantine(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")
	fs.Link(target, root, "alias1")
	fs.Link(target, root, "alias2")

	snapshot := fs.Graph()
	// alias1 被重定向到另一个新对象；alias2 在快照后直接消失。
	fs.Unlink(root, "alias1")
	foreign := fs.Create(root, "alias1")
	fs.Unlink(root, "alias2")

	report, err := Quarantine(fs, snapshot, target)
	if err != nil || !report.Complete() {
		t.Fatalf("quarantine should complete: err=%v report=%+v", err, report)
	}
	if len(report.Moved) != 1 || report.Moved[0].Name != "evil" {
		t.Fatalf("only the still-valid name should move, got %+v", report.Moved)
	}
	if got, err := fs.Stat(root, "alias1"); err != nil || got != foreign {
		t.Fatalf("foreign object at alias1 must be untouched, got %v err=%v", got, err)
	}
	if !fs.ExecutableAt(root, "alias1") {
		t.Fatal("name pointing at another object generation must stay executable")
	}
	if !fs.GateInstalled(target) {
		t.Fatal("gate for the target generation must remain active")
	}
}

func TestObjectNumberReuseKeepsGateGenerationScoped(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	firstGen := fs.Create(root, "evil")
	reusedID := firstGen.ID
	fs.Link(firstGen, root, "evil-link")

	report, err := Quarantine(fs, fs.Graph(), firstGen)
	if err != nil || !report.Complete() {
		t.Fatalf("quarantine failed: err=%v", err)
	}

	// 取证结束后销毁隔离对象，对象号回到复用池。
	fs.PurgeQuarantined()
	if _, ok := fs.objects[reusedID]; ok {
		t.Fatalf("object %d should be destroyed before reuse", reusedID)
	}

	// 新文件复用同一对象号但获得新代次：旧门禁继续压制旧代次，
	// 不得误伤承载新内容的新对象。
	secondGen := fs.Create(root, "benign")
	if secondGen.ID != reusedID {
		t.Fatalf("expected object number reuse of %d, got %d", reusedID, secondGen.ID)
	}
	if secondGen.Generation <= firstGen.Generation {
		t.Fatalf("reused object must get a new generation: first=%d second=%d",
			firstGen.Generation, secondGen.Generation)
	}
	if !fs.GateInstalled(firstGen) {
		t.Fatal("gate for the quarantined generation must remain installed")
	}
	if !fs.ExecutableAt(root, "benign") {
		t.Fatal("new generation reusing the object number must remain executable")
	}
	if !fs.ExecAllowed(secondGen) {
		t.Fatal("gate must be scoped by object generation, not by object number alone")
	}
	if fs.ExecAllowed(firstGen) {
		t.Fatal("old generation must still be denied execution")
	}

	if err := report.Rollback(fs); err == nil {
		t.Fatal("rollback of purged quarantine entries must fail and keep the gate")
	}
	if !fs.GateInstalled(firstGen) {
		t.Fatal("gate must be retained when recovery is impossible")
	}
}

func TestGateFailureMovesNothing(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")
	fs.Link(target, root, "alias1")
	gateErr := errors.New("gate subsystem unavailable")
	fs.FailInstallGate = gateErr

	report, err := Quarantine(fs, fs.Graph(), target)
	if !errors.Is(err, gateErr) {
		t.Fatalf("expected gate error %v, got %v", gateErr, err)
	}
	if report.GateOK {
		t.Fatal("report must indicate gate is not installed")
	}
	if len(report.Moved) != 0 || fs.QuarantineLen() != 0 {
		t.Fatal("no name may move when gate installation fails")
	}
	if !fs.EntryExists(root, "evil") || !fs.EntryExists(root, "alias1") {
		t.Fatal("all names must remain in place after gate failure")
	}
	if fs.GateCount() != 0 {
		t.Fatal("no gate may be installed after failed installation")
	}
}

func TestPersistentMoveFailureKeepsGateAndRecoveryList(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")
	fs.Link(target, root, "alias1")
	fs.Link(target, root, "alias2")
	fs.FailNextMoves = maxQuarantineRounds * 4
	fs.MoveErr = errors.New("quarantine volume offline")

	report, err := Quarantine(fs, fs.Graph(), target)
	if !errors.Is(err, ErrQuarantineIncomplete) {
		t.Fatalf("expected ErrQuarantineIncomplete, got %v", err)
	}
	if report.Complete() {
		t.Fatal("report must be incomplete")
	}
	if !fs.GateInstalled(target) {
		t.Fatal("gate must be retained despite partial failure")
	}
	if len(report.Remaining) != 3 {
		t.Fatalf("recovery list must contain all 3 remaining names, got %d", len(report.Remaining))
	}
	if fs.QuarantineLen() != 0 {
		t.Fatal("no successful move may reach the quarantine mapping")
	}
	assertBlockedEverywhere(t, fs, target)

	// 故障恢复后凭恢复清单重试，事务应完整收敛。
	fs.FailNextMoves = 0
	retryReport, err := report.Retry(fs)
	if err != nil || !retryReport.Complete() {
		t.Fatalf("retry should complete quarantine: err=%v report=%+v", err, retryReport)
	}
	if len(retryReport.Moved) != 3 || fs.QuarantineLen() != 3 {
		t.Fatalf("retry must move all 3 names, moved=%d qlen=%d",
			len(retryReport.Moved), fs.QuarantineLen())
	}
	if len(fs.Graph().NamesOf(target)) != 0 {
		t.Fatal("no entry may remain after successful retry")
	}

	if err := retryReport.Rollback(fs); err != nil {
		t.Fatalf("rollback after recovery failed: %v", err)
	}
	if fs.GateCount() != 0 {
		t.Fatal("gate must be removed after full rollback")
	}
	for _, name := range []string{"evil", "alias1", "alias2"} {
		if !fs.EntryExists(root, name) || !fs.ExecutableAt(root, name) {
			t.Fatalf("name %q must be restored and executable", name)
		}
	}
}

func TestPartialFailureRollbackRetainsGate(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")
	fs.Link(target, root, "alias1")
	fs.Link(target, root, "alias2")
	fs.FailNextMoves = 1
	fs.MoveErr = errors.New("transient move error")

	report, err := Quarantine(fs, fs.Graph(), target)
	if err != nil || !report.Complete() {
		t.Fatalf("one transient failure should still converge, err=%v report=%+v", err, report)
	}

	// 占用一个恢复目标位置：Rollback 必须保留门禁，并把未恢复条目留在清单中。
	first := report.Moved[0]
	blocking := fs.Create(first.Parent, first.Name)

	if err := report.Rollback(fs); err == nil {
		t.Fatal("rollback must fail when a destination name is occupied")
	}
	if !fs.GateInstalled(target) {
		t.Fatal("gate must remain active after failed rollback")
	}
	if len(report.Moved) == 0 {
		t.Fatal("unrecovered records must remain on the recovery list")
	}
	if !fs.ExecAllowed(blocking) {
		t.Fatal("unrelated blocking object must not be affected by the gate")
	}

	// 清出占用位置后再次回滚应成功并撤销门禁。
	fs.Unlink(first.Parent, first.Name)
	if err := report.Rollback(fs); err != nil {
		t.Fatalf("second rollback attempt should succeed: %v", err)
	}
	if fs.GateCount() != 0 {
		t.Fatal("gate must be removed once every entry is restored")
	}
}

func TestConcurrentChurnNoExecutableEntryWhileQuarantined(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")
	fs.Link(target, root, "alias1")

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 搅动协程：不断重命名、增加与删除指向目标代次的硬链接。
	wg.Add(1)
	go func() {
		defer wg.Done()
		round := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			round++
			func() {
				defer func() { _ = recover() }()
				slot := fmt.Sprintf("link-%d", round%4)
				switch {
				case !fs.EntryExists(root, slot):
					fs.Link(target, root, slot)
				case round%2 == 0:
					fs.Rename(root, slot, root, fmt.Sprintf("renamed-%d", round%4))
				default:
					fs.Unlink(root, slot)
				}
			}()
		}
	}()

	// 检查协程：门禁生效期间，任何时刻指向目标代次的名字都不可执行。
	var violationMu sync.Mutex
	var violations []string
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if !fs.GateInstalled(target) {
				continue
			}
			for _, entry := range fs.Graph().NamesOf(target) {
				if fs.ExecutableAt(entry.Parent, entry.Name) {
					violationMu.Lock()
					violations = append(violations,
						fmt.Sprintf("object %d executable at (parent=%d,name=%s)",
							entry.Object.ID, entry.Parent, entry.Name))
					violationMu.Unlock()
				}
			}
		}
	}()

	// 搅动期间反复尝试隔离，直到收敛或达到尝试上限。
	var report *QuarantineReport
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		report, err = Quarantine(fs, fs.Graph(), target)
		if report.Complete() {
			break
		}
		report, err = report.Retry(fs)
		if report.Complete() {
			break
		}
	}
	close(stop)
	wg.Wait()

	// 搅动停止后做最终收敛，处理最后时刻新建的硬链接。
	report, err = report.Retry(fs)

	if err != nil || !report.Complete() {
		t.Fatalf("quarantine must eventually converge under churn: err=%v report=%+v", err, report)
	}
	if len(fs.Graph().NamesOf(target)) != 0 {
		t.Fatal("no name may reference the target generation after convergence")
	}
	violationMu.Lock()
	found := append([]string(nil), violations...)
	violationMu.Unlock()
	if len(found) != 0 {
		t.Fatalf("executable entries observed while gate was active: %v", found)
	}
	if fs.ExecAllowed(target) {
		t.Fatal("gate must still deny the quarantined object generation")
	}
}

func TestRetryReinstallsGateWhenMissing(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")
	fs.FailNextMoves = maxQuarantineRounds * 4
	fs.MoveErr = errors.New("quarantine volume offline")

	report, err := Quarantine(fs, fs.Graph(), target)
	if !errors.Is(err, ErrQuarantineIncomplete) {
		t.Fatalf("expected incomplete quarantine, got %v", err)
	}

	// 模拟宿主在故障窗口内丢失门禁状态：Retry 必须先补装门禁再移动。
	if err := fs.RemoveGate(target); err != nil {
		t.Fatalf("remove gate: %v", err)
	}
	fs.FailNextMoves = 0
	retryReport, err := report.Retry(fs)
	if err != nil || !retryReport.Complete() {
		t.Fatalf("retry should reinstall gate and complete: err=%v report=%+v", err, retryReport)
	}
	if !fs.GateInstalled(target) {
		t.Fatal("retry must leave the gate installed")
	}
	assertBlockedEverywhere(t, fs, target)
}

func TestModelMisusePanicsAreExplicit(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")

	cases := []struct {
		name string
		call func()
	}{
		{"duplicate create", func() { fs.Create(root, "evil") }},
		{"link into missing parent", func() { fs.Link(target, 999, "x") }},
		{"rename to occupied name", func() {
			fs.Create(root, "other")
			fs.Rename(root, "evil", root, "other")
		}},
		{"unlink missing entry", func() { fs.Unlink(root, "ghost") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected explicit panic on model misuse")
				}
			}()
			tc.call()
		})
	}
}

func TestRestoreRejectsUnknownKeyAndCollision(t *testing.T) {
	fs := NewModelFS()
	root := fs.Root()
	target := fs.Create(root, "evil")

	report, err := Quarantine(fs, fs.Graph(), target)
	if err != nil {
		t.Fatalf("quarantine failed: %v", err)
	}
	if err := fs.RestoreFromQuarantine("missing-key", root, "evil"); !errors.Is(err, ErrNameNotFound) {
		t.Fatalf("unknown key must report ErrNameNotFound, got %v", err)
	}

	fs.Create(root, "evil")
	if err := report.Rollback(fs); err == nil {
		t.Fatal("rollback must reject restoring over an occupied name")
	}
	if !fs.GateInstalled(target) {
		t.Fatal("gate must remain installed after rejected rollback")
	}
}
