package logtrunc

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// The planner is compared against the per-entry dependency-set
// Reference under random workloads that include consumer registration,
// consumer exit, out-of-order confirmations, rejected rollback attempts,
// and snapshot pin/release.
func TestPlannerMatchesReference(t *testing.T) {
	const (
		segSize = 5
		nSeg    = 8
		steps   = 4000
	)
	maxSeq := uint64(segSize * nSeg)

	seed := int64(20261004)
	if s := os.Getenv("SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	rng := rand.New(rand.NewSource(seed))
	store := newStore(segSize, nSeg)
	reg := NewRegistry(store.End)
	snaps := NewSnapshots()
	planner := newPlanner(store, reg, snaps)
	ref := NewReference()
	ref.SetLogEnd(maxSeq)

	var consumers, snapshots []string
	var consumerSeq, snapshotSeq int

	pickConsumer := func() string { return consumers[rng.Intn(len(consumers))] }

	for step := 0; step < steps; step++ {
		op := rng.Intn(8)
		switch {
		case op == 0 && len(consumers) < 4: // register
			id := fmt.Sprintf("c%d", consumerSeq)
			consumerSeq++
			if err := reg.Register(id); err != nil {
				t.Fatal(err)
			}
			ref.RegisterConsumer(id)
			consumers = append(consumers, id)
		case op == 1 && len(consumers) > 0: // exit
			i := rng.Intn(len(consumers))
			id := consumers[i]
			if err := reg.Exit(id); err != nil {
				t.Fatal(err)
			}
			ref.ConsumerExit(id)
			consumers = append(consumers[:i], consumers[i+1:]...)
		case op == 2 && len(consumers) > 0: // confirm single seq, out of order
			id := pickConsumer()
			seq := uint64(rng.Intn(int(maxSeq))) + 1
			if err := reg.Confirm(id, seq); err != nil {
				t.Fatal(err)
			}
			horizon, _ := reg.Horizon(id)
			ref.AdvanceHorizon(id, horizon)
		case op == 3 && len(consumers) > 0: // confirm prefix, sometimes a rollback
			id := pickConsumer()
			horizon, _ := reg.Horizon(id)
			n := uint64(rng.Intn(int(maxSeq) + 1))
			if horizon > 0 && rng.Intn(3) == 0 {
				n = uint64(rng.Intn(int(horizon))) // force rollback attempt
			}
			err := reg.ConfirmPrefix(id, n)
			if n < horizon {
				if !errors.Is(err, ErrRollback) {
					t.Fatalf("step %d: rollback to %d (horizon %d): got %v, want ErrRollback", step, n, horizon, err)
				}
				continue // rejected: no state change on either side
			}
			if err != nil {
				t.Fatal(err)
			}
			newHorizon, _ := reg.Horizon(id)
			ref.AdvanceHorizon(id, newHorizon)
		case op == 4 && len(snapshots) < 3: // pin snapshot
			id := fmt.Sprintf("s%d", snapshotSeq)
			snapshotSeq++
			from := uint64(rng.Intn(int(maxSeq))) + 1
			to := from + uint64(rng.Intn(int(maxSeq-from+1)))
			if err := snaps.Pin(id, from, to); err != nil {
				t.Fatal(err)
			}
			ref.PinSnapshot(id, from, to)
			snapshots = append(snapshots, id)
		case op == 5 && len(snapshots) > 0: // release snapshot
			i := rng.Intn(len(snapshots))
			id := snapshots[i]
			snaps.Release(id)
			ref.ReleaseSnapshot(id)
			snapshots = append(snapshots[:i], snapshots[i+1:]...)
		}

		// After every step, compare the planner against the reference
		// for a random truncation target.
		target := uint64(rng.Intn(int(maxSeq) + 1))
		plan := planner.Plan(target)
		deletable := deletableIDs(plan)
		for _, seg := range store.Segments() {
			if seg.End > target {
				continue
			}
			want := ref.Deletable(seg)
			got := deletable[seg.ID]
			if want != got {
				t.Fatalf("step %d target %d: %s deletable=%v, reference=%v",
					step, target, seg.ID, got, want)
			}
			if got {
				continue
			}
			ret := retainedByID(plan, seg.ID)
			if ret == nil || len(ret.Blockers) == 0 {
				t.Fatalf("step %d: retained %s has no blocking evidence", step, seg.ID)
			}
			assertEvidenceMatchesReference(t, step, seg, ret.Blockers, ref)
		}
	}
}

// assertEvidenceMatchesReference checks that the planner's evidence set
// for a retained segment is exactly the reference's dependency set
// restricted to that segment: no missing blockers, no spurious ones.
func assertEvidenceMatchesReference(t *testing.T, step int, seg Segment, blockers []Blocker, ref *Reference) {
	t.Helper()
	wantConsumers := map[string]bool{}
	for id, horizon := range ref.horizons {
		if horizon < seg.End {
			wantConsumers[id] = true
		}
	}
	wantSnapshots := map[string]bool{}
	for id, r := range ref.snapshots {
		if seg.Overlaps(r[0], r[1]) {
			wantSnapshots[id] = true
		}
	}
	seenConsumers, seenSnapshots := 0, 0
	for _, b := range blockers {
		switch b.Kind {
		case BlockerConsumer:
			seenConsumers++
			if !wantConsumers[b.Consumer] {
				t.Fatalf("step %d: spurious consumer blocker %q on %s", step, b.Consumer, seg.ID)
			}
			horizon := ref.horizons[b.Consumer]
			wantGap := seg.Start
			if horizon+1 > wantGap {
				wantGap = horizon + 1
			}
			if b.Horizon != horizon || b.FirstGap != wantGap {
				t.Fatalf("step %d: blocker %+v, want horizon %d first gap %d", step, b, horizon, wantGap)
			}
			// The cited gap must be a real dependency in the reference.
			if !ref.Dependents(b.FirstGap)["c/"+b.Consumer] {
				t.Fatalf("step %d: cited gap %d is not a real dependency of %q", step, b.FirstGap, b.Consumer)
			}
		case BlockerSnapshot:
			seenSnapshots++
			if !wantSnapshots[b.Snapshot] {
				t.Fatalf("step %d: spurious snapshot blocker %q on %s", step, b.Snapshot, seg.ID)
			}
			r := ref.snapshots[b.Snapshot]
			if b.PinFrom != r[0] || b.PinTo != r[1] {
				t.Fatalf("step %d: blocker %+v, want pin [%d,%d]", step, b, r[0], r[1])
			}
		}
	}
	if seenConsumers != len(wantConsumers) || seenSnapshots != len(wantSnapshots) {
		t.Fatalf("step %d: evidence for %s incomplete: got %d consumers + %d snapshots, want %d + %d",
			step, seg.ID, seenConsumers, seenSnapshots, len(wantConsumers), len(wantSnapshots))
	}
}
