package logtrunc

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// checkConsistency cross-checks the segment-level Planner against the
// per-record dependency-set Reference after every operation.
func checkConsistency(t *testing.T, p *Planner, ref *Reference) {
	t.Helper()
	plan := p.Plan()

	// Horizon must equal the minimum per-record contiguous watermark.
	refConsumers := ref.Consumers()
	if len(refConsumers) == 0 {
		if plan.HasConsumers {
			t.Fatal("planner sees consumers, reference sees none")
		}
	} else {
		min := ref.Watermark(refConsumers[0])
		for _, c := range refConsumers[1:] {
			if w := ref.Watermark(c); w < min {
				min = w
			}
		}
		if !plan.HasConsumers || plan.Horizon != min {
			t.Fatalf("horizon = (%d, %v), reference min watermark = %d", plan.Horizon, plan.HasConsumers, min)
		}
	}

	deletable := make(map[string]bool, len(plan.Deletable))
	for _, s := range plan.Deletable {
		deletable[s.ID] = true
	}
	retained := make(map[string]SegmentPlan, len(plan.Retained))
	for _, sp := range plan.Retained {
		retained[sp.Segment.ID] = sp
	}

	for _, s := range p.Segments() {
		want := ref.SegmentDeletable(s.ID)
		if got := deletable[s.ID]; got != want {
			t.Fatalf("segment %s [%d,%d): planner deletable=%v, reference=%v",
				s.ID, s.Base, s.End, got, want)
		}
		if want {
			continue
		}
		sp, ok := retained[s.ID]
		if !ok {
			t.Fatalf("segment %s retained but missing from plan.Retained", s.ID)
		}
		if len(sp.Evidence) == 0 {
			t.Fatalf("segment %s: retained without any blocking evidence", s.ID)
		}
		// Every evidence item must correspond to a real per-record
		// dependency inside the segment.
		for _, ev := range sp.Evidence {
			dep := DepConsumer + ev.Consumer
			if ev.Snapshot != "" {
				dep = DepSnapshot + ev.Snapshot
			}
			found := false
			for off := s.Base; off < s.End && !found; off++ {
				for _, d := range ref.RecordDeps(off) {
					if d == dep {
						found = true
						break
					}
				}
			}
			if !found {
				t.Fatalf("segment %s: evidence %+v has no matching per-record dependency", s.ID, ev)
			}
		}
		// AllBlockers must match the reference exactly.
		wantConsumers, wantSnapshots := referenceBlockers(ref, s)
		gotConsumers, gotSnapshots := p.AllBlockers(s.ID)
		if !reflect.DeepEqual(gotConsumers, wantConsumers) {
			t.Fatalf("segment %s: AllBlockers consumers = %v, reference = %v", s.ID, gotConsumers, wantConsumers)
		}
		if !reflect.DeepEqual(gotSnapshots, wantSnapshots) {
			t.Fatalf("segment %s: AllBlockers snapshots = %v, reference = %v", s.ID, gotSnapshots, wantSnapshots)
		}
	}
}

func referenceBlockers(ref *Reference, s Segment) (consumers, snapshots []string) {
	for _, name := range ref.Consumers() {
		if ref.Watermark(name) < s.End {
			consumers = append(consumers, name)
		}
	}
	for snapID, pins := range ref.snapshots {
		if pins[s.ID] {
			snapshots = append(snapshots, snapID)
		}
	}
	sort.Strings(snapshots)
	return consumers, snapshots
}

// TestPlannerMatchesReference drives both models through identical
// random operation sequences — including consumer exits and attempted
// ack rollbacks — and compares them after every step.
func TestPlannerMatchesReference(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			p := NewPlanner()
			ref := NewReference()
			var nextBase uint64
			segN, consN, snapN := 0, 0, 0

			for step := 0; step < 300; step++ {
				consumers := p.Consumers()
				segments := p.Segments()
				switch op := rng.Intn(100); {
				case op < 20 || nextBase == 0:
					// Append a new segment at the tail of the log.
					length := uint64(1 + rng.Intn(8))
					s := Segment{ID: fmt.Sprintf("seg%d", segN), Base: nextBase, End: nextBase + length}
					segN++
					nextBase += length
					if err := p.AddSegment(s); err != nil {
						t.Fatal(err)
					}
					ref.AddSegment(s)
				case op < 45:
					// Individual ack, possibly leaving gaps.
					if len(consumers) == 0 {
						continue
					}
					name := consumers[rng.Intn(len(consumers))]
					off := uint64(rng.Intn(int(nextBase) + 2))
					if err := p.Ack(name, off); err != nil {
						t.Fatal(err)
					}
					ref.Confirm(name, off)
				case op < 58:
					// Bulk watermark move; rollbacks must be rejected by
					// the planner and skipped for the reference.
					if len(consumers) == 0 {
						continue
					}
					name := consumers[rng.Intn(len(consumers))]
					w := uint64(rng.Intn(int(nextBase) + 2))
					err := p.SetWatermark(name, w)
					if err != nil {
						if _, ok := err.(*RollbackError); !ok {
							t.Fatalf("SetWatermark: unexpected error %v", err)
						}
						continue
					}
					cur := ref.Watermark(name)
					for off := cur; off < w; off++ {
						ref.Confirm(name, off)
					}
				case op < 68:
					// New consumer joins.
					name := fmt.Sprintf("cons%d", consN)
					consN++
					p.AddConsumer(name, 0)
					ref.AddConsumer(name)
				case op < 78:
					// Consumer exits.
					if len(consumers) == 0 {
						continue
					}
					name := consumers[rng.Intn(len(consumers))]
					p.RemoveConsumer(name)
					ref.RemoveConsumer(name)
				case op < 90:
					// New snapshot pins a random subset of segments.
					if len(segments) == 0 {
						continue
					}
					pins := make(map[string]struct{})
					var pinList []string
					for _, s := range segments {
						if rng.Intn(2) == 0 {
							pins[s.ID] = struct{}{}
							pinList = append(pinList, s.ID)
						}
					}
					if len(pins) == 0 {
						continue
					}
					id := fmt.Sprintf("snap%d", snapN)
					snapN++
					p.AddSnapshot(Snapshot{ID: id, Segments: pins})
					ref.AddSnapshot(id, pinList)
				case op < 95:
					// Snapshot expires.
					if snapN == 0 {
						continue
					}
					id := fmt.Sprintf("snap%d", rng.Intn(snapN))
					p.RemoveSnapshot(id)
					ref.RemoveSnapshot(id)
				default:
					// Execute a truncation pass: delete everything the
					// planner says is deletable from both models.
					for _, s := range p.Plan().Deletable {
						p.RemoveSegment(s.ID)
						ref.RemoveSegment(s.ID)
					}
				}
				checkConsistency(t, p, ref)
			}
		})
	}
}
