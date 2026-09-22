// 2PC 事件模拟演示：通信、故障、时间全部在单进程模拟器中受控。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"twopc"
)

type cluster struct {
	sim   *twopc.Sim
	coord *twopc.Coordinator
	parts []*twopc.Participant
	ids   []string
}

func newCluster(n int) *cluster {
	dir, err := os.MkdirTemp("", "twopc-demo")
	if err != nil {
		panic(err)
	}
	s := twopc.NewSim()
	cw, err := twopc.OpenWAL(filepath.Join(dir, "coord.wal"))
	if err != nil {
		panic(err)
	}
	c := &cluster{sim: s, coord: twopc.NewCoordinator("coord", cw, s.Send)}
	s.AddCoordinator(c.coord)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%d", i)
		pw, err := twopc.OpenWAL(filepath.Join(dir, id+".wal"))
		if err != nil {
			panic(err)
		}
		p := twopc.NewParticipant(id, pw, s.Send)
		s.AddParticipant(p)
		c.parts = append(c.parts, p)
		c.ids = append(c.ids, id)
	}
	return c
}

func (c *cluster) printTrace() {
	for _, line := range c.sim.Trace {
		fmt.Println("   ", line)
	}
}

func (c *cluster) status(tx string) {
	fmt.Printf("    coord decision=%q", decisionOf(c.coord, tx))
	for _, p := range c.parts {
		fmt.Printf("  %s=%s(lock=%q)", p.ID, orDash(p.Txs[tx], "-"), p.LockedBy)
	}
	fmt.Println()
}

func decisionOf(c *twopc.Coordinator, tx string) string {
	for _, r := range c.WAL.Records() {
		if r.Kind == "CDecision" && r.TxID == tx {
			return r.Decision
		}
	}
	return "-"
}

func orDash(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func main() {
	fmt.Println("== 场景 A：无故障，全体 YES -> COMMIT（活性） ==")
	a := newCluster(3)
	a.coord.Start("txA", a.ids)
	a.sim.RunUntilIdle()
	a.printTrace()
	a.status("txA")

	fmt.Println("\n== 场景 B：p2 资源被占用 -> 一票否决 -> ABORT（活性） ==")
	b := newCluster(3)
	b.sim.Inject(twopc.Message{From: "coord", To: "p2", Type: "prepare", TxID: "tx-other"})
	b.sim.RunUntilIdle()
	b.coord.Start("txB", b.ids)
	b.sim.RunUntilIdle()
	b.printTrace()
	b.status("txB")

	fmt.Println("\n== 场景 C：收齐 YES 后协调者崩溃（阻塞）-> 重启后按日志只能 ABORT，不能猜提交 ==")
	c := newCluster(3)
	c.coord.HoldDecision = true
	c.coord.Start("txC", c.ids)
	c.sim.RunUntilIdle()
	c.sim.Crash("coord")
	c.sim.RunUntilIdle()
	fmt.Println("    -- 协调者崩溃期间：参与者持锁阻塞，无法推进 --")
	c.status("txC")
	c.sim.Restart("coord")
	c.sim.RunUntilIdle()
	c.printTrace()
	c.status("txC")

	fmt.Println("\n== 场景 D：COMMIT 落盘后、发出前崩溃 -> 重启按日志重放 COMMIT ==")
	d := newCluster(3)
	d.coord.AfterDecisionDurable = func() { d.sim.Crash("coord") }
	d.coord.Start("txD", d.ids)
	d.sim.RunUntilIdle()
	fmt.Println("    -- COMMIT 已持久化但未送达：参与者仍阻塞持锁 --")
	d.status("txD")
	d.sim.Restart("coord")
	d.sim.RunUntilIdle()
	d.printTrace()
	d.status("txD")

	fmt.Println("\n== 场景 E：重复 commit 幂等 + abort 后迟到 prepare 被拒 ==")
	e := newCluster(2)
	e.coord.Start("txE", e.ids)
	e.sim.RunUntilIdle()
	e.sim.Inject(twopc.Message{From: "coord", To: "p0", Type: "commit", TxID: "txE"}) // 重复 commit
	e.sim.Inject(twopc.Message{From: "coord", To: "p0", Type: "abort", TxID: "txGone"})
	e.sim.Inject(twopc.Message{From: "coord", To: "p0", Type: "prepare", TxID: "txGone"}) // abort 后迟到 prepare
	e.sim.RunUntilIdle()
	e.printTrace()
	fmt.Printf("    p0 applied txE %d 次（幂等，应为 1）；txGone 状态=%s lock=%q（应为 aborted，不占锁）\n",
		count(e.parts[0], "txE"), e.parts[0].Txs["txGone"], e.parts[0].LockedBy)
}

func count(p *twopc.Participant, tx string) int {
	n := 0
	for _, a := range p.Applied {
		if a == tx {
			n++
		}
	}
	return n
}
