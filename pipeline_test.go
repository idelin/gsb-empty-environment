package barrierpipe

import (
	"math/rand"
	"testing"
	"time"
)

func sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

// driveShuffled feeds per-input event streams into the aligner in a random
// interleaving (respecting per-input order and the blocking rule), closes
// exhausted inputs, and returns the downstream stream.
func driveShuffled(t *testing.T, al *Aligner, events [][]Event, rng *rand.Rand) []Event {
	t.Helper()
	n := len(events)
	pos := make([]int, n)
	closed := make([]bool, n)
	var downstream []Event
	for {
		var candidates []int
		for i := 0; i < n; i++ {
			if !closed[i] && !al.Blocked(i) && pos[i] < len(events[i]) {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 {
			progressed := false
			for i := 0; i < n; i++ {
				if !closed[i] && pos[i] == len(events[i]) {
					downstream = append(downstream, al.Close(i)...)
					closed[i] = true
					progressed = true
				}
			}
			if !progressed {
				for i := 0; i < n; i++ {
					if !closed[i] {
						t.Fatalf("driver stuck: input %d neither blocked-exhausted nor closed (pos=%d/%d)", i, pos[i], len(events[i]))
					}
				}
				return downstream
			}
			continue
		}
		i := candidates[rng.Intn(len(candidates))]
		downstream = append(downstream, al.OnEvent(i, events[i][pos[i]])...)
		pos[i]++
	}
}

func buildStreams(rng *rand.Rand, numInputs, numGens, maxPerGen int) ([][]Event, [][]int) {
	events := make([][]Event, numInputs)
	plan := make([][]int, numInputs)
	for i := 0; i < numInputs; i++ {
		plan[i] = make([]int, numGens)
		for g := 0; g < numGens; g++ {
			plan[i][g] = rng.Intn(maxPerGen + 1)
			for k := 0; k < plan[i][g]; k++ {
				events[i] = append(events[i], Data(i, g+1, i*1000+g*100+k))
			}
			events[i] = append(events[i], Barrier(g+1))
		}
	}
	return events, plan
}

// Out-of-order delivery across inputs must not mix generations around a
// snapshot: downstream is gen-n records, then Barrier(n), then gen-(n+1).
func TestAlignerOutOfOrderNoGenerationMixing(t *testing.T) {
	const numInputs, numGens = 3, 5
	rng := rand.New(rand.NewSource(42))
	events, plan := buildStreams(rng, numInputs, numGens, 3)

	al := NewAligner(numInputs)
	downstream := driveShuffled(t, al, events, rng)

	if err := VerifyDownstream(downstream, numGens); err != nil {
		t.Fatalf("generation mixing detected: %v", err)
	}

	// One snapshot per barrier, taken exactly when all inputs arrived:
	// consumed counts must equal all records of generations <= barrier.
	if len(al.Snapshots) != numGens {
		t.Fatalf("got %d snapshots, want %d", len(al.Snapshots), numGens)
	}
	for idx, snap := range al.Snapshots {
		if snap.Barrier != idx+1 {
			t.Fatalf("snapshot %d has barrier %d, want %d", idx, snap.Barrier, idx+1)
		}
		for i := 0; i < numInputs; i++ {
			want := sum(plan[i][:snap.Barrier])
			if snap.Consumed[i] != want {
				t.Errorf("snapshot barrier=%d input=%d consumed=%d, want %d", snap.Barrier, i, snap.Consumed[i], want)
			}
		}
	}

	// No record lost or duplicated.
	counts := make([]int, numInputs)
	for _, ev := range downstream {
		if ev.Kind == DataEvent {
			counts[ev.Rec.Src]++
		}
	}
	for i := range counts {
		if want := sum(plan[i]); counts[i] != want {
			t.Errorf("input %d: downstream has %d records, want %d", i, counts[i], want)
		}
	}
}

// An input that reaches the barrier first must pause data advancement until
// the barrier aligns.
func TestBlockedInputPausesData(t *testing.T) {
	al := NewAligner(2)
	al.OnEvent(0, Data(0, 1, 1))
	if out := al.OnEvent(0, Barrier(1)); len(out) != 0 {
		t.Fatalf("barrier emitted downstream before alignment: %v", out)
	}
	if !al.Blocked(0) {
		t.Fatal("input 0 should be blocked at barrier 1")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic when advancing data on a blocked input")
			}
		}()
		al.OnEvent(0, Data(0, 2, 1))
	}()

	// Once input 1 arrives, the barrier aligns and input 0 unblocks.
	out := al.OnEvent(1, Barrier(1))
	if len(out) != 1 || out[0].Kind != BarrierEvent || out[0].Barrier != 1 {
		t.Fatalf("expected aligned barrier 1 downstream, got %v", out)
	}
	if al.Blocked(0) {
		t.Fatal("input 0 should be unblocked after alignment")
	}
	if got := al.OnEvent(0, Data(0, 2, 1)); len(got) != 1 {
		t.Fatalf("data after alignment should flow, got %v", got)
	}
}

