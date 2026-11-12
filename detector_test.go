package netsec

import (
	"context"
	"strings"
	"testing"
	"time"
)

const (
	testSandboxID   = "sandbox-1"
	testNSInode     = uint64(4026532345)
	testHostNSInode = uint64(4026531835)
	testSandboxRoot = "/sys/fs/cgroup/kubepods/podabc/sandbox-1"
	testSeqStart    = uint64(100)
)

func newTestDetector(t *testing.T) (*Detector, *[]Alert) {
	t.Helper()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	var alerts []Alert
	d := NewDetector(Config{
		Sandboxes: []SandboxConfig{{
			ID:             testSandboxID,
			CgroupNSInode:  testNSInode,
			HostCgroupRoot: testSandboxRoot,
		}},
		Block: func(a Alert) { alerts = append(alerts, a) },
		Now:   clock,
	})
	return d, &alerts
}

func sandboxEvent(seq uint64, sys Syscall) *AuditEvent {
	return &AuditEvent{
		Time:          time.Date(2026, 11, 12, 10, 0, int(seq)%60, 0, time.UTC),
		Seq:           seq,
		PID:           9001,
		PPID:          9000,
		Comm:          "evil",
		CgroupNSInode: testNSInode,
		HostCgroup:    testSandboxRoot,
		Syscall:       sys,
	}
}

// TestReleaseAgentWrite 验证 v1 release_agent 写入与 notify_on_release
// 打开的组合手法能被关联出来，并带上完整事件链。
func TestReleaseAgentWrite(t *testing.T) {
	d, alerts := newTestDetector(t)

	mount := sandboxEvent(testSeqStart, SysMount)
	mount.MountPoint = "/sys/fs/cgroup/cpu"
	mount.MountRoot = "/sys/fs/cgroup/cpu"
	mount.FSType = "cgroup"
	mount.Writable = true

	openNotify := sandboxEvent(testSeqStart+1, SysOpenat)
	openNotify.Path = "/sys/fs/cgroup/cpu/notify_on_release"
	openNotify.ResolvedHostPath = "/sys/fs/cgroup/cpu/notify_on_release"
	openNotify.Writable = true

	writeAgent := sandboxEvent(testSeqStart+2, SysWrite)
	writeAgent.Path = "/sys/fs/cgroup/cpu/release_agent"
	writeAgent.ResolvedHostPath = "/sys/fs/cgroup/cpu/release_agent"
	writeAgent.Payload = "/tmp/pwn.sh"

	for _, ev := range []*AuditEvent{mount, openNotify, writeAgent} {
		d.ProcessEvent(ev)
	}

	if got := len(*alerts); got != 3 {
		t.Fatalf("expected 3 alerts (writable mount + notify + release_agent), got %d", got)
	}
	wantRules := []EscapeRule{
		RuleWritableCgroupMount,
		RuleNotifyOnRelease,
		RuleReleaseAgentWrite,
	}
	for i, want := range wantRules {
		if (*alerts)[i].Rule != want {
			t.Fatalf("alert %d = %s, want %s", i, (*alerts)[i].Rule, want)
		}
	}
	last := (*alerts)[2]
	if last.SandboxID != testSandboxID {
		t.Fatalf("sandbox id = %q", last.SandboxID)
	}
	if last.Trigger != writeAgent {
		t.Fatal("trigger should be the release_agent write event")
	}
	if len(last.Chain) != 3 {
		t.Fatalf("chain length = %d, want 3", len(last.Chain))
	}
	if last.Chain[0] != mount || last.Chain[2] != writeAgent {
		t.Fatal("event chain order mismatch")
	}
}

// TestWritableCgroupMount 验证沙箱内可写 cgroup 挂载触发，只读挂载放行。
func TestWritableCgroupMount(t *testing.T) {
	d, alerts := newTestDetector(t)

	readonly := sandboxEvent(testSeqStart, SysMount)
	readonly.MountPoint = "/tmp/cg-ro"
	readonly.FSType = "cgroup2"
	readonly.Writable = false

	writable := sandboxEvent(testSeqStart+1, SysMount)
	writable.MountPoint = "/tmp/cg-rw"
	writable.FSType = "cgroup2"
	writable.Writable = true

	otherFS := sandboxEvent(testSeqStart+2, SysMount)
	otherFS.MountPoint = "/tmp/proc"
	otherFS.FSType = "proc"
	otherFS.Writable = true

	for _, ev := range []*AuditEvent{readonly, writable, otherFS} {
		d.ProcessEvent(ev)
	}

	if got := len(*alerts); got != 1 {
		t.Fatalf("expected 1 alert, got %d", got)
	}
	if (*alerts)[0].Rule != RuleWritableCgroupMount {
		t.Fatalf("rule = %s", (*alerts)[0].Rule)
	}
	if (*alerts)[0].Reason == "" {
		t.Fatal("alert reason should not be empty")
	}
}

