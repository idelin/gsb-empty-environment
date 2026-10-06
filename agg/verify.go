package agg

import "fmt"

// Violation is a protocol invariant broken by the event log.
type Violation struct {
	Seq uint64
	Msg string
}

// Verify is the event-tag verification model. It re-derives the
// protocol facts independently from the tagged log and checks the
// invariants of the barrier protocol:
//
//  1. exactly-once:   at most one GenerationFinalized per generation
//  2. completeness:   a generation finalizes only after every partition
//     that is still a member has crossed the barrier since the reset
//  3. no-duplicates:  at most one BarrierCrossed per (partition, gen)
//     within one reset window
//  4. exit-safety:    after PartitionExited, no accepted data and no
//     barrier crossing from that partition until it rejoins
//  5. value-safety:   the finalized value equals the sum of accepted
//     old-generation data for that generation
//  6. carry-safety:   the value carried into a rolled generation equals
//     the buffered new-generation data accepted so far
//  7. timeliness:     finalization happens immediately when the barrier
//     set completes, and the roll immediately follows the finalize
func Verify(log []Event) []Violation {
	var v []Violation
	fail := func(seq uint64, format string, args ...interface{}) {
		v = append(v, Violation{Seq: seq, Msg: fmt.Sprintf(format, args...)})
	}

	members := map[string]bool{}
	crossed := map[string]bool{} // per current reset window; an exit
	// clears the entry, so a partition that rejoins must cross again
	resetOpen := false
	var curGen uint64
	oldSum := map[uint64]int64{} // accepted old-generation data per gen
	newSum := int64(0)           // buffered new-generation data since last roll
	finalized := map[uint64]bool{}

	// pendingCompletion is set when an exit/barrier event completed the
	// barrier set; the finalize must be the very next event.
	pendingCompletion := false

	allCrossed := func() bool {
		if len(members) == 0 {
			return false
		}
		for m := range members {
			if !crossed[m] {
				return false
			}
		}
		return true
	}

	for i, ev := range log {
		if pendingCompletion && ev.Kind != KindGenFinalized {
			fail(ev.Seq, "timeliness: barrier set completed at seq %d but next event is %s, not GenerationFinalized", log[i-1].Seq, ev.Kind)
		}
		pendingCompletion = false

		switch ev.Kind {
		case KindPartitionJoined:
			members[ev.Partition] = true

		case KindPartitionExited:
			delete(members, ev.Partition)
			delete(crossed, ev.Partition)
			if resetOpen && allCrossed() {
				pendingCompletion = true
			}

		case KindResetIssued:
			if resetOpen {
				fail(ev.Seq, "control: ResetIssued while a reset is already in flight")
			}
			resetOpen = true
			crossed = map[string]bool{}

		case KindDataAccepted:
			if !members[ev.Partition] {
				fail(ev.Seq, "exit-safety: data accepted from non-member %q", ev.Partition)
			}
			if ev.Gen == curGen {
				oldSum[ev.Gen] += ev.Value
			} else if ev.Gen == curGen+1 {
				newSum += ev.Value
			} else {
				fail(ev.Seq, "data: accepted data for unexpected generation %d (current %d)", ev.Gen, curGen)
			}

		case KindBarrierCrossed:
			if !resetOpen {
				fail(ev.Seq, "barrier: %q crossed with no reset in flight", ev.Partition)
			}
			if !members[ev.Partition] {
				fail(ev.Seq, "exit-safety: %q crossed after exiting", ev.Partition)
			}
			if crossed[ev.Partition] {
				fail(ev.Seq, "no-duplicates: %q crossed barrier for gen %d twice in one reset window", ev.Partition, ev.Gen)
			}
			crossed[ev.Partition] = true
			if allCrossed() {
				pendingCompletion = true
			}

		case KindGenFinalized:
			if finalized[ev.Gen] {
				fail(ev.Seq, "exactly-once: generation %d finalized more than once", ev.Gen)
			}
			finalized[ev.Gen] = true
			for m := range members {
				if !crossed[m] {
					fail(ev.Seq, "completeness: gen %d finalized while member %q had not crossed", ev.Gen, m)
				}
			}
			if ev.Value != oldSum[ev.Gen] {
				fail(ev.Seq, "value-safety: gen %d finalized as %d, want %d", ev.Gen, ev.Value, oldSum[ev.Gen])
			}
			if i+1 >= len(log) || log[i+1].Kind != KindGenRolled {
				fail(ev.Seq, "timeliness: GenerationFinalized(%d) not immediately followed by GenerationRolled", ev.Gen)
			}

		case KindGenRolled:
			if ev.Gen != curGen+1 {
				fail(ev.Seq, "control: rolled to gen %d from gen %d", ev.Gen, curGen)
			}
			if ev.Value != newSum {
				fail(ev.Seq, "carry-safety: rolled to gen %d carrying %d, want %d", ev.Gen, ev.Value, newSum)
			}
			curGen = ev.Gen
			resetOpen = false
			crossed = map[string]bool{}
			newSum = 0
			oldSum[ev.Gen] += ev.Value // carried buffer counts toward the new old-generation
		}
	}
	return v
}
