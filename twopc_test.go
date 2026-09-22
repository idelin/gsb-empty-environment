package twopc

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
)

func newCluster(t *testing.T, nParts int) (*Sim, *Coordinator, []*Participant) {
	t.Helper()
	dir := t.TempDir()
	s := NewSim()
	cw, err := OpenWAL(filepath.Join(dir, "coord.wal"))
	if err != nil {
		t.Fatal(err)
	}
	coord := NewCoordinator("coord", cw, s.Send)
	s.AddCoordinator(coord)
	var parts []*Participant
	for i := 0; i < nParts; i++ {
		id := fmt.Sprintf("p%d", i)
		pw, err := OpenWAL(filepath.Join(dir, id+".wal"))
		if err != nil {
			t.Fatal(err)
		}
		p := NewParticipant(id, pw, s.Send)
		s.AddParticipant(p)
		parts = append(parts, p)
	}
	return s, coord, parts
}

func partIDs(parts []*Participant) []string {
	ids := make([]string, len(parts))
	for i, p := range parts {
		ids[i] = p.ID
	}
	return ids
}

func coordDecision(c *Coordinator, tx string) string {
	for _, r := range c.WAL.Records() {
		if r.Kind == RDecision && r.TxID == tx {
			return r.Decision
		}
	}
	return ""
}

func countApplied(p *Participant, tx string) int {
	n := 0
	for _, a := range p.Applied {
		if a == tx {
			n++
		}
	}
	return n
}

func assertNoViolations(t *testing.T, s *Sim) {
	t.Helper()
	for _, p := range s.Parts {
		if len(p.Violations) > 0 {
			t.Fatalf("participant %s safety violations: %v", p.ID, p.Violations)
		}
	}
}

// ---------- 活性证据：无故障时提交路径终止 ----------

func TestCommitNoFaults(t *testing.T) {
	s, coord, parts := newCluster(t, 3)
	coord.Start("tx1", partIDs(parts))
	s.RunUntilIdle()

	if got := coordDecision(coord, "tx1"); got != "COMMIT" {
		t.Fatalf("decision = %q, want COMMIT", got)
	}
	for _, p := range parts {
		if p.Txs["tx1"] != "committed" || p.LockedBy != "" || countApplied(p, "tx1") != 1 {
			t.Fatalf("%s: state=%q lockedBy=%q applied=%d", p.ID, p.Txs["tx1"], p.LockedBy, countApplied(p, "tx1"))
		}
	}
	if s.Pending() != 0 {
		t.Fatalf("system not quiescent, %d pending events", s.Pending())
	}
	assertNoViolations(t, s)
}

// ---------- 活性证据：一票否决时中止路径终止 ----------

func TestAbortOnNoVote(t *testing.T) {
	s, coord, parts := newCluster(t, 3)
	// p2 的资源先被另一个事务占用 -> 对 tx1 投 NO。
	s.Inject(Message{From: "coord", To: "p2", Type: "prepare", TxID: "tx-other"})
	s.RunUntilIdle()

	coord.Start("tx1", partIDs(parts))
	s.RunUntilIdle()

	if got := coordDecision(coord, "tx1"); got != "ABORT" {
		t.Fatalf("decision = %q, want ABORT", got)
	}
	for _, p := range parts[:2] {
		if p.Txs["tx1"] != "aborted" || p.LockedBy != "" {
			t.Fatalf("%s: state=%q lockedBy=%q, want aborted/unlocked", p.ID, p.Txs["tx1"], p.LockedBy)
		}
	}
	if parts[2].LockedBy != "tx-other" {
		t.Fatalf("p2 must keep tx-other lock, got %q", parts[2].LockedBy)
	}
	assertNoViolations(t, s)
}

// ---------- 阻塞情形 1：全 prepare 后协调者崩溃 ----------