// TestMigrateToParent 验证把任务写入上级 cgroup.procs 触发，
// 写回沙箱内部子树属于合法 v2 子树管理，放行。
func TestMigrateToParent(t *testing.T) {
	d, alerts := newTestDetector(t)

	// 合法：沙箱内部再切一个子 cgroup 并把自己迁进去。
	internalWrite := sandboxEvent(testSeqStart, SysWrite)
	internalWrite.Path = testSandboxRoot + "/child/cgroup.procs"
	internalWrite.ResolvedHostPath = testSandboxRoot + "/child/cgroup.procs"
	internalWrite.Payload = "9001"

	// 逃逸：写到沙箱根之外的上级 cgroup.procs。
	parentWrite := sandboxEvent(testSeqStart+1, SysWrite)
	parentWrite.Path = "/sys/fs/cgroup/kubepods/podabc/cgroup.procs"
	parentWrite.ResolvedHostPath = "/sys/fs/cgroup/kubepods/podabc/cgroup.procs"
	parentWrite.Payload = "9001"

	// 合成迁移事件（探针直接给出迁移语义）同样应触发。
	migrate := sandboxEvent(testSeqStart+2, SysMigrate)
	migrate.Path = "/sys/fs/cgroup/kubepods/cgroup.procs"
	migrate.ResolvedHostPath = "/sys/fs/cgroup/kubepods/cgroup.procs"

	for _, ev := range []*AuditEvent{internalWrite, parentWrite, migrate} {
		d.ProcessEvent(ev)
	}

	if got := len(*alerts); got != 2 {
		t.Fatalf("expected 2 alerts, got %d", got)
	}
	for i, want := range []EscapeRule{RuleMigrateToParent, RuleMigrateToParent} {
		if (*alerts)[i].Rule != want {
			t.Fatalf("alert %d rule = %s, want %s", i, (*alerts)[i].Rule, want)
		}
	}

	// 内部迁移事件应出现在第二次告警的事件链中。
	chain := (*alerts)[1].Chain
	found := false
	for _, ev := range chain {
		if ev == internalWrite {
			found = true
		}
	}
	if !found {
		t.Fatal("internal write event missing from chain")
	}
}

// TestNormalScheduling 验证正常调度与合法宿主管理不会误报。
func TestNormalScheduling(t *testing.T) {
	d, alerts := newTestDetector(t)
	d.AllowManager(testHostNSInode, "systemd")

	// 1. 宿主管理器在初始 namespace 内挂载 cgroup2。
	hostMount := sandboxEvent(testSeqStart, SysMount)
	hostMount.PID = 1
	hostMount.Comm = "systemd"
	hostMount.CgroupNSInode = testHostNSInode
	hostMount.HostCgroup = "/"
	hostMount.MountPoint = "/sys/fs/cgroup"
	hostMount.FSType = "cgroup2"
	hostMount.Writable = true

	// 2. 宿主管理器做 v2 子树管理：写沙箱上级的 cgroup.procs。
	hostManage := sandboxEvent(testSeqStart+1, SysWrite)
	hostManage.PID = 1
	hostManage.Comm = "systemd"
	hostManage.CgroupNSInode = testHostNSInode
	hostManage.HostCgroup = "/"
	hostManage.Path = "/sys/fs/cgroup/kubepods/podabc/cgroup.procs"
	hostManage.ResolvedHostPath = "/sys/fs/cgroup/kubepods/podabc/cgroup.procs"
	hostManage.Payload = "9001"

	// 3. 沙箱内部正常调度：写自己的 cgroup.procs（调度到子任务）。
	sched := sandboxEvent(testSeqStart+2, SysWrite)
	sched.Path = testSandboxRoot + "/tasks/cgroup.procs"
	sched.ResolvedHostPath = testSandboxRoot + "/tasks/cgroup.procs"
	sched.Payload = "9002"

	// 4. 沙箱内对 notify_on_release 的只读访问。
	readOnly := sandboxEvent(testSeqStart+3, SysOpenat)
	readOnly.Path = testSandboxRoot + "/notify_on_release"
	readOnly.ResolvedHostPath = testSandboxRoot + "/notify_on_release"
	readOnly.Writable = false

	// 5. 普通业务写（非 cgroup 文件）。
	businessWrite := sandboxEvent(testSeqStart+4, SysWrite)
	businessWrite.Path = "/var/log/app.log"
	businessWrite.ResolvedHostPath = "/var/log/app.log"
	businessWrite.Payload = "ok"

	for _, ev := range []*AuditEvent{hostMount, hostManage, sched, readOnly, businessWrite} {
		d.ProcessEvent(ev)
	}

	if got := len(*alerts); got != 0 {
		t.Fatalf("expected no alerts for legitimate activity, got %d: %+v", got, *alerts)
	}
}

