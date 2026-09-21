package deter_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	deter "example.com/deter"
)

// sink collects task output. The runtime runs one task at a time, but the
// mutex keeps the helper honest under -race regardless of test structure.
type sink struct {
	mu   sync.Mutex
	logs []string
}

func (s *sink) add(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, fmt.Sprintf(format, args...))
}

// workload builds the injected task functions: a root task that spawns two
// workers; each worker sleeps on the virtual clock and performs external
// calls, one of which the injected handler fails.
func workload(s *sink) (string, deter.TaskFunc) {
	worker := func(name string) deter.TaskFunc {
		return func(env *deter.Env) {
			for i := 0; i < 2; i++ {
				env.Sleep(time.Duration(i+1) * time.Second)
				res, err := env.Call("fetch", name)
				if err != nil {
					s.add("%s: error: %v", name, err)
				} else {
					s.add("%s: %s", name, res)
				}
			}
			s.add("%s: done at %s", name, env.Now())
		}
	}
	root := func(env *deter.Env) {
		env.Spawn("worker-a", worker("a"))
		env.Spawn("worker-b", worker("b"))
		env.Sleep(500 * time.Millisecond)
		res, err := env.Call("fetch", "root")
		if err != nil {
			s.add("root: error: %v", err)
		} else {
			s.add("root: %s", res)
		}
		s.add("root: done at %s", env.Now())
	}
	return "root", root
}

// handler is the injected source of external results and failures.
func handler(name, arg string) (string, error) {
	if arg == "b" {
		return "", fmt.Errorf("injected failure for %q", arg)
	}
	return "data-" + arg, nil
}

func record(t *testing.T, s *sink) ([]deter.Event, *deter.Runtime) {
	t.Helper()
	rt := deter.NewRecord(handler)
	events, err := rt.Run(workload(s))
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	return events, rt
}

func TestRecordIsDeterministic(t *testing.T) {
	s1, s2 := &sink{}, &sink{}
	ev1, _ := record(t, s1)
	ev2, _ := record(t, s2)
	if !reflect.DeepEqual(ev1, ev2) {
		t.Fatalf("two recordings differ:\n%v\n%v", ev1, ev2)
	}
	if !reflect.DeepEqual(s1.logs, s2.logs) {
		t.Fatalf("two runs produced different output:\n%v\n%v", s1.logs, s2.logs)
	}
}