func TestBlockingWhenCoordinatorCrashesAfterAllPrepare(t *testing.T) {
	s, coord, parts := newCluster(t, 3)
	coord.HoldDecision = true // 收齐 YES 后、写决策记录前崩溃
	coord.Start("tx1", partIDs(parts))
	s.RunUntilIdle()
	s.Crash("coord")
	s.RunUntilIdle()

	// 阻塞证据：所有参与者持锁等待，系统静止却无法推进，
	// 且参与者不能单方面提交或中止。
	for _, p := range parts {
		if p.Txs["tx1"] != "prepared" || p.LockedBy != "tx1" {
			t.Fatalf("%s should be blocked holding the lock, state=%q lockedBy=%q", p.ID, p.Txs["tx1"], p.LockedBy)
		}
	}
	if s.Pending() != 0 {
		t.Fatalf("expected quiescent-but-blocked system")
	}

	// 恢复：日志中无决策记录，协调者不能"猜"提交，只能补写 ABORT。
	s.Restart("coord")
	s.RunUntilIdle()
	if got := coordDecision(coord, "tx1"); got != "ABORT" {
		t.Fatalf("recovery must not guess commit, decision = %q", got)
	}
	for _, p := range parts {
		if p.Txs["tx1"] != "aborted" || p.LockedBy != "" {
			t.Fatalf("%s: state=%q lockedBy=%q, want aborted/unlocked", p.ID, p.Txs["tx1"], p.LockedBy)
		}
	}
	assertNoViolations(t, s)
}

// ---------- 阻塞情形 2：COMMIT 落盘后、发出前崩溃，恢复按日志重放 ----------

func TestRecoveryReplaysDurableCommit(t *testing.T) {
	s, coord, parts := newCluster(t, 3)
	coord.AfterDecisionDurable = func() { s.Crash("coord") } // COMMIT 已 fsync，消息未发
	coord.Start("tx1", partIDs(parts))
	s.RunUntilIdle()

	if got := coordDecision(coord, "tx1"); got != "COMMIT" {
		t.Fatalf("decision = %q, want COMMIT", got)
	}
	for _, p := range parts { // 决策未送达：参与者仍阻塞持锁
		if p.Txs["tx1"] != "prepared" || p.LockedBy != "tx1" {
			t.Fatalf("%s should be blocked, state=%q lockedBy=%q", p.ID, p.Txs["tx1"], p.LockedBy)
		}
	}

	s.Restart("coord") // 恢复只能按日志重放 COMMIT
	s.RunUntilIdle()
	for _, p := range parts {
		if p.Txs["tx1"] != "committed" || countApplied(p, "tx1") != 1 {
			t.Fatalf("%s: state=%q applied=%d", p.ID, p.Txs["tx1"], countApplied(p, "tx1"))
		}
	}
	assertNoViolations(t, s)
}

// ---------- 阻塞情形 3：参与者 promise 后崩溃，重启后仍持锁等待 ----------

func TestParticipantCrashRecoversPromise(t *testing.T) {
	s, coord, parts := newCluster(t, 3)
	parts[1].AfterPromiseDurable = func() { s.Crash("p1") } // promise 已 fsync，YES 未发出
	coord.Start("tx1", partIDs(parts))
	s.RunUntilIdle()

	if coord.Txs["tx1"].Decision != "" {
		t.Fatalf("coordinator must wait for p1's vote")
	}
	s.Restart("p1")
	// 承诺不丢：锁从 WAL 恢复，参与者仍处于阻塞窗口。
	if parts[1].Txs["tx1"] != "prepared" || parts[1].LockedBy != "tx1" {
		t.Fatalf("p1 promise not recovered: state=%q lockedBy=%q", parts[1].Txs["tx1"], parts[1].LockedBy)
	}
	parts[1].OnTimeout("coord") // 重传 YES，推动完成
	s.RunUntilIdle()
	for _, p := range parts {
		if p.Txs["tx1"] != "committed" {
			t.Fatalf("%s: state=%q, want committed", p.ID, p.Txs["tx1"])
		}
	}
	assertNoViolations(t, s)
}

// ---------- 重复 commit 幂等 ----------

func TestDuplicateCommitIdempotent(t *testing.T) {
	s, coord, parts := newCluster(t, 3)
	coord.Start("tx1", partIDs(parts))
	s.RunUntilIdle()

	s.Inject(Message{From: "coord", To: "p0", Type: "commit", TxID: "tx1"})
	s.Inject(Message{From: "coord", To: "p0", Type: "commit", TxID: "tx1"})
	s.RunUntilIdle()

	if n := countApplied(parts[0], "tx1"); n != 1 {
		t.Fatalf("tx1 applied %d times, want exactly 1", n)
	}
	if parts[0].Txs["tx1"] != "committed" || parts[0].LockedBy != "" {
		t.Fatalf("p0: state=%q lockedBy=%q", parts[0].Txs["tx1"], parts[0].LockedBy)
	}
	assertNoViolations(t, s)
}

