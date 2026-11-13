package netsec

import (
	"fmt"
	"reflect"
	"testing"
)

func TestExhaustiveReclaimCounters(t *testing.T) {
	for retireAt := 0; retireAt < EpochCount; retireAt++ {
		for successful := 0; successful <= 8; successful++ {
			s := testState(t, 0, Options{AutoReclaim: false})
			for i := 0; i < retireAt; i++ {
				applyOK(t, s, UpdaterThread, TryAdvance())
			}
			applyOK(t, s, UpdaterThread, ReplaceRoot(1))
			ref := ObjectRef{ID: 1, Gen: 0}
			applyOK(t, s, UpdaterThread, RetireGen(ref))
			for i := 0; i < successful; i++ {
				applyOK(t, s, UpdaterThread, TryAdvance())
			}
			info, ok := s.Retirement(ref)
			if !ok {
				t.Fatalf("retireAt=%d successful=%d: missing retirement", retireAt, successful)
			}
			wantEligible := successful >= 2
			if info.SuccessfulAdvances != uint64(successful) ||
				info.SafeAdvances != uint8(intMin(successful, 255)) {
				t.Fatalf("retireAt=%d successful=%d: counters=%+v", retireAt, successful, info)
			}
			if got := s.CanReclaim(ref); got != wantEligible {
				t.Fatalf("retireAt=%d successful=%d: CanReclaim=%t want %t (%+v)",
					retireAt, successful, got, wantEligible, info)
			}
			if wantEligible {
				if err := s.Reclaim(ref); err != nil {
					t.Fatalf("retireAt=%d successful=%d: Reclaim: %v", retireAt, successful, err)
				}
			}
		}
	}
}

func TestExhaustiveSearchMatchesBruteForce(t *testing.T) {
	threadAlphabet := []Event{
		Enter(),
		LoadRoot(),
		Dereference(),
		Exit(),
		Quiescent(),
	}
	updaterAlphabet := []Event{
		ReplaceRoot(1),
		RetireObject(1),
		TryAdvance(),
	}
	threadScripts := enumerateScripts(threadAlphabet, 3)
	updaterScripts := enumerateScripts(updaterAlphabet, 3)

	checked := 0
	for _, threadScript := range threadScripts {
		for _, updaterScript := range updaterScripts {
			p := NewProgram(threadScript, updaterScript)
			want, err := bruteShortestWitness(p)
			if err != nil {
				t.Fatalf("brute force: %v", err)
			}
			got, err := FindShortestWitness(p)
			if err != nil {
				t.Fatalf("FindShortestWitness(%v): %v", p.Threads, err)
			}
			if (got == nil) != (want == nil) {
				t.Fatalf("witness existence differs for %v:\nBFS=%v\nbrute=%v",
					p.Threads, got, want)
			}
			if got != nil {
				if got.Length() != want.Length() ||
					!reflect.DeepEqual(got.Actors(), want.Actors()) ||
					got.Violation.Ref != want.Violation.Ref {
					t.Fatalf("witness mismatch for %v:\nBFS=%v\nbrute=%v",
						p.Threads, got, want)
				}
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no programs checked")
	}
	t.Logf("compared %d programs", checked)
}

func enumerateScripts(alphabet []Event, maxLen int) [][]Event {
	scripts := [][]Event{{}}
	var walk func(cur []Event)
	walk = func(cur []Event) {
		if len(cur) == maxLen {
			return
		}
		for _, ev := range alphabet {
			next := append(append([]Event(nil), cur...), ev)
			scripts = append(scripts, next)
			walk(next)
		}
	}
	walk(nil)
	return scripts
}

func bruteShortestWitness(p Program) (*Witness, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	st, err := NewStateWithOptions(len(p.Threads), DefaultOptions())
	if err != nil {
		return nil, err
	}
	pos := make([]int, len(p.Threads))
	var best *Witness

	var walk func(cur *State, pos []int, steps []WitnessStep)
	walk = func(cur *State, pos []int, steps []WitnessStep) {
		for thread := range p.Threads {
			if pos[thread] >= len(p.Threads[thread]) {
				continue
			}
			ev := p.Threads[thread][pos[thread]]
			next := cur.Clone()
			applyThread := ThreadID(thread)
			if ev.Kind.IsUpdaterEvent() {
				applyThread = UpdaterThread
			}
			out, err := next.Apply(applyThread, ev)
			if err != nil {
				continue
			}
			step := WitnessStep{
				Thread:    thread,
				Event:     ev,
				Advanced:  out.Advanced,
				Reclaimed: append([]ObjectRef(nil), out.Reclaimed...),
			}
			nextSteps := append(append([]WitnessStep(nil), steps...), step)
			if out.Violation != nil {
				cand := &Witness{
					Steps:     nextSteps,
					Violation: *out.Violation,
					State:     next.Clone(),
				}
				if best == nil || compareWitness(cand, best) < 0 {
					best = cand
				}
				continue
			}
			nextPos := append([]int(nil), pos...)
			nextPos[thread]++
			walk(next, nextPos, nextSteps)
		}
	}
	walk(st, pos, nil)
	return best, nil
}

func compareWitness(a, b *Witness) int {
	if a.Length() != b.Length() {
		if a.Length() < b.Length() {
			return -1
		}
		return 1
	}
	aa, ba := a.Actors(), b.Actors()
	for i := range aa {
		if aa[i] < ba[i] {
			return -1
		}
		if aa[i] > ba[i] {
			return 1
		}
	}
	return 0
}

func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestWitnessFormatting(t *testing.T) {
	p := NewProgram(
		[]Event{Enter(), LoadRoot(), Exit(), Enter(), Dereference()},
		[]Event{ReplaceRoot(1), RetireObject(1), TryAdvance(), TryAdvance()},
	)
	w, err := FindShortestWitness(p)
	if err != nil || w == nil {
		t.Fatalf("FindShortestWitness: %v, %v", w, err)
	}
	if got := fmt.Sprint(w); got == "" {
		t.Fatal("empty witness string")
	}
}
