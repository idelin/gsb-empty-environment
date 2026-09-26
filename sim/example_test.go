package sim_test

import (
	"fmt"

	"quotacredit/sim"
)

// Example is a deterministic end-to-end run: the first transfer copy is
// lost, its retry is duplicated by the network. The unique transfer id makes
// credit land exactly once and total availability dips only while the funds
// are in the deducted-but-not-received window.
func Example() {
	first := sim.FaultKey(sim.Message{Kind: sim.MsgTransfer, ID: sim.TransferID{From: 1, Seq: 1}, Sender: 1, Target: 2, SentAt: 0})
	retry := sim.FaultKey(sim.Message{Kind: sim.MsgTransfer, ID: sim.TransferID{From: 1, Seq: 1}, Sender: 1, Target: 2, SentAt: 20})
	s := sim.NewSimulator(sim.ScriptedFaults{
		MinD:      1,
		ForceDrop: map[string]bool{first: true},
		ForceDup:  map[string]bool{retry: true},
	})
	a := s.AddReplica(1, 100)
	b := s.AddReplica(2, 0)
	a.ResendInterval = 20
	if _, err := a.Transfer(2, 30); err != nil {
		fmt.Println("error:", err)
	}
	s.At(10, func(int64) {
		snap := s.SnapshotNow()
		fmt.Println("available while in flight:", snap.TotalAvailable())
	})
	s.Run()
	if err := s.Audit(); err != nil {
		fmt.Println("audit error:", err)
	}
	fmt.Println("a:", a.Balance(), "b:", b.Balance())
	fmt.Println("dup transfers rejected:", s.Stats().TransferDups)
	fmt.Println("pending:", a.PendingCount())
	// Output:
	// available while in flight: 70
	// a: 70 b: 30
	// dup transfers rejected: 1
	// pending: 0
}
