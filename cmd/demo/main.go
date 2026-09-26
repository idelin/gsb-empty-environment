// Command demo runs a reproducible scenario through the single-process
// event simulator: quota partition, offline spends, lossy and duplicating
// network, retransmission, and crash/restart recovery.
package main

import (
	"fmt"
	"math/rand"
	"os"

	"quotacredit/sim"
)

func main() {
	rng := rand.New(rand.NewSource(20260926))
	faults := sim.ScriptedFaults{
		Rand:          rng,
		MinD:          1,
		MaxD:          4,
		DropProb:      0.15,
		DuplicateProb: 0.10,
		ForceDrop: map[string]bool{
			// First transfer copy is lost: sender must retry with the same id.
			sim.FaultKey(sim.Message{Kind: sim.MsgTransfer, ID: sim.TransferID{From: 1, Seq: 1}, Sender: 1, Target: 2, SentAt: 5}): true,
			// Ack for the R2->R3 transfer is lost once; duplicate ack on retry
			// is rejected by the durable dedup key.
			sim.FaultKey(sim.Message{Kind: sim.MsgAck, ID: sim.TransferID{From: 2, Seq: 1}, Sender: 3, Target: 2, SentAt: 41}): true,
		},
		ForceDup: map[string]bool{
			// A duplicated retry copy of R1->R2 must not double-credit.
			sim.FaultKey(sim.Message{Kind: sim.MsgTransfer, ID: sim.TransferID{From: 1, Seq: 1}, Sender: 1, Target: 2, SentAt: 30}): true,
		},
	}
	s := sim.NewSimulator(faults)
	s.SetTrace(func(line string) { fmt.Println(line) })

	r1 := s.AddReplica(1, 100)
	r2 := s.AddReplica(2, 70)
	r3 := s.AddReplica(3, 30) // Q = 200
	for _, r := range []*sim.Replica{r1, r2, r3} {
		r.ResendInterval = 25
	}

	// Workload and controlled faults on the virtual clock.
	s.At(3, func(int64) {
		if err := r1.Spend(10); err != nil {
			fmt.Println("spend failed:", err)
		}
	})
	s.At(5, func(int64) {
		if _, err := r1.Transfer(2, 20); err != nil {
			fmt.Println("transfer failed:", err)
		}
	})
	s.At(13, func(int64) {
		snap := s.SnapshotNow()
		fmt.Printf("t=%-4d [audit] while first copy is lost: available=%d (R1 escrow 20 reduces the total)\n",
			s.Now(), snap.TotalAvailable())
	})
	s.At(35, func(int64) {
		if err := r2.Spend(15); err != nil {
			fmt.Println("spend failed:", err)
		}
	})
	s.At(40, func(int64) {
		if _, err := r2.Transfer(3, 10); err != nil {
			fmt.Println("transfer failed:", err)
		}
	})
	s.At(60, func(int64) { s.Crash(3) }) // durable credit of 10 must survive
	s.At(75, func(int64) { s.Restart(3) })
	s.At(90, func(int64) {
		// R2 cannot overdraw: it holds 70 + 20 received - 15 spent - 10 sent
		// = 65, so 66 must be rejected.
		if err := r2.Spend(66); err != sim.ErrOverdraft {
			fmt.Println("overdraft guard did not trigger, err:", err)
		}
	})

	s.Run()

	fmt.Println("------------------------------------------------------------")
	if err := s.Audit(); err != nil {
		fmt.Println("SAFETY CHECK FAILED:", err)
		os.Exit(1)
	}
	snap := s.SnapshotNow()
	for _, v := range snap.Replicas {
		fmt.Printf("R%d: balance=%-3d spent=%-3d inflight=%d\n", v.ID, v.Balance, v.Spent, v.InFlight)
	}
	st := s.Stats()
	fmt.Printf("quota=%d  available=%d  spent=%d (available+spent=%d)\n",
		snap.Quota, snap.TotalAvailable(), snap.Spent, snap.TotalAvailable()+snap.Spent)
	fmt.Printf("network: sent=%d delivered=%d dropped=%d duplicate-copies=%d\n",
		st.Sent, st.Delivered, st.Dropped, st.Duplicates)
	fmt.Printf("app dedup: duplicate transfers rejected=%d, duplicate acks rejected=%d\n",
		st.TransferDups, st.AckDups)

	ok := r1.Balance() == 70 && r2.Balance() == 65 && r3.Balance() == 40 &&
		r1.PendingCount() == 0 && r2.PendingCount() == 0 && r3.PendingCount() == 0 &&
		st.TransferDups >= 1 && snap.TotalAvailable()+snap.Spent == snap.Quota
	if !ok {
		fmt.Println("RESULT: unexpected final state")
		os.Exit(1)
	}
	fmt.Println("SAFETY: invariants hold; liveness: every transfer completed; exactly-once verified")
}