// A closed input counts as arrived at every subsequent barrier, so the
// remaining inputs keep aligning and snapshotting without it.
func TestClosedInputCountsAsArrived(t *testing.T) {
	const numGens = 4
	rng := rand.New(rand.NewSource(3))
	events, plan := buildStreams(rng, 3, numGens, 2)

	// Input 2 closes right after barrier 1: drop everything past it.
	cut := 0
	for cut < len(events[2]) {
		ev := events[2][cut]
		cut++
		if ev.Kind == BarrierEvent && ev.Barrier == 1 {
			break
		}
	}
	events[2] = events[2][:cut]

	al := NewAligner(3)
	downstream := driveShuffled(t, al, events, rng)

	if err := VerifyDownstream(downstream, numGens); err != nil {
		t.Fatalf("generation mixing with closed input: %v", err)
	}
	if len(al.Snapshots) != numGens {
		t.Fatalf("got %d snapshots, want %d (closed input must not stall alignment)", len(al.Snapshots), numGens)
	}
	// The closed input's consumed count freezes at its gen-1 total.
	for _, snap := range al.Snapshots {
		if want := plan[2][0]; snap.Consumed[2] != want {
			t.Errorf("snapshot barrier=%d: closed input consumed=%d, want frozen %d", snap.Barrier, snap.Consumed[2], want)
		}
		for i := 0; i < 2; i++ {
			if want := sum(plan[i][:snap.Barrier]); snap.Consumed[i] != want {
				t.Errorf("snapshot barrier=%d input=%d consumed=%d, want %d", snap.Barrier, i, snap.Consumed[i], want)
			}
		}
	}
}

// End-to-end with live channels: sources emit with random delays (out-of-order
// across inputs), blocked inputs stop being drained so per-input backlog stays
// bounded by the channel capacity, and generations never mix downstream.
func TestPipelineBoundedBacklog(t *testing.T) {
	const numInputs, numGens, capacity = 4, 6, 3
	rng := rand.New(rand.NewSource(7))
	_, plan := buildStreams(rng, numInputs, numGens, 5)

	chans := make([]chan Event, numInputs)
	inputs := make([]<-chan Event, numInputs)
	for i := range chans {
		chans[i] = make(chan Event, capacity)
		inputs[i] = chans[i]
	}
	op := NewOperator(inputs, numInputs*numGens*6)
	go op.Run()
	for i := range chans {
		srcRng := rand.New(rand.NewSource(int64(100 + i)))
		delay := func() {
			if srcRng.Intn(3) == 0 {
				time.Sleep(time.Duration(srcRng.Intn(3)) * time.Millisecond)
			}
		}
		go Source(i, plan[i], i*1000, delay, -1, chans[i])
	}

	var downstream []Event
	for ev := range op.Output {
		downstream = append(downstream, ev)
	}

	if err := VerifyDownstream(downstream, numGens); err != nil {
		t.Fatalf("generation mixing in live pipeline: %v", err)
	}
	counts := make([]int, numInputs)
	for _, ev := range downstream {
		if ev.Kind == DataEvent {
			counts[ev.Rec.Src]++
		}
	}
	for i := range counts {
		if want := sum(plan[i]); counts[i] != want {
			t.Errorf("input %d: downstream has %d records, want %d", i, counts[i], want)
		}
	}
	for i, m := range op.MaxBacklog {
		if m > capacity {
			t.Errorf("input %d backlog %d exceeds capacity %d", i, m, capacity)
		}
	}
	t.Logf("max backlog per input: %v (capacity %d)", op.MaxBacklog, capacity)
	if got := len(op.Aligner().Snapshots); got != numGens {
		t.Errorf("got %d snapshots, want %d", got, numGens)
	}
}

// Backpressure proof: with a tiny input buffer, a fast input that reaches the
// barrier early must be paused, so its backlog stays bounded while a slow
// input is still producing earlier-generation records.
func TestPipelineFastInputPausedByBackpressure(t *testing.T) {
	const capacity = 2
	fast := make(chan Event, capacity)
	slow := make(chan Event, capacity)
	op := NewOperator([]<-chan Event{fast, slow}, 16)
	go op.Run()

	// Fast input races to barrier 1.
	go Source(0, []int{2, 2}, 0, nil, -1, fast)
	// Slow input crawls through generation 1.
	go Source(1, []int{2, 2}, 100, func() { time.Sleep(5 * time.Millisecond) }, -1, slow)

	var downstream []Event
	for ev := range op.Output {
		downstream = append(downstream, ev)
	}
	if err := VerifyDownstream(downstream, 2); err != nil {
		t.Fatalf("generation mixing: %v", err)
	}
	if op.MaxBacklog[0] > capacity {
		t.Errorf("fast input backlog %d exceeds capacity %d: not paused", op.MaxBacklog[0], capacity)
	}
	t.Logf("fast input max backlog: %d (capacity %d)", op.MaxBacklog[0], capacity)
}