func TestReplayMatchesRecord(t *testing.T) {
	s1 := &sink{}
	events, rt := record(t, s1)
	if rt.Now() != 3*time.Second {
		t.Fatalf("virtual clock = %v, want 3s", rt.Now())
	}

	s2 := &sink{}
	rt2 := deter.NewReplay(events)
	if _, err := rt2.Run(workload(s2)); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !reflect.DeepEqual(s1.logs, s2.logs) {
		t.Fatalf("replay output differs:\nrecord: %v\nreplay: %v", s1.logs, s2.logs)
	}
	if rt2.Now() != rt.Now() {
		t.Fatalf("replay clock = %v, want %v", rt2.Now(), rt.Now())
	}

	// The injected failure must be replayed from the log, not recomputed.
	var sawFailure bool
	for _, l := range s2.logs {
		if strings.Contains(l, `injected failure for "b"`) {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Fatalf("replayed run lost the injected failure: %v", s2.logs)
	}
}

// TestExtraRequestDiverges replays the recording against task functions that
// issue one extra request; the divergence must be reported at that step.
func TestExtraRequestDiverges(t *testing.T) {
	s1 := &sink{}
	events, _ := record(t, s1)

	// Find the first step produced by worker-a (task 1).
	firstWorkerStep := -1
	for i, ev := range events {
		if ev.Task == 1 {
			firstWorkerStep = i
			break
		}
	}
	if firstWorkerStep < 0 {
		t.Fatal("recording has no worker-a events")
	}

	extra := func(env *deter.Env) {
		env.Spawn("worker-a", func(e *deter.Env) {
			e.Call("ping", "a") // the extra, unrecorded request
			e.Sleep(time.Second)
		})
		env.Spawn("worker-b", func(e *deter.Env) { e.Sleep(time.Second) })
		env.Sleep(500 * time.Millisecond)
		env.Call("fetch", "root")
	}

	s2 := &sink{}
	_ = s2
	_, err := deter.NewReplay(events).Run("root", extra)
	var div *deter.Divergence
	if !errors.As(err, &div) {
		t.Fatalf("err = %v, want *Divergence", err)
	}
	if div.Step != firstWorkerStep {
		t.Fatalf("divergence at step %d, want step %d", div.Step, firstWorkerStep)
	}
	if !strings.Contains(div.Actual, `"ping a"`) {
		t.Fatalf("divergence does not name the extra request: %v", div)
	}
}

// TestTruncatedRecordingFails replays a recording with its tail cut off;
// replay must report divergence at the first missing step, not success.
func TestTruncatedRecordingFails(t *testing.T) {
	s1 := &sink{}
	events, _ := record(t, s1)

	truncated := events[:len(events)-3]
	s2 := &sink{}
	_, err := deter.NewReplay(truncated).Run(workload(s2))
	var div *deter.Divergence
	if !errors.As(err, &div) {
		t.Fatalf("err = %v, want *Divergence", err)
	}
	if div.Step != len(truncated) {
		t.Fatalf("divergence at step %d, want step %d (first missing event)", div.Step, len(truncated))
	}
	if !strings.Contains(div.Expected, "end of recording") {
		t.Fatalf("unexpected divergence: %v", div)
	}
}

// TestUnconsumedTailFails covers the opposite case: the tasks finish exactly
// as recorded but the recording carries extra events at its tail (e.g. it
// came from a longer run). Replay must not claim success.
func TestUnconsumedTailFails(t *testing.T) {
	s1 := &sink{}
	events, _ := record(t, s1)

	extended := append([]deter.Event(nil), events...)
	extended = append(extended, deter.Event{Task: 1, Op: deter.OpSleep, Arg: "1s", At: 3 * time.Second})
	s2 := &sink{}
	_, err := deter.NewReplay(extended).Run(workload(s2))
	var div *deter.Divergence
	if !errors.As(err, &div) {
		t.Fatalf("err = %v, want *Divergence", err)
	}
	if div.Step != len(events) {
		t.Fatalf("divergence at step %d, want step %d (first tail event)", div.Step, len(events))
	}
	if !strings.Contains(div.Actual, "unconsumed tail") {
		t.Fatalf("unexpected divergence: %v", div)
	}
}

// TestTamperedEventDiverges flips one recorded argument; replay must flag
// exactly that step.
func TestTamperedEventDiverges(t *testing.T) {
	s1 := &sink{}
	events, _ := record(t, s1)

	tampered := append([]deter.Event(nil), events...)
	tampered[5].Arg = "fetch other"
	s2 := &sink{}
	_, err := deter.NewReplay(tampered).Run(workload(s2))
	var div *deter.Divergence
	if !errors.As(err, &div) {
		t.Fatalf("err = %v, want *Divergence", err)
	}
	if div.Step != 5 {
		t.Fatalf("divergence at step %d, want step 5", div.Step)
	}
}

// TestScheduleDivergence points a recorded step at a task that is no longer
// runnable; replay must flag the scheduling choice itself.
func TestScheduleDivergence(t *testing.T) {
	s1 := &sink{}
	events, _ := record(t, s1)

	tampered := append([]deter.Event(nil), events...)
	// Root (task 0) is done by step 7; scheduling it must be rejected.
	tampered[7].Task = 0
	s2 := &sink{}
	_, err := deter.NewReplay(tampered).Run(workload(s2))
	var div *deter.Divergence
	if !errors.As(err, &div) {
		t.Fatalf("err = %v, want *Divergence", err)
	}
	if div.Step != 7 || !strings.Contains(div.Actual, "not runnable") {
		t.Fatalf("unexpected divergence: %v", div)
	}
}

// TestVirtualClockNotRealTime sleeps far beyond any wall-clock budget; the
// run completes instantly because only the virtual clock advances.
func TestVirtualClockNotRealTime(t *testing.T) {
	s := &sink{}
	rt := deter.NewRecord(nil)
	events, err := rt.Run("root", func(env *deter.Env) {
		env.Sleep(1000 * time.Hour)
		s.add("woke at %s", env.Now())
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if rt.Now() != 1000*time.Hour {
		t.Fatalf("virtual clock = %v, want 1000h", rt.Now())
	}

	s2 := &sink{}
	if _, err := deter.NewReplay(events).Run("root", func(env *deter.Env) {
		env.Sleep(1000 * time.Hour)
		s2.add("woke at %s", env.Now())
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !reflect.DeepEqual(s.logs, s2.logs) {
		t.Fatalf("replay output differs: %v vs %v", s.logs, s2.logs)
	}
}

// TestFailureInjectionRecorded verifies the recorded failure, not a live
// handler, drives replayed tasks.
func TestFailureInjectionRecorded(t *testing.T) {
	s1 := &sink{}
	events, _ := record(t, s1)

	// Replay with no handler at all: outcomes must come from the recording.
	s2 := &sink{}
	if _, err := deter.NewReplay(events).Run(workload(s2)); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !reflect.DeepEqual(s1.logs, s2.logs) {
		t.Fatalf("replay output differs:\nrecord: %v\nreplay: %v", s1.logs, s2.logs)
	}
}
