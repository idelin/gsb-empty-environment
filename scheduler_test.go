package sim

import (
	"math"
	"testing"
)

// 场景一：权重 1:2:3 的三个租户长期 backlog，服务量比例应收敛到权重比。
func TestFairShares(t *testing.T) {
	s := New(2000, 1, 64)
	s.AddTenant("a", 1, 64, 0)
	s.AddTenant("b", 2, 64, 0)
	s.AddTenant("c", 3, 64, 0)
	for i := 0; i < 2100; i++ {
		s.Submit("a", 1, 1, 0)
	}
	for i := 0; i < 4100; i++ {
		s.Submit("b", 1, 1, 0)
	}
	for i := 0; i < 6100; i++ {
		s.Submit("c", 1, 1, 0)
	}
	s.Run()

	if len(s.Violations) > 0 {
		t.Fatalf("invariant violations: %v", s.Violations[:3])
	}
	sa, sb, sc := s.Tenant("a").Served, s.Tenant("b").Served, s.Tenant("c").Served
	t.Logf("served a=%.0f b=%.0f c=%.0f (expect ~1:2:3)", sa, sb, sc)
	if math.Abs(sb/sa-2) > 0.02 || math.Abs(sc/sa-3) > 0.03 {
		t.Errorf("service ratio %f:%f:%f not close to 1:2:3", sa/sa, sb/sa, sc/sa)
	}
	if sa < 1900 {
		t.Errorf("tenant a served %.0f, expected ~2000 (one weight-unit per round)", sa)
	}
}

// 场景二：巨成本作业靠逐轮积攒额度最终启动，期间不阻塞其他租户；
// 空租户不积攒额度；并发上限不被突破。
func TestGiantJob(t *testing.T) {
	s := New(1000, 1, 10)
	s.AddTenant("big", 1, 5, 0)
	s.AddTenant("small", 1, 10, 0)
	s.AddTenant("idle", 5, 10, 0) // 从不提交作业
	s.Submit("big", 300, 5, 0)    // 巨成本作业：需 300 轮积攒
	for i := 0; i < 400; i++ {
		s.Submit("small", 1, 1, 0)
	}

	s.Run()

	if len(s.Violations) > 0 {
		t.Fatalf("invariant violations: %v", s.Violations[:3])
	}
	big, small, idle := s.Tenant("big"), s.Tenant("small"), s.Tenant("idle")

	if big.Served != 300 {
		t.Errorf("big served %.0f, expected 300 (giant job completed)", big.Served)
	}
	if small.Served != 400 {
		t.Errorf("small served %.0f, expected all 400 jobs done (not starved by giant)", small.Served)
	}
	if idle.Credit != 0 {
		t.Errorf("idle tenant accumulated credit %.1f, expected 0 (empty tenants gain nothing)", idle.Credit)
	}
	if s.PeakGlobal > 10 {
		t.Errorf("peak global concurrency %d exceeded limit 10", s.PeakGlobal)
	}
	if big.PeakRunning > 5 || small.PeakRunning > 10 {
		t.Errorf("per-tenant concurrency exceeded: big=%d small=%d", big.PeakRunning, small.PeakRunning)
	}
	t.Logf("big served=%.0f small served=%.0f idle credit=%.1f peakGlobal=%d",
		big.Served, small.Served, idle.Credit, s.PeakGlobal)
}

// 场景三：租户退出时额度清零、移出轮转、丢弃待运行作业；
// 运行中的作业继续持有槽位直到完成；重新加入后从零开始。
func TestExitClearsCredit(t *testing.T) {
	s := New(600, 1, 8)
	s.AddTenant("buddy", 1, 4, 0)
	s.AddTenant("vip", 3, 4, 0)
	for i := 0; i < 1000; i++ {
		s.Submit("buddy", 1, 1, 0)
	}
	s.Submit("vip", 3, 500, 0)   // 长作业，跨越退出时刻，t=500 才归还槽位
	s.Submit("vip", 1e9, 1, 0.5) // 永远付不起的巨作业，让 vip 持续积攒额度
	s.ExitTenant("vip", 100)
	s.RejoinTenant("vip", 200)
	for i := 0; i < 50; i++ {
		s.Submit("vip", 1, 1, 200.5)
	}

	var creditAtExit, creditAtJoin float64
	var runningAtExit int
	var inRotationAtExit = true
	vip := s.Tenant("vip")
	s.Hook = func(sim *Sim, kind string, tn *Tenant) {
		if tn != vip {
			return
		}
		switch kind {
		case "exit":
			creditAtExit = vip.Credit
			runningAtExit = vip.Running
			inRotationAtExit = sim.InRotation(vip)
		case "join":
			creditAtJoin = vip.Credit
		}
	}
	s.Run()

	if len(s.Violations) > 0 {
		t.Fatalf("invariant violations: %v", s.Violations[:3])
	}
	if creditAtExit != 0 {
		t.Errorf("credit at exit = %.1f, expected 0 (cleared on exit)", creditAtExit)
	}
	if inRotationAtExit {
		t.Errorf("vip still in rotation after exit")
	}
	if runningAtExit != 1 {
		t.Errorf("running jobs at exit = %d, expected 1 (long job keeps its slot)", runningAtExit)
	}
	if creditAtJoin != 0 {
		t.Errorf("credit at rejoin = %.1f, expected 0 (fresh start)", creditAtJoin)
	}
	// 长作业 3 + 重新加入后的 50 个小作业；退出前的巨作业被丢弃
	if vip.Served != 53 {
		t.Errorf("vip served %.0f, expected 53 (3 from long job + 50 after rejoin)", vip.Served)
	}
	if len(vip.Queue) != 0 {
		t.Errorf("vip queue not empty at end: %d jobs left", len(vip.Queue))
	}
	buddy := s.Tenant("buddy")
	if buddy.Served < 590 {
		t.Errorf("buddy served %.0f, expected ~600 (unaffected by vip exit/join)", buddy.Served)
	}
	t.Logf("vip served=%.0f credit@exit=%.1f credit@join=%.1f buddy served=%.0f",
		vip.Served, creditAtExit, creditAtJoin, buddy.Served)
}
