package chandy

import (
	"math/rand"
	"sync"
	"testing"
	"time"
)

// buildCluster creates n fully connected processes, each holding perNode
// units, and starts their message loops. Returns the processes, a stop
// channel, and a WaitGroup for the loops.
func buildCluster(n, perNode int, maxDelay time.Duration) ([]*Process, chan struct{}, *sync.WaitGroup) {
	procs := make([]*Process, n)
	for i := range procs {
		incoming := []int{}
		for j := 0; j < n; j++ {
			if j != i {
				incoming = append(incoming, j)
			}
		}
		procs[i] = NewProcess(i, perNode, incoming)
	}
	stop := make(chan struct{})
	for i := range procs {
		for j := range procs {
			if i != j {
				procs[i].ConnectDelay(procs[j], maxDelay, stop)
			}
		}
	}
	wg := &sync.WaitGroup{}
	for _, p := range procs {
		wg.Add(1)
		go p.Run(stop, wg)
	}
	return procs, stop, wg
}

// runTraffic launches concurrent random 1-unit transfers until the returned
// channel is closed. Returns the stop channel and a WaitGroup.
func runTraffic(procs []*Process) (chan struct{}, *sync.WaitGroup) {
	n := len(procs)
	stop := make(chan struct{})
	wg := &sync.WaitGroup{}
	for i, p := range procs {
		wg.Add(1)
		go func(idx int, p *Process) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(idx)*7919 + 13))
			for {
				select {
				case <-stop:
					return
				default:
				}
				to := rng.Intn(n)
				if to == p.ID() {
					continue
				}
				p.Transfer(to, 1)
				time.Sleep(time.Duration(rng.Intn(500)) * time.Microsecond)
			}
		}(i, p)
	}
	return stop, wg
}

// collectSnapshots gathers one snapshot result per process with a timeout.
func collectSnapshots(t *testing.T, procs []*Process) []Snapshot {
	t.Helper()
	snaps := make([]Snapshot, len(procs))
	for i, p := range procs {
		select {
		case snaps[i] = <-p.Result():
		case <-time.After(5 * time.Second):
			t.Fatalf("process %d: timed out waiting for snapshot", i)
		}
	}
	return snaps
}

// TestSnapshotConservation is the acceptance test: a 3-node cluster under
// continuous concurrent transfers; node A initiates the snapshot; the sum
// of captured local balances plus in-flight transfers must equal the
// initial total exactly.
func TestSnapshotConservation(t *testing.T) {
	const n, perNode = 3, 100
	for round := 0; round < 20; round++ {
		procs, stop, loops := buildCluster(n, perNode, 2*time.Millisecond)
		trafficStop, traffic := runTraffic(procs)

		time.Sleep(20 * time.Millisecond) // let transfers flow
		procs[0].InitiateSnapshot(1)      // node A initiates
		snaps := collectSnapshots(t, procs)

		close(trafficStop)
		traffic.Wait()
		close(stop)
		loops.Wait()

		captured := 0
		for _, s := range snaps {
			if s.SnapID != 1 {
				t.Fatalf("round %d: process %d reported snapID %d, want 1", round, s.ProcessID, s.SnapID)
			}
			captured += s.Total()
		}
		if want := n * perNode; captured != want {
			t.Fatalf("round %d: captured total %d, want %d (assets not conserved)", round, captured, want)
		}
	}
}

// TestSnapshotTerminates verifies markers propagate to every node and each
// node finishes its snapshot even while traffic continues.
func TestSnapshotTerminates(t *testing.T) {
	procs, stop, loops := buildCluster(3, 50, time.Millisecond)
	trafficStop, traffic := runTraffic(procs)
	defer func() {
		close(trafficStop)
		traffic.Wait()
		close(stop)
		loops.Wait()
	}()

	procs[2].InitiateSnapshot(7) // initiated by a non-zero node
	snaps := collectSnapshots(t, procs)
	for _, s := range snaps {
		if s.SnapID != 7 {
			t.Fatalf("process %d: snapID %d, want 7", s.ProcessID, s.SnapID)
		}
	}
}

// TestNoMessageLoss stops traffic after the snapshot, drains the channels,
// and confirms the live balances still sum to the initial total.
func TestNoMessageLoss(t *testing.T) {
	const n, perNode = 3, 100
	procs, stop, loops := buildCluster(n, perNode, 2*time.Millisecond)
	trafficStop, traffic := runTraffic(procs)

	time.Sleep(20 * time.Millisecond)
	procs[0].InitiateSnapshot(1)
	collectSnapshots(t, procs)

	// Stop generating transfers, then let the network drain so every
	// in-flight message is delivered and processed before teardown.
	close(trafficStop)
	traffic.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for {
		total := 0
		for _, p := range procs {
			total += p.Balance()
		}
		if total == n*perNode {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("total %d, want %d (messages lost or duplicated)", total, n*perNode)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	loops.Wait()

	total := 0
	for _, p := range procs {
		total += p.Balance()
	}
	if want := n * perNode; total != want {
		t.Fatalf("total %d, want %d (messages lost or duplicated)", total, want)
	}
}

// TestTransferRespectsFunds checks a process cannot overspend.
func TestTransferRespectsFunds(t *testing.T) {
	a := NewProcess(0, 5, []int{1})
	b := NewProcess(1, 0, []int{0})
	a.Connect(b)
	for i := 0; i < 5; i++ {
		if !a.Transfer(1, 1) {
			t.Fatalf("transfer %d should succeed", i)
		}
	}
	if a.Transfer(1, 1) {
		t.Fatal("transfer beyond balance should fail")
	}
	if got := a.Balance(); got != 0 {
		t.Fatalf("balance %d, want 0", got)
	}
}
