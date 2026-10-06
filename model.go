package agg

// OpKind identifies the kind of one step in an event-label trace.
type OpKind int

const (
	OpEvent   OpKind = iota // deliver Event
	OpBarrier               // deliver barrier (Partition, Gen)
	OpExit                  // Partition leaves the expected set
	OpJoin                  // Partition (re)joins the expected set
)

// Op is one step of an event-label trace.
type Op struct {
	Kind      OpKind
	Event     Event  // OpEvent
	Partition string // OpBarrier, OpExit, OpJoin
	Gen       int    // OpBarrier
}

// Model replays an op trace and computes publications directly from event
// labels. Unlike Aggregator it performs no buffering and no generation
// rotation: a generation's final value is simply the sum over the distinct
// labels carrying that generation's tag, contributed while the partition
// was a member. Agreement between Model and Aggregator over arbitrary
// traces is the cross-check that the aggregator's buffering machinery is
// faithful to the label semantics.
func Model(initial []string, ops []Op) []Publication {
	members := make(map[string]bool, len(initial))
	crossed := make(map[string]int, len(initial))
	for _, p := range initial {
		members[p] = true
		crossed[p] = 0
	}
	seen := make(map[label]bool)
	sums := make(map[int]map[string]int64) // gen -> partition -> sum
	gen := 0
	var pubs []Publication

	allCrossed := func() bool {
		if len(members) == 0 {
			return false
		}
		for p := range members {
			if crossed[p] != gen+1 {
				return false
			}
		}
		return true
	}
	closeGen := func() {
		var total int64
		for _, s := range sums[gen] {
			total += s
		}
		pubs = append(pubs, Publication{Gen: gen, Total: total})
		delete(sums, gen)
		gen++
	}

	for _, op := range ops {
		switch op.Kind {
		case OpEvent:
			e := op.Event
			l := label{e.Partition, e.Seq}
			if !members[e.Partition] || seen[l] || e.Gen != crossed[e.Partition] {
				continue
			}
			seen[l] = true
			if sums[e.Gen] == nil {
				sums[e.Gen] = make(map[string]int64)
			}
			sums[e.Gen][e.Partition] += e.Value
		case OpBarrier:
			if !members[op.Partition] || op.Gen != gen || crossed[op.Partition] != gen {
				continue
			}
			crossed[op.Partition] = gen + 1
			if allCrossed() {
				closeGen()
			}
		case OpExit:
			if !members[op.Partition] {
				continue
			}
			delete(members, op.Partition)
			delete(crossed, op.Partition)
			// Discard the exited partition's buffered post-barrier
			// contribution; its pre-barrier contribution stays.
			delete(sums[gen+1], op.Partition)
			if allCrossed() {
				closeGen()
			}
		case OpJoin:
			if members[op.Partition] {
				continue
			}
			members[op.Partition] = true
			crossed[op.Partition] = gen
		}
	}
	return pubs
}
