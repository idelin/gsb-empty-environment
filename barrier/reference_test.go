package barrier

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestCrossCheckReference replays a deterministic pseudo-random event
// stream - out-of-order applies, gap fills, restarts with epochs, recovery
// proofs, cancels, offlines and deadline judgements - through both the
// indexed Coordinator and the full-scan Reference, and demands identical
// outcomes at every tick.
func TestCrossCheckReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	clockA := &ManualClock{}
	clockB := &ManualClock{}
	coord := NewCoordinator(clockA)
	ref := NewReference(clockB)

	shards := []ShardID{"s1", "s2", "s3", "s4"}
	epoch := map[ShardID]Epoch{}       // test-side view of the newest epoch
	nextCommit := map[ShardID]Commit{} // next commit the shard will report
	live := map[ReqID]bool{}           // requests still pending
	var liveOrder []ReqID

	// Boot half the shards with a recovery proof; the rest start unknown.
	for i, sh := range shards {
		epoch[sh] = 1
		nextCommit[sh] = 1
		if i%2 == 0 {
			coord.Recover(sh, 1, 0)
			ref.Recover(sh, 1, 0)
		}
	}

	checkDrain := func(tick int) {
		t.Helper()
		got := coord.DrainResults()
		want := ref.DrainResults()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tick %d: result mismatch:\ncoord: %+v\nref:   %+v", tick, got, want)
		}
		for _, r := range got {
			delete(live, r.Req)
		}
		cs, rs := coord.Stats(), ref.Stats()
		if cs.PendingRequests != rs.PendingRequests || cs.BufferedReports != rs.BufferedReports {
			t.Fatalf("tick %d: stats mismatch: coord %+v ref %+v", tick, cs, rs)
		}
		if cs.PendingRequests != len(live) {
			t.Fatalf("tick %d: %d live requests tracked, coordinator holds %d", tick, len(live), cs.PendingRequests)
		}
	}

	for tick := 1; tick <= 60; tick++ {
		clockA.Set(Tick(tick))
		clockB.Set(Tick(tick))

		// Apply a random burst of events at this tick before judging.
		for ev := 0; ev < 1+rng.Intn(4); ev++ {
			sh := shards[rng.Intn(len(shards))]
			switch rng.Intn(10) {
			case 0, 1, 2, 3: // report, sometimes skipping a commit to open a gap
				if rng.Intn(4) == 0 {
					nextCommit[sh]++ // skip: creates a gap to be filled later
				}
				coord.Report(sh, epoch[sh], nextCommit[sh])
				ref.Report(sh, epoch[sh], nextCommit[sh])
				nextCommit[sh]++
			case 4: // stale-epoch report: must be ignored
				coord.Report(sh, epoch[sh]-1, nextCommit[sh]+5)
				ref.Report(sh, epoch[sh]-1, nextCommit[sh]+5)
			case 5: // restart: bump epoch, progress becomes unknown
				epoch[sh]++
				nextCommit[sh] = 1
				coord.Report(sh, epoch[sh], nextCommit[sh])
				ref.Report(sh, epoch[sh], nextCommit[sh])
				nextCommit[sh]++
			case 6: // recovery proof for the current epoch
				coord.Recover(sh, epoch[sh], 0)
				ref.Recover(sh, epoch[sh], 0)
			case 7: // shard goes offline
				coord.Offline(sh)
				ref.Offline(sh)
			case 8: // new client wait
				target := Commit(1 + rng.Intn(6))
				var set []ShardID
				for _, s2 := range shards {
					if rng.Intn(2) == 0 {
						set = append(set, s2)
					}
				}
				if len(set) == 0 {
					set = []ShardID{sh}
				}
				deadline := Tick(tick + 1 + rng.Intn(12))
				idA := coord.Wait(target, set, deadline)
				idB := ref.Wait(target, set, deadline)
				if idA != idB {
					t.Fatalf("request id divergence: %d vs %d", idA, idB)
				}
				live[idA] = true
				liveOrder = append(liveOrder, idA)
			case 9: // cancel a random live request
				if len(liveOrder) > 0 {
					id := liveOrder[rng.Intn(len(liveOrder))]
					if live[id] {
						if coord.Cancel(id) != ref.Cancel(id) {
							t.Fatalf("cancel divergence for req %d", id)
						}
					}
				}
			}
		}

		// Progress is applied; only now are deadlines judged.
		coord.JudgeTimeouts()
		ref.JudgeTimeouts()
		checkDrain(tick)
	}

	// Everything must eventually finish; no request or buffer may leak.
	clockA.Set(1000)
	clockB.Set(1000)
	coord.JudgeTimeouts()
	ref.JudgeTimeouts()
	checkDrain(1000)
	if st := coord.Stats(); st.PendingRequests != 0 || st.IndexEntries != 0 {
		t.Fatalf("coordinator leaked state: %+v", st)
	}
	if len(live) != 0 {
		t.Fatalf("%d requests never finished", len(live))
	}
	fmt.Println("cross-check replay finished: 60 ticks, coordinator == reference")
}