// TestNamespaceBoundary 验证 namespace 内外判定：宿主 namespace 中
// 未登记的进程不按沙箱规则告警，而 namespace inode 是首要归属依据。
func TestNamespaceBoundary(t *testing.T) {
	d, alerts := newTestDetector(t)

	// 宿主 namespace、cgroup 路径恰好落在沙箱根之外的进程：
	// 不属于任何沙箱，直接忽略。
	hostProc := sandboxEvent(testSeqStart, SysMount)
	hostProc.CgroupNSInode = testHostNSInode
	hostProc.HostCgroup = "/user.slice"
	hostProc.MountPoint = "/tmp/cg"
	hostProc.FSType = "cgroup2"
	hostProc.Writable = true
	d.ProcessEvent(hostProc)

	if got := len(*alerts); got != 0 {
		t.Fatalf("host-namespace process must not be evaluated, got %d alerts", got)
	}

	// 事件没有 namespace 信息时，按宿主 cgroup 路径兜底归属。
	ev := sandboxEvent(testSeqStart+1, SysMount)
	ev.CgroupNSInode = 0
	ev.HostCgroup = testSandboxRoot + "/worker"
	ev.MountPoint = "/tmp/cg2"
	ev.FSType = "cgroup2"
	ev.Writable = true
	d.ProcessEvent(ev)

	if got := len(*alerts); got != 1 {
		t.Fatalf("path-based fallback should attribute event to sandbox, got %d alerts", got)
	}
}

// TestMountTableResolution 验证未提供预解析宿主路径时，
// 检测器依据内部挂载表把进程视角路径映射到宿主路径并完成判定。
func TestMountTableResolution(t *testing.T) {
	d, alerts := newTestDetector(t)

	// 沙箱内挂载 cgroup2：挂载点在进程视角为 /tmp/cg，
	// 对应宿主上的沙箱 cgroup 根。
	mount := sandboxEvent(testSeqStart, SysMount)
	mount.MountPoint = "/tmp/cg"
	mount.MountRoot = "/sys/fs/cgroup/kubepods"
	mount.FSType = "cgroup2"
	mount.Writable = true

	// 只给进程视角路径：/tmp/cg/podabc/cgroup.procs 经挂载表映射到
	// 宿主上的 /sys/fs/cgroup/kubepods/podabc/cgroup.procs，即沙箱根之外。
	migrate := sandboxEvent(testSeqStart+1, SysWrite)
	migrate.Path = "/tmp/cg/podabc/cgroup.procs"
	migrate.Payload = "9001"

	d.ProcessEvent(mount)
	d.ProcessEvent(migrate)

	if got := len(*alerts); got != 2 {
		t.Fatalf("expected mount + migration alerts, got %d", got)
	}
	if (*alerts)[0].Rule != RuleWritableCgroupMount {
		t.Fatalf("first rule = %s", (*alerts)[0].Rule)
	}
	if (*alerts)[1].Rule != RuleMigrateToParent {
		t.Fatalf("second rule = %s", (*alerts)[1].Rule)
	}
	if !strings.Contains((*alerts)[1].Reason, "/sys/fs/cgroup/kubepods/podabc") {
		t.Fatalf("reason should contain host-resolved path, got %q", (*alerts)[1].Reason)
	}

	// 解析后落在沙箱内部（podabc/sandbox-1 子树）的写入应放行。
	inside := sandboxEvent(testSeqStart+2, SysWrite)
	inside.Path = "/tmp/cg/podabc/sandbox-1/worker/cgroup.procs"
	inside.Payload = "9003"
	d.ProcessEvent(inside)
	if got := len(*alerts); got != 2 {
		t.Fatalf("in-sandbox subtree write must be allowed, got %d alerts", got)
	}
}

// TestRunFromChannelSource 验证通过可订阅事件源运行的完整链路。
func TestRunFromChannelSource(t *testing.T) {
	var alerts []Alert
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	d := NewDetector(Config{
		Sandboxes: []SandboxConfig{{
			ID:             testSandboxID,
			CgroupNSInode:  testNSInode,
			HostCgroupRoot: testSandboxRoot,
		}},
		Block: func(a Alert) { alerts = append(alerts, a) },
		Now:   func() time.Time { return now },
	})
	source := NewChannelSource(4)

	done := make(chan struct{})
	go func() {
		_ = d.Run(context.Background(), source)
		close(done)
	}()

	ev := sandboxEvent(testSeqStart, SysWrite)
	ev.Path = "/sys/fs/cgroup/cpu/release_agent"
	ev.ResolvedHostPath = "/sys/fs/cgroup/cpu/release_agent"
	ev.Payload = "/x"
	if !source.Emit(ev) {
		t.Fatal("emit failed")
	}
	_ = source.Close()
	<-done

	if len(alerts) != 1 || alerts[0].Rule != RuleReleaseAgentWrite {
		t.Fatalf("channel subscription did not produce expected alert: %+v", alerts)
	}
}