// ---------- abort 后迟到 prepare 不能重新占锁 ----------

func TestLatePrepareAfterAbortCannotRelock(t *testing.T) {
	s, coord, parts := newCluster(t, 3)
	s.Inject(Message{From: "coord", To: "p2", Type: "prepare", TxID: "tx-other"})
	s.RunUntilIdle()
	coord.Start("tx1", partIDs(parts)) // p2 否决 -> 全体 abort
	s.RunUntilIdle()
	if parts[0].Txs["tx1"] != "aborted" {
		t.Fatalf("precondition: p0 should be aborted, got %q", parts[0].Txs["tx1"])
	}

	// 网络中滞留的重复 prepare 现在才送达。
	s.Inject(Message{From: "coord", To: "p0", Type: "prepare", TxID: "tx1"})
	s.RunUntilIdle()

	if parts[0].LockedBy == "tx1" || parts[0].Txs["tx1"] != "aborted" {
		t.Fatalf("late prepare re-locked: state=%q lockedBy=%q", parts[0].Txs["tx1"], parts[0].LockedBy)
	}
	assertNoViolations(t, s)
}

// ---------- 安全性证据：随机故障注入下的不变量 ----------

func TestFuzzSafetyUnderFaults(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s, coord, parts := newCluster(t, 3)
			ids := partIDs(parts)

			// 随机预占锁 -> 触发一票否决路径。
			if rng.Intn(2) == 0 {
				victim := ids[rng.Intn(len(ids))]
				s.Inject(Message{From: "coord", To: victim, Type: "prepare", TxID: "tx-other"})
				s.RunUntilIdle()
			}

			switch seed % 4 {
			case 1: // 收齐 YES 后、写决策前崩溃
				coord.HoldDecision = true
				coord.Start("tx1", ids)
				s.RunUntilIdle()
				s.Crash("coord")
			case 2: // 决策落盘后、广播前崩溃
				coord.AfterDecisionDurable = func() { s.Crash("coord") }
				coord.Start("tx1", ids)
				s.RunUntilIdle()
			case 3: // 某参与者 promise 后崩溃
				victim := parts[rng.Intn(len(parts))]
				victim.AfterPromiseDurable = func() { s.Crash(victim.ID) }
				coord.Start("tx1", ids)
				s.RunUntilIdle()
				s.Restart(victim.ID)
				victim.OnTimeout("coord")
			default: // 无故障
				coord.Start("tx1", ids)
				s.RunUntilIdle()
			}

			// 全部重启（幂等），再让未决的 prepared 参与者重传，跑到静止。
			for _, p := range parts {
				s.Restart(p.ID)
			}
			s.Restart("coord")
			for _, p := range parts {
				p.OnTimeout("coord")
			}
			s.RunUntilIdle()

			// 不变量 1：无任何安全性违例。
			assertNoViolations(t, s)
			// 不变量 2：所有参与者对 tx1 的终态一致。
			outcome := ""
			for _, p := range parts {
				st := p.Txs["tx1"]
				if st != "committed" && st != "aborted" {
					t.Fatalf("seed=%d: %s not terminated, state=%q", seed, p.ID, st)
				}
				if outcome == "" {
					outcome = st
				} else if st != outcome {
					t.Fatalf("seed=%d: divergent outcomes %q vs %q", seed, outcome, st)
				}
			}
			// 不变量 3：参与者终态必须与协调者持久决策一致。
			if d := coordDecision(coord, "tx1"); d == "" {
				t.Fatalf("seed=%d: coordinator has no durable decision", seed)
			} else if (d == "COMMIT") != (outcome == "committed") {
				t.Fatalf("seed=%d: decision=%s but participants=%s", seed, d, outcome)
			}
			// 不变量 4：tx1 的锁全部释放，提交至多应用一次。
			for _, p := range parts {
				if p.LockedBy == "tx1" {
					t.Fatalf("seed=%d: %s still holds tx1 lock", seed, p.ID)
				}
				if n := countApplied(p, "tx1"); n > 1 {
					t.Fatalf("seed=%d: %s applied tx1 %d times", seed, p.ID, n)
				}
			}
		})
	}
}
